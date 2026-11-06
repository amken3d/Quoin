// Package thumb emits Thumb-2 assembly for the Cortex-M33 (ARMv8-M
// Mainline, as in the RP2350) straight from the checked program, with no C
// compiler in between. Because the backend chooses every instruction, it
// knows every stack frame exactly and every instruction's cost, and it
// combines those costs over the structured control flow into a worst-case
// cycle and instruction bound per function.
//
// Code shape (v0, chosen for predictability over speed):
//   - every local and parameter has a stack slot; expressions evaluate into
//     callee-saved temporaries r4..r11, so calls never force spills;
//   - r0..r3 carry arguments and results, r12 is scratch;
//   - internal calls follow AAPCS register assignment; a fallible function
//     returns its error in r0 and a scalar value in r1;
//   - aggregates (structs, arrays) are passed and returned by address and
//     copied by the receiver.
package thumb

import (
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/amken3d/quoin/internal/ast"
	"github.com/amken3d/quoin/internal/check"
	"github.com/amken3d/quoin/internal/diag"
	"github.com/amken3d/quoin/internal/types"
)

// Options configure the backend.
type Options struct {
	Timing Timing
	Target *Target
}

// Result is the generated program and its exact resource figures.
type Result struct {
	Asm    string           // program assembly (data stack size patched later)
	Frames map[string]int64 // C/asm symbol -> exact stack bytes of that function
	Bounds map[*types.Fn]Bound
}

const (
	firstTemp = 4
	lastTemp  = 11
	scratch   = 12
)

var rn = [...]string{"r0", "r1", "r2", "r3", "r4", "r5", "r6", "r7", "r8", "r9", "r10", "r11", "r12"}

type val struct{ r, r2 int } // r2 >= 0 only for slices (ptr in r, len in r2)

func one(r int) val { return val{r, -1} }

type loopLabels struct{ brk, cont string }

type stmtState struct {
	lin            path
	brk, cont, ret path
	unb            *unbounded
}

type stub struct {
	label string
	code  string
}

type gen struct {
	p      *check.Program
	opt    Options
	t      Timing
	d      *diag.List
	bounds map[*types.Fn]Bound
	frames map[string]int64

	out   strings.Builder
	fb    *strings.Builder
	fn    *types.Fn
	fd    *ast.FnDecl
	nlab  int
	stubs []stub

	tempTop, tempMax  int
	slots             map[*types.Symbol]int64 // rel offset in locals area
	localTop, localMx int64
	tmpTop, tmpMax    int64
	outArgs           int64
	dfSlot, retpSlot  int64
	loops             []loopLabels
	st                stmtState
	sliceMax          map[*types.Symbol]int64
	failed            bool
}

// Generate emits the program. Unsupported constructs are reported as E208.
func Generate(p *check.Program, opt Options, d *diag.List) *Result {
	g := &gen{p: p, opt: opt, t: opt.Timing, d: d, bounds: map[*types.Fn]Bound{}, frames: map[string]int64{}}
	if g.t == (Timing{}) {
		g.t = DefaultTiming
	}
	order := g.topo()
	g.out.WriteString("\t.syntax unified\n\t.cpu cortex-m33\n\t.thumb\n")
	for _, fn := range order {
		if fn.Extern {
			g.externBound(fn)
			continue
		}
		g.function(fn)
	}
	g.data()
	return &Result{Asm: g.out.String(), Frames: g.frames, Bounds: g.bounds}
}

// topo orders reachable functions callees-first (the call graph is acyclic).
func (g *gen) topo() []*types.Fn {
	seen := map[*types.Fn]bool{}
	var order []*types.Fn
	var visit func(f *types.Fn)
	visit = func(f *types.Fn) {
		if seen[f] {
			return
		}
		seen[f] = true
		for _, c := range f.Calls {
			visit(c.Callee)
		}
		order = append(order, f)
	}
	for _, e := range g.p.Entries {
		visit(e)
	}
	return order
}

func (g *gen) unsupported(pos diag.Pos, what string) {
	g.failed = true
	g.d.ErrorFix(pos, "E208", what+" is not supported by the thumb backend yet", "use --target c for this program")
}

func (g *gen) externBound(fn *types.Fn) {
	b := Bound{Cycles: fn.CyclesDecl, Insns: fn.CyclesDecl}
	if fn.CyclesDecl < 0 {
		b = Bound{Unbounded: true, Why: "extern fn " + fn.Name + " declares no `cycles` bound", Pos: fn.Pos}
	}
	g.bounds[fn] = b
	d := fn.Decl.(*ast.FnDecl)
	for _, p := range fn.Params {
		switch p.Type.Kind {
		case types.Int, types.Bool, types.Enum, types.Slice, types.Regs, types.ErrorVal:
		default:
			g.unsupported(d.P, "extern parameter of type "+p.Type.String())
		}
	}
	if !scalar(fn.Ret) && fn.Ret.Kind != types.Void {
		g.unsupported(d.P, "extern result of type "+fn.Ret.String())
	}
}

// ---- emission helpers ------------------------------------------------------

func (g *gen) emit(c int64, format string, args ...any) {
	g.fb.WriteString("\t")
	fmt.Fprintf(g.fb, format, args...)
	g.fb.WriteString("\n")
	g.st.lin = g.st.lin.plus(path{c, 1, true})
}

func (g *gen) alu(format string, args ...any) { g.emit(g.t.ALU, format, args...) }

func (g *gen) newLabel() string {
	g.nlab++
	return fmt.Sprintf(".Lq%d", g.nlab)
}

func (g *gen) label(l string) { fmt.Fprintf(g.fb, "%s:\n", l) }

// trapIf emits a not-taken conditional branch to ql_trap. Trap paths end
// the program, so only the not-taken cost counts toward bounds.
func (g *gen) trapIf(cond string) { g.emit(g.t.NotTkn, "b%s.w ql_trap", cond) }

func (g *gen) movImm(r int, v uint32) {
	switch {
	case v <= 0xFFFF:
		g.alu("movw %s, #%d", rn[r], v)
	case ^v <= 0xFF:
		g.alu("mvn %s, #%d", rn[r], ^v)
	default:
		g.alu("movw %s, #%d", rn[r], v&0xFFFF)
		g.alu("movt %s, #%d", rn[r], v>>16)
	}
}

func (g *gen) movSym(r int, sym string) {
	g.alu("movw %s, #:lower16:%s", rn[r], sym)
	g.alu("movt %s, #:upper16:%s", rn[r], sym)
}

// cmpImm compares register r with constant v.
func (g *gen) cmpImm(r int, v int64) {
	if v >= 0 && v <= 255 {
		g.alu("cmp %s, #%d", rn[r], v)
		return
	}
	if v < 0 && v >= -255 {
		g.alu("cmn %s, #%d", rn[r], -v)
		return
	}
	g.movImm(scratch, uint32(v))
	g.alu("cmp %s, r12", rn[r])
}

func (g *gen) alloc(pos diag.Pos) int {
	if g.tempTop > lastTemp {
		g.unsupported(pos, "an expression deeper than 8 temporaries")
		return lastTemp
	}
	r := g.tempTop
	g.tempTop++
	if g.tempTop-1 > g.tempMax {
		g.tempMax = g.tempTop - 1
	}
	return r
}

func (g *gen) free(v val) {
	if v.r >= firstTemp && v.r <= lastTemp+1 {
		g.tempTop = v.r
	}
}

// local reserves a slot in the locals area and returns its token.
func (g *gen) local(size, align int64) int64 {
	g.localTop = types.AlignUp(g.localTop, max(align, 4))
	off := g.localTop
	g.localTop += types.AlignUp(size, 4)
	g.localMx = max(g.localMx, g.localTop)
	return off
}

func (g *gen) temp(size, align int64) int64 {
	g.tmpTop = types.AlignUp(g.tmpTop, max(align, 4))
	off := g.tmpTop
	g.tmpTop += types.AlignUp(size, 4)
	g.tmpMax = max(g.tmpMax, g.tmpTop)
	return off
}

func lslot(off int64) string { return fmt.Sprintf("@L%d@", off) }
func tslot(off int64) string { return fmt.Sprintf("@T%d@", off) }

// ---- types -----------------------------------------------------------------

func scalar(t *types.Type) bool {
	switch t.Kind {
	case types.Int, types.Bool, types.Enum, types.ErrorVal, types.UntypedInt:
		return true
	}
	return false
}

func aggregate(t *types.Type) bool { return t.Kind == types.Struct || t.Kind == types.Array }

// words is how many argument words a parameter of type t takes.
func words(t *types.Type) int {
	if t.Kind == types.Slice {
		return 2
	}
	return 1
}

func intOf(t *types.Type) *types.Type {
	switch t.Kind {
	case types.Enum:
		return t.Base
	case types.Bool:
		return types.IntT(1, false)
	case types.ErrorVal:
		return types.U16
	}
	return t
}

// loadOp/storeOp pick the memory instruction for a scalar of type t.
func loadOp(t *types.Type) string {
	it := intOf(t)
	switch types.CBits(it.Bits) {
	case 8:
		if it.Signed {
			return "ldrsb"
		}
		return "ldrb"
	case 16:
		if it.Signed {
			return "ldrsh"
		}
		return "ldrh"
	}
	return "ldr"
}

func storeOp(t *types.Type) string {
	switch types.CBits(intOf(t).Bits) {
	case 8:
		return "strb"
	case 16:
		return "strh"
	}
	return "str"
}

func fieldOffset(t *types.Type, name string) (int64, *types.Type) {
	var off int64
	for _, f := range t.Fields {
		s, a := f.Type.Layout()
		off = types.AlignUp(off, a)
		if f.Name == name {
			return off, f.Type
		}
		off += s
	}
	return 0, types.InvalidT
}

func (g *gen) checkSupported(t *types.Type, pos diag.Pos) {
	switch t.Kind {
	case types.Int:
		if t.Bits > 32 {
			g.unsupported(pos, t.String()+" (64-bit integers)")
		}
	case types.Array, types.Grow, types.Queue, types.Slice:
		g.checkSupported(t.Elem, pos)
	case types.Struct:
		for _, f := range t.Fields {
			g.checkSupported(f.Type, pos)
		}
	}
}

// ---- functions -------------------------------------------------------------

func (g *gen) function(fn *types.Fn) {
	fd := fn.Decl.(*ast.FnDecl)
	g.fn, g.fd = fn, fd
	var body strings.Builder
	g.fb = &body
	g.stubs = nil
	g.tempTop, g.tempMax = firstTemp, firstTemp-1
	g.slots = map[*types.Symbol]int64{}
	g.sliceMax = map[*types.Symbol]int64{}
	g.localTop, g.localMx, g.tmpTop, g.tmpMax, g.outArgs = 0, 0, 0, 0, 0
	g.loops = nil
	g.st = stmtState{lin: zero}

	for _, p := range fn.Params {
		g.checkSupported(p.Type, p.Pos)
	}
	g.checkSupported(fn.Ret, fd.P)

	// Incoming words: hidden result pointer, then parameters.
	w := 0
	if aggregate(fn.Ret) {
		g.retpSlot = g.local(4, 4)
		g.storeIncoming(w, g.retpSlot)
		w++
	}
	type copyIn struct {
		sym     *types.Symbol
		ptrSlot int64
	}
	var copies []copyIn
	for _, p := range fn.Params {
		switch {
		case aggregate(p.Type):
			ptr := g.local(4, 4)
			g.storeIncoming(w, ptr)
			copies = append(copies, copyIn{p, ptr})
			size, align := p.Type.Layout()
			g.slots[p] = g.local(size, align)
			w++
		case p.Type.Kind == types.Slice:
			s := g.local(8, 4)
			g.slots[p] = s
			g.storeIncoming(w, s)
			g.storeIncoming(w+1, s+4)
			w += 2
		default:
			s := g.local(4, 4)
			g.slots[p] = s
			g.storeIncoming(w, s)
			w++
		}
	}
	for _, c := range copies {
		size, _ := c.sym.Type.Layout()
		g.emit(g.t.Load, "ldr r1, [sp, #%s]", lslot(c.ptrSlot))
		g.alu("add r0, sp, #%s", lslot(g.slots[c.sym]))
		g.copyMem(0, 1, size)
	}
	if fn.DataFrame > 0 {
		g.dfSlot = g.local(4, 4)
		g.movSym(scratch, "ql_dsp")
		g.emit(g.t.Load, "ldr r1, [r12]")
		g.emit(g.t.Store, "str r1, [sp, #%s]", lslot(g.dfSlot))
		g.addImm(2, 1, fn.DataFrame)
		g.emit(g.t.Store, "str r2, [r12]")
	}
	g.whereBounds(fd)
	for _, wc := range fd.Where {
		v := g.expr(wc)
		g.alu("cmp %s, #0", rn[v.r])
		g.trapIf("eq")
		g.free(v)
	}
	pre := flow{fall: g.st.lin}
	g.st = stmtState{lin: zero}
	bf := seq(pre, g.block(fd.Body))
	if bf.fall.ok {
		// Implicit return at the end.
		saved := g.st
		g.st = stmtState{lin: zero}
		if fn.Fallible() {
			g.alu("movs r0, #0")
		}
		if fn.Main {
			g.alu("movs r0, #0")
		}
		g.release()
		g.fb.WriteString("@EPI@\n")
		tail := g.st.lin
		g.st = saved
		bf.ret = maxp(bf.ret, bf.fall.plus(tail))
	}
	for _, s := range g.stubs {
		body.WriteString(s.label + ":\n" + s.code)
	}

	// Frame layout: [outgoing args][aggregate temps][locals] then pushed regs.
	nregs := g.tempMax - firstTemp + 1
	pushed := int64(4 * (nregs + 1))
	tmpBase := g.outArgs
	locBase := tmpBase + types.AlignUp(g.tmpMax, 4)
	frame := locBase + g.localMx
	if (frame+pushed)%8 != 0 {
		frame += 4
	}
	regs := "lr"
	popRegs := "pc"
	if nregs > 0 {
		list := "r4"
		if nregs > 1 {
			list = fmt.Sprintf("r4-r%d", firstTemp+nregs-1)
		}
		regs = list + ", lr"
		popRegs = list + ", pc"
	}
	var prolog, epi strings.Builder
	prologCost := path{1 + int64(nregs+1), 1, true}
	fmt.Fprintf(&prolog, "\tpush {%s}\n", regs)
	epiCost := path{1 + int64(nregs+1) + g.t.PopPC, 1, true}
	if frame > 0 {
		fmt.Fprintf(&prolog, "%s", spAdjust("sub", frame))
		fmt.Fprintf(&epi, "%s", spAdjust("add", frame))
		n := int64(1)
		if frame > 4095 {
			n = 3
		}
		prologCost = prologCost.plus(path{n * g.t.ALU, n, true})
		epiCost = epiCost.plus(path{n * g.t.ALU, n, true})
	}
	fmt.Fprintf(&epi, "\tpop {%s}", popRegs)

	text := body.String()
	text = strings.ReplaceAll(text, "@EPI@", epi.String())
	text = slotRe.ReplaceAllStringFunc(text, func(m string) string {
		sub := slotRe.FindStringSubmatch(m)
		n, _ := strconv.ParseInt(sub[2], 10, 64)
		switch sub[1] {
		case "L":
			n += locBase
		case "T":
			n += tmpBase
		case "A":
			n += frame + pushed
		}
		return strconv.FormatInt(n, 10)
	})

	g.frames[fn.CName] = frame + pushed
	var b Bound
	switch {
	case bf.unb != nil:
		b = Bound{Unbounded: true, Why: bf.unb.why, Pos: bf.unb.pos}
	case !bf.ret.ok:
		b = Bound{NeverReturns: true}
	default:
		r := prologCost.plus(bf.ret).plus(epiCost)
		b = Bound{Cycles: r.cyc, Insns: r.ins}
	}
	g.bounds[fn] = b

	sec := ".text." + fn.CName
	fmt.Fprintf(&g.out, "\n\t.section %s, \"ax\", %%progbits\n\t.align 2\n", sec)
	if fn.ISR || fn.Main {
		fmt.Fprintf(&g.out, "\t.global %s\n", fn.CName)
	}
	fmt.Fprintf(&g.out, "\t.type %s, %%function\n\t.thumb_func\n%s:\n", fn.CName, fn.CName)
	fmt.Fprintf(&g.out, "\t@ frame %dB, wcet %s cycles\n", frame+pushed, b)
	g.out.WriteString(prolog.String())
	g.out.WriteString(text)
	fmt.Fprintf(&g.out, "\t.size %s, .-%s\n", fn.CName, fn.CName)
}

var slotRe = regexp.MustCompile(`@([LTA])(\d+)@`)

func spAdjust(op string, n int64) string {
	if n <= 4095 {
		return fmt.Sprintf("\t%sw sp, sp, #%d\n", op, n)
	}
	return fmt.Sprintf("\tmovw r12, #%d\n\tmovt r12, #%d\n\t%s sp, sp, r12\n", n&0xFFFF, n>>16, op)
}

// storeIncoming moves incoming argument word w into a local slot.
func (g *gen) storeIncoming(w int, slot int64) {
	if w < 4 {
		g.emit(g.t.Store, "str r%d, [sp, #%s]", w, lslot(slot))
		return
	}
	g.emit(g.t.Load, "ldr r12, [sp, #@A%d@]", (w-4)*4)
	g.emit(g.t.Store, "str r12, [sp, #%s]", lslot(slot))
}

func (g *gen) addImm(dst, src int, v int64) {
	if v >= 0 && v <= 4095 {
		g.alu("addw %s, %s, #%d", rn[dst], rn[src], v)
		return
	}
	g.movImm(scratch, uint32(v))
	g.alu("add %s, %s, r12", rn[dst], rn[src])
}

// release restores the data-stack pointer (clobbers r2, r12 only).
func (g *gen) release() {
	if g.fn.DataFrame == 0 {
		return
	}
	g.emit(g.t.Load, "ldr r2, [sp, #%s]", lslot(g.dfSlot))
	g.movSym(scratch, "ql_dsp")
	g.emit(g.t.Store, "str r2, [r12]")
}

// whereBounds records `p.len <= N` facts from where clauses as loop bounds
// for slice parameters. They are sound: the clause is checked at entry.
func (g *gen) whereBounds(fd *ast.FnDecl) {
	for _, w := range fd.Where {
		b, ok := w.(*ast.Binary)
		if !ok || (b.Op != "<=" && b.Op != "<") {
			continue
		}
		f, ok := b.X.(*ast.Field)
		if !ok || f.Kind != ast.FieldLen {
			continue
		}
		id, ok := f.X.(*ast.Ident)
		k := b.Y.Info().Const
		if !ok || id.Sym == nil || k == nil {
			continue
		}
		n := k.Int64()
		if b.Op == "<" {
			n--
		}
		g.sliceMax[id.Sym] = n
	}
}

// copyMem copies size bytes from [src] to [dst] (registers), unrolled,
// using r2/r3 as scratch. Both addresses are 4-byte aligned except for
// byte-sized tails.
func (g *gen) copyMem(dst, src int, size int64) {
	var off int64
	for ; off+4 <= size; off += 4 {
		g.emit(g.t.Load, "ldr r3, [%s, #%d]", rn[src], off)
		g.emit(g.t.Store, "str r3, [%s, #%d]", rn[dst], off)
	}
	for ; off < size; off++ {
		g.emit(g.t.Load, "ldrb r3, [%s, #%d]", rn[src], off)
		g.emit(g.t.Store, "strb r3, [%s, #%d]", rn[dst], off)
	}
}

func (g *gen) zeroMem(dst int, size int64) {
	g.alu("movs r3, #0")
	var off int64
	for ; off+4 <= size; off += 4 {
		g.emit(g.t.Store, "str r3, [%s, #%d]", rn[dst], off)
	}
	for ; off < size; off++ {
		g.emit(g.t.Store, "strb r3, [%s, #%d]", rn[dst], off)
	}
}

// ---- statements ------------------------------------------------------------

func (g *gen) here() flow {
	return flow{fall: g.st.lin, brk: g.st.brk, cont: g.st.cont, ret: g.st.ret, unb: g.st.unb}
}

func (g *gen) block(b *ast.Block) flow {
	saved := g.localTop
	f := flow{fall: zero}
	for _, s := range b.Stmts {
		f = seq(f, g.stmt(s))
	}
	g.localTop = saved
	return f
}

func (g *gen) stmt(s ast.Stmt) flow {
	saved := g.st
	savedTmp := g.tmpTop
	g.st = stmtState{lin: zero}
	fmt.Fprintf(g.fb, "\t@ %s:%d\n", baseName(s.SPos().File), s.SPos().Line)
	var f flow
	switch s := s.(type) {
	case *ast.Block:
		f = g.block(s)
	case *ast.IfStmt:
		f = g.ifStmt(s)
	case *ast.ForStmt:
		f = g.forStmt(s)
	case *ast.BranchStmt:
		lb := g.loops[len(g.loops)-1]
		target := lb.brk
		if s.Tok == "continue" {
			target = lb.cont
		}
		g.emit(g.t.Taken, "b.w %s", target)
		f = g.here()
		if s.Tok == "break" {
			f.brk = maxp(f.brk, f.fall)
		} else {
			f.cont = maxp(f.cont, f.fall)
		}
		f.fall = path{}
	case *ast.ReturnStmt:
		g.ret(s)
		f = g.here()
		f.ret = maxp(f.ret, f.fall)
		f.fall = path{}
	default:
		g.simple(s)
		f = g.here()
	}
	g.st = saved
	g.tmpTop = savedTmp
	return f
}

func baseName(p string) string {
	if i := strings.LastIndexAny(p, "/\\"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func (g *gen) simple(s ast.Stmt) {
	switch s := s.(type) {
	case *ast.DefineStmt:
		if s.Sym == nil {
			return
		}
		t := s.Sym.Type
		g.checkSupported(t, s.P)
		size, align := t.Layout()
		if t.Kind == types.Slice || t.Kind == types.Regs || scalar(t) {
			size, align = int64(4*words(t)), 4
		}
		v := g.exprAs(s.Value, t)
		slot := g.local(size, align)
		g.slots[s.Sym] = slot
		if t.Kind == types.Slice {
			g.sliceMax[s.Sym] = g.maxLen(s.Value)
		}
		g.storeLocal(slot, t, v)
		g.free(v)
	case *ast.VarStmt:
		if s.Sym == nil {
			return
		}
		t := s.Sym.Type
		g.checkSupported(t, s.P)
		if s.Sym.OnDataStack {
			slot := g.local(4, 4)
			g.slots[s.Sym] = slot
			r := g.alloc(s.P)
			g.emit(g.t.Load, "ldr %s, [sp, #%s]", rn[r], lslot(g.dfSlot))
			g.addImm(r, r, s.Sym.DataOff)
			g.emit(g.t.Store, "str %s, [sp, #%s]", rn[r], lslot(slot))
			g.alu("movs r3, #0")
			g.emit(g.t.Store, "str r3, [%s]", rn[r])
			g.free(one(r))
			return
		}
		size, align := t.Layout()
		if scalar(t) {
			size, align = 4, 4
		}
		if s.Value != nil {
			v := g.exprAs(s.Value, t)
			slot := g.local(size, align)
			g.slots[s.Sym] = slot
			g.storeLocal(slot, t, v)
			g.free(v)
			return
		}
		slot := g.local(size, align)
		g.slots[s.Sym] = slot
		if scalar(t) {
			g.alu("movs r3, #0")
			g.emit(g.t.Store, "str r3, [sp, #%s]", lslot(slot))
			return
		}
		g.alu("add r0, sp, #%s", lslot(slot))
		g.zeroMem(0, size)
	case *ast.AssignStmt:
		g.assign(s)
	case *ast.ExprStmt:
		g.exprStmt(s.X)
	}
}

// storeLocal stores value v of type t into a local slot.
func (g *gen) storeLocal(slot int64, t *types.Type, v val) {
	switch {
	case t.Kind == types.Slice:
		g.emit(g.t.Store, "str %s, [sp, #%s]", rn[v.r], lslot(slot))
		g.emit(g.t.Store, "str %s, [sp, #%s]", rn[v.r2], lslot(slot+4))
	case aggregate(t):
		size, _ := t.Layout()
		g.alu("add r0, sp, #%s", lslot(slot))
		g.alu("mov r1, %s", rn[v.r])
		g.copyMem(0, 1, size)
	default:
		g.emit(g.t.Store, "str %s, [sp, #%s]", rn[v.r], lslot(slot))
	}
}

func (g *gen) ifStmt(s *ast.IfStmt) flow {
	elseL, endL := g.newLabel(), g.newLabel()
	g.condJump(s.Cond, elseL, false)
	head := g.here()
	// The not-taken cost of the branch is already in head (condJump charges
	// NotTkn); the else path pays the difference to a taken branch.
	thenF := g.block(s.Then)
	var elseF flow
	if s.Else != nil {
		jump := stmtState{lin: zero}
		saved := g.st
		g.st = jump
		g.emit(g.t.Taken, "b.w %s", endL)
		jmp := g.st.lin
		g.st = saved
		if thenF.fall.ok {
			thenF.fall = thenF.fall.plus(jmp)
		}
		g.label(elseL)
		elseF = g.stmt(s.Else)
	} else {
		g.label(elseL)
		elseF = flow{fall: zero}
	}
	g.label(endL)
	extra := flow{fall: path{g.t.Taken - g.t.NotTkn, 0, true}}
	return seq(head, alt(thenF, seq(extra, elseF)))
}

func (g *gen) forStmt(s *ast.ForStmt) flow {
	top, cont, brk := g.newLabel(), g.newLabel(), g.newLabel()
	g.loops = append(g.loops, loopLabels{brk, cont})
	defer func() { g.loops = g.loops[:len(g.loops)-1] }()
	savedLocal := g.localTop
	defer func() { g.localTop = savedLocal }()

	var trips int64 = -1 // -1: unknown
	var why string
	switch s.Kind {
	case ast.ForEver:
		why = "`for` without a condition"
	case ast.ForCond:
		why = "`for cond` has no iteration bound"
	}

	// Setup (runs once) is charged to the head.
	var cond, step func()
	switch s.Kind {
	case ast.ForRange:
		t := s.VarSym.Type
		lo := g.exprAs(s.Lo, t)
		iv := g.local(4, 4)
		g.slots[s.VarSym] = iv
		g.emit(g.t.Store, "str %s, [sp, #%s]", rn[lo.r], lslot(iv))
		g.free(lo)
		hi := g.exprAs(s.Hi, t)
		hv := g.local(4, 4)
		g.emit(g.t.Store, "str %s, [sp, #%s]", rn[hi.r], lslot(hv))
		g.free(hi)
		cc := "ge"
		if !intOf(t).Signed {
			cc = "hs"
		}
		cond = func() {
			g.emit(g.t.Load, "ldr r0, [sp, #%s]", lslot(iv))
			g.emit(g.t.Load, "ldr r1, [sp, #%s]", lslot(hv))
			g.alu("cmp r0, r1")
			g.emit(g.t.NotTkn, "b%s.w %s", cc, brk)
		}
		step = func() {
			g.emit(g.t.Load, "ldr r0, [sp, #%s]", lslot(iv))
			g.alu("adds r0, #1")
			g.emit(g.t.Store, "str r0, [sp, #%s]", lslot(iv))
		}
		loLo, _ := rangeOf(s.Lo)
		_, hiHi := rangeOf(s.Hi)
		if loLo != nil && hiHi != nil {
			trips = max(0, new(big.Int).Sub(hiHi, loLo).Int64())
			if new(big.Int).Sub(hiHi, loLo).BitLen() > 40 {
				trips = 1 << 40
			}
		} else {
			why = "range bounds are not known"
		}
	case ast.ForEach:
		it := s.Iter.Info().Type
		et := it.Elem
		st := &types.Type{Kind: types.Slice, Elem: et}
		v := g.exprAs(s.Iter, st)
		ps := g.local(8, 4)
		g.emit(g.t.Store, "str %s, [sp, #%s]", rn[v.r], lslot(ps))
		g.emit(g.t.Store, "str %s, [sp, #%s]", rn[v.r2], lslot(ps+4))
		g.free(v)
		ix := g.local(4, 4)
		g.alu("movs r0, #0")
		g.emit(g.t.Store, "str r0, [sp, #%s]", lslot(ix))
		if s.IndexSym != nil {
			g.slots[s.IndexSym] = ix
		}
		esize, ealign := et.Layout()
		var ev int64
		if aggregate(et) {
			ev = g.local(esize, ealign)
		} else {
			ev = g.local(4, 4)
		}
		g.slots[s.VarSym] = ev
		cond = func() {
			g.emit(g.t.Load, "ldr r0, [sp, #%s]", lslot(ix))
			g.emit(g.t.Load, "ldr r1, [sp, #%s]", lslot(ps+4))
			g.alu("cmp r0, r1")
			g.emit(g.t.NotTkn, "bhs.w %s", brk)
			g.emit(g.t.Load, "ldr r1, [sp, #%s]", lslot(ps))
			g.elemAddr(1, 1, 0, esize)
			if aggregate(et) {
				g.alu("add r0, sp, #%s", lslot(ev))
				g.copyMem(0, 1, esize)
			} else {
				g.emit(g.t.Load, "%s r0, [r1]", loadOp(et))
				g.emit(g.t.Store, "str r0, [sp, #%s]", lslot(ev))
			}
		}
		step = func() {
			g.emit(g.t.Load, "ldr r0, [sp, #%s]", lslot(ix))
			g.alu("adds r0, #1")
			g.emit(g.t.Store, "str r0, [sp, #%s]", lslot(ix))
		}
		switch it.Kind {
		case types.Array:
			trips = it.Len
		case types.Grow:
			trips = it.Len
		default:
			if n := g.maxLen(s.Iter); n >= 0 {
				trips = n
			} else {
				why = "the slice has no length bound (add `where " + exprName(s.Iter) + ".len <= N`)"
			}
		}
	}
	head := g.here()

	// Condition (each iteration, plus the final failing check).
	g.label(top)
	cs := g.st
	g.st = stmtState{lin: zero}
	var condFlow flow
	switch s.Kind {
	case ast.ForCond:
		g.condJump(s.Cond, brk, false)
		condFlow = g.here()
	case ast.ForRange, ast.ForEach:
		cond()
		condFlow = g.here()
	default:
		condFlow = flow{fall: zero}
	}
	g.st = cs
	body := g.block(s.Body)
	g.label(cont)
	g.st = stmtState{lin: zero}
	if step != nil {
		step()
	}
	g.emit(g.t.Taken, "b.w %s", top)
	stepCost := g.st.lin
	g.st = cs
	g.label(brk)

	iter := condFlow.fall.plus(maxp(body.fall, body.cont)).plus(stepCost)
	exitCheck := condFlow.fall.plus(path{g.t.Taken - g.t.NotTkn, 0, true})
	var f flow
	switch {
	case trips < 0:
		f = flow{fall: zero, unb: &unbounded{pos: s.P, why: why}}
		if s.Kind == ast.ForEver && !body.brk.ok {
			// Never falls through. Returns from inside still need a trip bound.
			f = flow{unb: nil}
			if body.ret.ok {
				f.unb = &unbounded{pos: s.P, why: why}
			}
		}
	default:
		f.fall = iter.times(trips).plus(exitCheck)
		if trips > 0 {
			before := iter.times(trips - 1).plus(condFlow.fall)
			f.fall = maxp(f.fall, before.plus(body.brk))
			f.ret = before.plus(body.ret)
		}
		f.unb = body.unb
		if f.unb == nil {
			f.unb = condFlow.unb
		}
	}
	return seq(head, f)
}

// rangeOf returns known integer bounds of an expression.
func rangeOf(e ast.Expr) (lo, hi *big.Int) {
	in := e.Info()
	if in.Const != nil {
		return in.Const, in.Const
	}
	if in.Lo != nil && in.Hi != nil {
		return in.Lo, in.Hi
	}
	t := in.Type
	if t != nil && t.Kind == types.Enum {
		t = t.Base
	}
	if t != nil && t.Kind == types.Int {
		return t.Min(), t.Max()
	}
	return nil, nil
}

// maxLen bounds the length of a slice-valued expression, or -1.
func (g *gen) maxLen(e ast.Expr) int64 {
	t := e.Info().Type
	switch t.Kind {
	case types.Array, types.Grow:
		return t.Len
	}
	switch x := e.(type) {
	case *ast.Paren:
		return g.maxLen(x.X)
	case *ast.Ident:
		if n, ok := g.sliceMax[x.Sym]; ok {
			return n
		}
	case *ast.SliceExpr:
		base := g.maxLen(x.X)
		if x.Hi != nil {
			if _, hi := rangeOf(x.Hi); hi != nil && (base < 0 || hi.Int64() < base) && hi.IsInt64() {
				base = hi.Int64()
			}
		}
		return base
	}
	return -1
}

func exprName(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return "s"
}

func (g *gen) ret(s *ast.ReturnStmt) {
	fn := g.fn
	switch {
	case s.ErrTag != "":
		g.movImm(0, uint32(g.errCode(s.ErrTag)))
	case s.Value != nil:
		v := g.exprAs(s.Value, fn.Ret)
		switch {
		case aggregate(fn.Ret):
			size, _ := fn.Ret.Layout()
			g.emit(g.t.Load, "ldr r0, [sp, #%s]", lslot(g.retpSlot))
			g.alu("mov r1, %s", rn[v.r])
			g.copyMem(0, 1, size)
			if fn.Fallible() {
				g.alu("movs r0, #0")
			}
		case fn.Fallible():
			g.alu("mov r1, %s", rn[v.r])
			g.alu("movs r0, #0")
		default:
			g.alu("mov r0, %s", rn[v.r])
		}
		g.free(v)
	case fn.Fallible(), fn.Main:
		g.alu("movs r0, #0")
	}
	g.release()
	g.fb.WriteString("@EPI@\n")
}

func (g *gen) errCode(name string) int64 {
	for _, e := range g.p.ErrTags {
		if e.Name == name {
			return e.Const.Int64()
		}
	}
	return 0
}

// ---- data ------------------------------------------------------------------

func (g *gen) data() {
	var bss, data, ro strings.Builder
	for _, sym := range g.p.Globals {
		t := sym.Type
		if t == nil || t.Kind == types.Invalid {
			continue
		}
		size, align := t.Layout()
		if t.Kind == types.Queue {
			esize, ealign := t.Elem.Layout()
			size = types.AlignUp(8, max(ealign, 4)) + esize*(t.Len+1)
			align = max(align, 4)
		}
		d, _ := sym.Decl.(*ast.VarDecl)
		if d != nil && d.Value != nil {
			fmt.Fprintf(&data, "\t.align %d\n%s:\n", log2(align), sym.CName)
			g.initData(&data, t, d.Value)
		} else {
			fmt.Fprintf(&bss, "\t.align %d\n%s:\n\t.space %d\n", log2(align), sym.CName, max(size, 1))
		}
	}
	for _, c := range g.p.Consts {
		if c.Sym == nil || c.Sym.Type == nil || c.Sym.Const != nil || !aggregate(c.Sym.Type) {
			continue
		}
		_, align := c.Sym.Type.Layout()
		fmt.Fprintf(&ro, "\t.align %d\n%s:\n", log2(align), c.Sym.CName)
		g.initData(&ro, c.Sym.Type, c.Value)
	}
	if ro.Len() > 0 {
		g.out.WriteString("\n\t.section .rodata.quoin, \"a\"\n" + ro.String())
	}
	if data.Len() > 0 {
		g.out.WriteString("\n\t.section .data.quoin, \"aw\"\n" + data.String())
	}
	if bss.Len() > 0 {
		g.out.WriteString("\n\t.section .bss.quoin, \"aw\", %nobits\n" + bss.String())
	}
}

func log2(n int64) int {
	k := 0
	for int64(1)<<k < n {
		k++
	}
	return k
}

// initData writes a static initializer for value e of type t.
func (g *gen) initData(b *strings.Builder, t *types.Type, e ast.Expr) {
	size, _ := t.Layout()
	if k := e.Info().Const; k != nil && scalar(t) {
		v := k.Int64()
		switch size {
		case 1:
			fmt.Fprintf(b, "\t.byte %d\n", v&0xFF)
		case 2:
			fmt.Fprintf(b, "\t.short %d\n", v&0xFFFF)
		default:
			fmt.Fprintf(b, "\t.word %d\n", uint32(v))
		}
		return
	}
	switch x := e.(type) {
	case *ast.Paren:
		g.initData(b, t, x.X)
	case *ast.BoolLit:
		v := 0
		if x.Val {
			v = 1
		}
		fmt.Fprintf(b, "\t.byte %d\n", v)
	case *ast.ArrayLit:
		for _, el := range x.Elems {
			g.initData(b, t.Elem, el)
		}
	case *ast.StructLit:
		var off int64
		for _, f := range t.Fields {
			fs, fa := f.Type.Layout()
			aligned := types.AlignUp(off, fa)
			if aligned > off {
				fmt.Fprintf(b, "\t.space %d\n", aligned-off)
			}
			off = aligned
			var val ast.Expr
			for _, fi := range x.Fields {
				if fi.Name == f.Name {
					val = fi.Value
				}
			}
			if val == nil {
				fmt.Fprintf(b, "\t.space %d\n", fs)
			} else {
				g.initData(b, f.Type, val)
			}
			off += fs
		}
		if size > off {
			fmt.Fprintf(b, "\t.space %d\n", size-off)
		}
	case *ast.Ident:
		if d, ok := x.Sym.Decl.(*ast.ConstDecl); ok {
			g.initData(b, t, d.Value)
			return
		}
		fmt.Fprintf(b, "\t.space %d\n", size)
	default:
		fmt.Fprintf(b, "\t.space %d\n", size)
	}
}

// DataStack returns the assembly defining the shared data stack.
func DataStack(bytes int64) string {
	var b strings.Builder
	b.WriteString("\n\t.section .data.quoin_dsp, \"aw\"\n\t.align 2\n\t.global ql_dsp\nql_dsp:\n\t.word ql_dstack\n")
	fmt.Fprintf(&b, "\t.section .bss.quoin_dstack, \"aw\", %%nobits\n\t.align 3\n\t.global ql_dstack, ql_dstack_end\nql_dstack:\n\t.space %d\nql_dstack_end:\n", max(bytes, 8))
	return b.String()
}

// SortedBounds lists bounds by function name, for stable output.
func SortedBounds(m map[*types.Fn]Bound) []*types.Fn {
	var fs []*types.Fn
	for f := range m {
		fs = append(fs, f)
	}
	sort.Slice(fs, func(i, j int) bool { return fs[i].Name < fs[j].Name })
	return fs
}
