package thumb

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/amken3d/quoin/internal/ast"
	"github.com/amken3d/quoin/internal/types"
)

func (p path) minus(q path) path {
	if !p.ok || !q.ok {
		return path{}
	}
	return path{p.cyc - q.cyc, p.ins - q.ins, true}
}

func (g *gen) since(start path) path { return g.st.lin.minus(start) }

// outOfLine generates code into a stub placed after the function body and
// returns its label and the cost of running it (excluding any epilogue).
func (g *gen) outOfLine(f func()) (string, flow) {
	label := g.newLabel()
	saved, savedSt := g.fb, g.st
	var buf strings.Builder
	g.fb = &buf
	g.st = stmtState{lin: zero}
	f()
	fl := g.here()
	g.fb, g.st = saved, savedSt
	g.stubs = append(g.stubs, stub{label, buf.String()})
	return label, fl
}

// mergeExit folds the flow of a stub reached by a taken branch at the
// current point into the statement's exit paths.
func (g *gen) mergeExit(at path, fl flow) {
	base := at.plus(path{g.t.Taken, 0, true})
	g.st.brk = maxp(g.st.brk, base.plus(fl.brk))
	g.st.cont = maxp(g.st.cont, base.plus(fl.cont))
	g.st.ret = maxp(g.st.ret, base.plus(fl.ret))
	if g.st.unb == nil && fl.unb != nil {
		g.st.unb = fl.unb
	}
}

// ---- conditions ------------------------------------------------------------

// condJump emits code that branches to label when e == jumpIf and falls
// through otherwise. It returns the cost of the jumping and falling paths
// from its start, and leaves g.st.lin on the falling path.
func (g *gen) condJump(e ast.Expr, label string, jumpIf bool) (jump, fall path) {
	start := g.st.lin
	switch x := e.(type) {
	case *ast.Paren:
		return g.condJump(x.X, label, jumpIf)
	case *ast.Unary:
		if x.Op == "!" {
			return g.condJump(x.X, label, !jumpIf)
		}
	case *ast.BoolLit:
		if x.Val == jumpIf {
			g.emit(g.t.Taken, "b.w %s", label)
			return g.since(start), path{}
		}
		return path{}, zero
	case *ast.Binary:
		switch x.Op {
		case "&&", "||":
			and := x.Op == "&&"
			if and != jumpIf {
				// a&&b jump-if-false / a||b jump-if-true: either side jumps.
				aJ, aF := g.condJump(x.X, label, jumpIf)
				bJ, bF := g.condJump(x.Y, label, jumpIf)
				g.st.lin = start.plus(aF).plus(bF)
				return maxp(aJ, aF.plus(bJ)), aF.plus(bF)
			}
			// a&&b jump-if-true / a||b jump-if-false: a short-circuits past b.
			skip := g.newLabel()
			aJ, aF := g.condJump(x.X, skip, !jumpIf)
			bJ, bF := g.condJump(x.Y, label, jumpIf)
			g.label(skip)
			fall = maxp(aJ, aF.plus(bF))
			g.st.lin = start.plus(fall)
			return aF.plus(bJ), fall
		case "==", "!=", "<", "<=", ">", ">=":
			ot := operandType(x.X.Info().Type, x.Y.Info().Type)
			a := g.exprAs(x.X, ot)
			b := g.exprAs(x.Y, ot)
			g.alu("cmp %s, %s", rn[a.r], rn[b.r])
			g.free(a)
			cc := condCode(x.Op, signedOf(ot))
			if !jumpIf {
				cc = invert(cc)
			}
			return g.branch(start, cc, label)
		}
	}
	v := g.expr(e)
	g.alu("cmp %s, #0", rn[v.r])
	g.free(v)
	cc := "ne"
	if !jumpIf {
		cc = "eq"
	}
	return g.branch(start, cc, label)
}

func (g *gen) branch(start path, cc, label string) (jump, fall path) {
	before := g.since(start)
	g.fb.WriteString(fmt.Sprintf("\tb%s.w %s\n", cc, label))
	jump = before.plus(path{g.t.Taken, 1, true})
	fall = before.plus(path{g.t.NotTkn, 1, true})
	g.st.lin = start.plus(fall)
	return
}

func signedOf(t *types.Type) bool {
	t = intOf(t)
	return t.Kind == types.Int && t.Signed
}

func condCode(op string, signed bool) string {
	switch op {
	case "==":
		return "eq"
	case "!=":
		return "ne"
	case "<":
		if signed {
			return "lt"
		}
		return "lo"
	case "<=":
		if signed {
			return "le"
		}
		return "ls"
	case ">":
		if signed {
			return "gt"
		}
		return "hi"
	case ">=":
		if signed {
			return "ge"
		}
		return "hs"
	}
	return "al"
}

func invert(cc string) string {
	return map[string]string{"eq": "ne", "ne": "eq", "lt": "ge", "ge": "lt", "le": "gt", "gt": "le",
		"lo": "hs", "hs": "lo", "ls": "hi", "hi": "ls"}[cc]
}

func operandType(x, y *types.Type) *types.Type {
	if x.Kind == types.UntypedInt {
		return y
	}
	if y.Kind == types.UntypedInt {
		return x
	}
	if types.Widens(x, y) {
		return y
	}
	return x
}

// ---- values ----------------------------------------------------------------

// exprAs evaluates e for a context of type to (arrays and grow arrays
// become (ptr, len) views where a slice is expected).
func (g *gen) exprAs(e ast.Expr, to *types.Type) val {
	from := e.Info().Type
	if to != nil && to.Kind == types.Slice && from != nil && (from.Kind == types.Array || from.Kind == types.Grow) {
		r := g.alloc(e.EPos())
		r2 := g.alloc(e.EPos())
		if from.Kind == types.Array {
			a := g.addr(e)
			g.alu("mov %s, %s", rn[r], rn[a])
			g.free(one(a))
			g.movImm(r2, uint32(from.Len))
		} else {
			p := g.expr(e)
			g.emit(g.t.Load, "ldr %s, [%s]", rn[r2], rn[p.r])
			g.alu("adds %s, %s, #4", rn[r], rn[p.r])
			g.free(p)
		}
		return val{r, r2}
	}
	return g.expr(e)
}

func (g *gen) expr(e ast.Expr) val {
	in := e.Info()
	if in.Const != nil && in.Type != nil && in.Type.Kind != types.Struct && in.Type.Kind != types.Array {
		r := g.alloc(e.EPos())
		g.movImm(r, uint32(in.Const.Int64()))
		return one(r)
	}
	switch x := e.(type) {
	case *ast.Paren:
		return g.expr(x.X)
	case *ast.BoolLit:
		r := g.alloc(x.P)
		if x.Val {
			g.alu("movs %s, #1", rn[r])
		} else {
			g.alu("movs %s, #0", rn[r])
		}
		return one(r)
	case *ast.Ident:
		return g.ident(x)
	case *ast.Unary:
		return g.unary(x)
	case *ast.Binary:
		return g.binary(x)
	case *ast.Call:
		return g.call(x)
	case *ast.Index:
		a := g.indexAddr(x)
		if aggregate(in.Type) {
			return one(a)
		}
		g.emit(g.t.Load, "%s %s, [%s]", loadOp(in.Type), rn[a], rn[a])
		return one(a)
	case *ast.SliceExpr:
		return g.slice(x)
	case *ast.Field:
		return g.field(x)
	case *ast.Try:
		return g.try(x)
	case *ast.Catch:
		return g.catchExpr(x)
	case *ast.StructLit:
		size, align := in.Type.Layout()
		off := g.temp(size, align)
		r := g.alloc(x.P)
		g.alu("add %s, sp, #%s", rn[r], tslot(off))
		g.zeroMem(r, size)
		for _, fi := range x.Fields {
			fo, ft := fieldOffset(in.Type, fi.Name)
			v := g.exprAs(fi.Value, ft)
			g.storeAt(r, fo, ft, v)
			g.free(v)
		}
		return one(r)
	case *ast.ArrayLit:
		size, align := in.Type.Layout()
		off := g.temp(size, align)
		r := g.alloc(x.P)
		g.alu("add %s, sp, #%s", rn[r], tslot(off))
		esize, _ := in.Type.Elem.Layout()
		for i, el := range x.Elems {
			v := g.exprAs(el, in.Type.Elem)
			g.storeAt(r, int64(i)*esize, in.Type.Elem, v)
			g.free(v)
		}
		return one(r)
	case *ast.EnumLit:
		r := g.alloc(x.P)
		g.movImm(r, uint32(in.Const.Int64()))
		return one(r)
	}
	g.unsupported(e.EPos(), fmt.Sprintf("expression %T", e))
	return one(g.alloc(e.EPos()))
}

// storeAt stores v (of type t) at [base + off].
func (g *gen) storeAt(base int, off int64, t *types.Type, v val) {
	switch {
	case aggregate(t):
		size, _ := t.Layout()
		g.addImm(0, base, off)
		g.alu("mov r1, %s", rn[v.r])
		g.copyMem(0, 1, size)
	case off <= 4095:
		g.emit(g.t.Store, "%s %s, [%s, #%d]", storeOp(t), rn[v.r], rn[base], off)
	default:
		g.addImm(0, base, off)
		g.emit(g.t.Store, "%s %s, [r0]", storeOp(t), rn[v.r])
	}
}

func (g *gen) ident(x *ast.Ident) val {
	sym := x.Sym
	t := x.Info().Type
	switch sym.Kind {
	case types.SymLocal, types.SymParam:
		slot, ok := g.slots[sym]
		if !ok {
			g.unsupported(x.P, "local `"+sym.Name+"` without a slot")
			return one(g.alloc(x.P))
		}
		switch {
		case t.Kind == types.Slice:
			r := g.alloc(x.P)
			r2 := g.alloc(x.P)
			g.emit(g.t.Load, "ldr %s, [sp, #%s]", rn[r], lslot(slot))
			g.emit(g.t.Load, "ldr %s, [sp, #%s]", rn[r2], lslot(slot+4))
			return val{r, r2}
		case aggregate(t):
			r := g.alloc(x.P)
			g.alu("add %s, sp, #%s", rn[r], lslot(slot))
			return one(r)
		default:
			r := g.alloc(x.P)
			g.emit(g.t.Load, "ldr %s, [sp, #%s]", rn[r], lslot(slot))
			return one(r)
		}
	case types.SymGlobal:
		r := g.alloc(x.P)
		g.movSym(r, sym.CName)
		if scalar(t) {
			g.emit(g.t.Load, "%s %s, [%s]", loadOp(t), rn[r], rn[r])
		}
		return one(r)
	case types.SymConst:
		r := g.alloc(x.P)
		g.movSym(r, sym.CName)
		return one(r)
	case types.SymPeriph:
		r := g.alloc(x.P)
		g.movImm(r, uint32(sym.Addr.Uint64()))
		return one(r)
	case types.SymErr:
		r := g.alloc(x.P)
		g.movImm(r, uint32(sym.Const.Int64()))
		return one(r)
	}
	g.unsupported(x.P, "name `"+sym.Name+"`")
	return one(g.alloc(x.P))
}

// addr returns a register holding the address of an addressable expression
// (aggregates evaluate to their address anyway).
func (g *gen) addr(e ast.Expr) int {
	switch x := e.(type) {
	case *ast.Paren:
		return g.addr(x.X)
	case *ast.Ident:
		sym := x.Sym
		switch sym.Kind {
		case types.SymLocal, types.SymParam:
			r := g.alloc(x.P)
			g.alu("add %s, sp, #%s", rn[r], lslot(g.slots[sym]))
			return r
		case types.SymGlobal, types.SymConst:
			r := g.alloc(x.P)
			g.movSym(r, sym.CName)
			return r
		}
	case *ast.Field:
		if x.Kind == ast.FieldStruct {
			r := g.addr(x.X)
			off, _ := fieldOffset(x.X.Info().Type, x.Name)
			if off > 0 {
				g.addImm(r, r, off)
			}
			return r
		}
	case *ast.Index:
		return g.indexAddr(x)
	}
	if aggregate(e.Info().Type) {
		return g.expr(e).r
	}
	g.unsupported(e.EPos(), "taking the address of this expression")
	return g.alloc(e.EPos())
}

// elemAddr computes dst = base + idx*size.
func (g *gen) elemAddr(dst, base, idx int, size int64) {
	if size&(size-1) == 0 {
		k := log2(size)
		if k == 0 {
			g.alu("add %s, %s, %s", rn[dst], rn[base], rn[idx])
		} else {
			g.alu("add %s, %s, %s, lsl #%d", rn[dst], rn[base], rn[idx], k)
		}
		return
	}
	g.movImm(scratch, uint32(size))
	g.emit(g.t.Mul, "mla %s, %s, r12, %s", rn[dst], rn[idx], rn[base])
}

// indexAddr returns a register holding &x.X[x.I], bounds-checked as marked.
func (g *gen) indexAddr(x *ast.Index) int {
	t := x.X.Info().Type
	esize, _ := t.Elem.Layout()
	check := x.Info().Check
	switch t.Kind {
	case types.Array:
		base := g.addr(x.X)
		idx := g.exprAs(x.I, types.Usize)
		if check {
			g.cmpImm(idx.r, t.Len)
			g.trapIf("hs")
		}
		g.elemAddr(base, base, idx.r, esize)
		g.free(idx)
		return base
	case types.Slice:
		v := g.expr(x.X)
		idx := g.exprAs(x.I, types.Usize)
		if check {
			g.alu("cmp %s, %s", rn[idx.r], rn[v.r2])
			g.trapIf("hs")
		}
		g.elemAddr(v.r, v.r, idx.r, esize)
		g.free(one(v.r + 1))
		return v.r
	case types.Grow:
		p := g.expr(x.X)
		idx := g.exprAs(x.I, types.Usize)
		if check {
			g.emit(g.t.Load, "ldr r3, [%s]", rn[p.r])
			g.alu("cmp %s, r3", rn[idx.r])
			g.trapIf("hs")
		}
		g.alu("adds %s, %s, #4", rn[p.r], rn[p.r])
		g.elemAddr(p.r, p.r, idx.r, esize)
		g.free(idx)
		return p.r
	}
	g.unsupported(x.P, "indexing "+t.String())
	return g.alloc(x.P)
}

func (g *gen) slice(x *ast.SliceExpr) val {
	t := x.X.Info().Type
	esize, _ := t.Elem.Layout()
	r := g.alloc(x.P)
	r2 := g.alloc(x.P)
	switch t.Kind {
	case types.Array:
		a := g.addr(x.X)
		g.alu("mov %s, %s", rn[r], rn[a])
		g.free(one(a))
		g.movImm(r2, uint32(t.Len))
	case types.Grow:
		p := g.expr(x.X)
		g.emit(g.t.Load, "ldr %s, [%s]", rn[r2], rn[p.r])
		g.alu("adds %s, %s, #4", rn[r], rn[p.r])
		g.free(p)
	default:
		v := g.expr(x.X)
		g.alu("mov %s, %s", rn[r], rn[v.r])
		g.alu("mov %s, %s", rn[r2], rn[v.r2])
		g.free(v)
	}
	lo := -1
	if x.Lo != nil {
		lo = g.exprAs(x.Lo, types.Usize).r
	}
	hi := -1
	if x.Hi != nil {
		hi = g.exprAs(x.Hi, types.Usize).r
	}
	if x.Info().Check {
		if hi >= 0 {
			g.alu("cmp %s, %s", rn[hi], rn[r2])
			g.trapIf("hi")
		}
		if lo >= 0 {
			h := r2
			if hi >= 0 {
				h = hi
			}
			g.alu("cmp %s, %s", rn[lo], rn[h])
			g.trapIf("hi")
		}
	}
	if hi >= 0 {
		g.alu("mov %s, %s", rn[r2], rn[hi])
	}
	if lo >= 0 {
		g.alu("sub %s, %s, %s", rn[r2], rn[r2], rn[lo])
		g.elemAddr(r, r, lo, esize)
	}
	g.free(one(r2 + 1))
	return val{r, r2}
}

func (g *gen) regWait(base ast.Expr) int64 {
	if id, ok := base.(*ast.Ident); ok && id.Sym != nil && id.Sym.Kind == types.SymPeriph && id.Sym.Addr != nil {
		a := id.Sym.Addr.Uint64()
		switch {
		case a >= 0xD0000000 && a < 0xE0000000:
			return g.t.WaitSIO
		case a >= 0x40000000 && a < 0x50000000:
			return g.t.WaitAPB
		case a >= 0x50000000 && a < 0x60000000:
			return g.t.WaitAHB
		case a >= 0x20000000 && a < 0x30000000:
			return 0 // SRAM (used by tests that model a peripheral in RAM)
		}
	}
	return g.t.WaitUnknown
}

func (g *gen) field(x *ast.Field) val {
	t := x.Info().Type
	switch x.Kind {
	case ast.FieldStruct:
		r := g.addr(x)
		if aggregate(t) {
			return one(r)
		}
		g.emit(g.t.Load, "%s %s, [%s]", loadOp(t), rn[r], rn[r])
		return one(r)
	case ast.FieldReg:
		b := g.expr(x.X)
		g.emit(g.t.Load+g.regWait(x.X), "%s %s, [%s, #%d]", loadOp(x.Reg.Type), rn[b.r], rn[b.r], x.Reg.Offset)
		return b
	case ast.FieldBits:
		v := g.expr(x.X)
		g.alu("ubfx %s, %s, #%d, #%d", rn[v.r], rn[v.r], x.Bits.Bit, x.Bits.Width)
		return v
	case ast.FieldLen:
		bt := x.X.Info().Type
		switch bt.Kind {
		case types.Slice:
			v := g.expr(x.X)
			g.alu("mov %s, %s", rn[v.r], rn[v.r2])
			g.free(one(v.r + 1))
			return one(v.r)
		case types.Grow:
			p := g.expr(x.X)
			g.emit(g.t.Load, "ldr %s, [%s]", rn[p.r], rn[p.r])
			return p
		}
	}
	g.unsupported(x.P, "selector ."+x.Name)
	return one(g.alloc(x.P))
}

// ---- operators -------------------------------------------------------------

func (g *gen) unary(x *ast.Unary) val {
	t := intOf(x.Info().Type)
	v := g.expr(x.X)
	r := rn[v.r]
	switch x.Op {
	case "!":
		g.alu("eor %s, %s, #1", r, r)
	case "-":
		if t.Bits == 32 {
			g.alu("rsbs %s, %s, #0", r, r)
			if x.Info().Check {
				g.trapIf("vs")
			}
		} else {
			g.alu("rsb %s, %s, #0", r, r)
			if x.Info().Check {
				g.rangeCheck(v.r, t)
			}
		}
	case "~":
		g.alu("mvn %s, %s", r, r)
		if !t.Signed && t.Bits < 32 {
			g.alu("ubfx %s, %s, #0, #%d", r, r, t.Bits)
		}
	}
	return v
}

// rangeCheck traps unless register r holds a value of integer type t
// (t narrower than 32 bits).
func (g *gen) rangeCheck(r int, t *types.Type) {
	if t.Bits >= 32 {
		return
	}
	if t.Signed {
		g.alu("sbfx r12, %s, #0, #%d", rn[r], t.Bits)
		g.alu("cmp r12, %s", rn[r])
		g.trapIf("ne")
		return
	}
	g.alu("lsrs r12, %s, #%d", rn[r], t.Bits)
	g.trapIf("ne")
}

// normalize reduces register r to integer type t modulo 2^bits.
func (g *gen) normalize(r int, t *types.Type) {
	if t.Bits >= 32 {
		return
	}
	if t.Signed {
		g.alu("sbfx %s, %s, #0, #%d", rn[r], rn[r], t.Bits)
	} else {
		g.alu("ubfx %s, %s, #0, #%d", rn[r], rn[r], t.Bits)
	}
}

// satMax puts t's maximum into r under the given IT condition.
func (g *gen) satMax(r int, t *types.Type, cond string) {
	bits := t.Bits
	if t.Signed {
		bits--
	}
	if bits <= 16 {
		g.alu("it %s", cond)
		g.alu("movw%s %s, #%d", cond, rn[r], (1<<bits)-1)
		return
	}
	g.alu("itt %s", cond)
	g.alu("mov%s %s, #-1", cond, rn[r])
	g.alu("lsr%s %s, %s, #%d", cond, rn[r], rn[r], 32-bits)
}

func (g *gen) binary(x *ast.Binary) val {
	switch x.Op {
	case "&&", "||", "==", "!=", "<", "<=", ">", ">=":
		r := g.alloc(x.P)
		start := g.st.lin
		g.alu("movs %s, #0", rn[r])
		f := g.newLabel()
		saveTop := g.tempTop
		jump, fall := g.condJump(x, f, false)
		g.tempTop = saveTop
		g.alu("movs %s, #1", rn[r])
		g.label(f)
		// Paths: false (jump) skips the final movs.
		head := path{g.t.ALU, 1, true}
		g.st.lin = start.plus(head).plus(maxp(jump, fall.plus(path{g.t.ALU, 1, true})))
		return one(r)
	}
	t := intOf(x.Info().Type)
	a := g.exprAs(x.X, t)
	b := g.exprAs(x.Y, t)
	if x.Op == "<<" || x.Op == ">>" {
		g.shift(x.Op, a.r, b.r, t, x.Info().Check)
	} else {
		g.arith(x.Op, a.r, b.r, t, x.Info().Check, x)
	}
	g.free(b)
	return a
}

func (g *gen) shift(op string, a, b int, t *types.Type, check bool) {
	if check {
		g.cmpImm(b, int64(t.Bits))
		g.trapIf("hs")
	}
	switch {
	case op == "<<":
		g.alu("lsl %s, %s, %s", rn[a], rn[a], rn[b])
		g.normalize(a, t)
	case t.Signed:
		g.alu("asr %s, %s, %s", rn[a], rn[a], rn[b])
	default:
		g.alu("lsr %s, %s, %s", rn[a], rn[a], rn[b])
	}
}

// arith computes a = a op b for integer type t (registers normalized).
func (g *gen) arith(op string, a, b int, t *types.Type, check bool, at ast.Expr) {
	ra, rb := rn[a], rn[b]
	n := t.Bits
	switch op {
	case "&", "|", "^":
		ins := map[string]string{"&": "and", "|": "orr", "^": "eor"}[op]
		g.alu("%s %s, %s, %s", ins, ra, ra, rb)
	case "+", "-":
		ins := map[string]string{"+": "add", "-": "sub"}[op]
		switch {
		case !check:
			g.alu("%s %s, %s, %s", ins, ra, ra, rb)
		case n == 32 && t.Signed:
			g.alu("%ss %s, %s, %s", ins, ra, ra, rb)
			g.trapIf("vs")
		case n == 32:
			g.alu("%ss %s, %s, %s", ins, ra, ra, rb)
			if op == "+" {
				g.trapIf("cs")
			} else {
				g.trapIf("cc")
			}
		default:
			g.alu("%s %s, %s, %s", ins, ra, ra, rb)
			g.rangeCheck(a, t)
		}
	case "*":
		switch {
		case !check || n <= 16:
			g.emit(g.t.Mul, "mul %s, %s, %s", ra, ra, rb)
			if check {
				g.rangeCheck(a, t)
			}
		case t.Signed:
			g.emit(g.t.Mul, "smull %s, r12, %s, %s", ra, ra, rb)
			g.alu("cmp r12, %s, asr #31", ra)
			g.trapIf("ne")
			g.rangeCheck(a, t)
		default:
			g.emit(g.t.Mul, "umull %s, r12, %s, %s", ra, ra, rb)
			g.alu("cmp r12, #0")
			g.trapIf("ne")
			g.rangeCheck(a, t)
		}
	case "/", "%":
		if check {
			g.alu("cmp %s, #0", rb)
			g.trapIf("eq")
			if t.Signed && n == 32 {
				g.alu("cmn %s, #1", rb)
				g.alu("it eq")
				g.alu("cmpeq %s, #0x80000000", ra)
				g.trapIf("eq")
			}
		}
		div := "udiv"
		if t.Signed {
			div = "sdiv"
		}
		if op == "/" {
			g.emit(g.t.Div, "%s %s, %s, %s", div, ra, ra, rb)
			if check && t.Signed {
				g.rangeCheck(a, t)
			}
		} else {
			g.emit(g.t.Div, "%s r12, %s, %s", div, ra, rb)
			g.emit(g.t.Mul, "mls %s, r12, %s, %s", ra, rb, ra)
		}
	case "+%", "-%", "*%":
		switch op {
		case "+%":
			g.alu("add %s, %s, %s", ra, ra, rb)
		case "-%":
			g.alu("sub %s, %s, %s", ra, ra, rb)
		default:
			g.emit(g.t.Mul, "mul %s, %s, %s", ra, ra, rb)
		}
		g.normalize(a, t)
	case "+|", "-|":
		add := op == "+|"
		switch {
		case t.Signed && n == 32:
			if add {
				g.alu("qadd %s, %s, %s", ra, ra, rb)
			} else {
				g.alu("qsub %s, %s, %s", ra, ra, rb) // Rd = Rm - Rn, saturating
			}
		case t.Signed:
			if add {
				g.alu("add %s, %s, %s", ra, ra, rb)
			} else {
				g.alu("sub %s, %s, %s", ra, ra, rb)
			}
			g.alu("ssat %s, #%d, %s", ra, n, ra)
		case !add:
			g.alu("subs %s, %s, %s", ra, ra, rb)
			g.alu("it lo")
			g.alu("movlo %s, #0", ra)
		case n == 32:
			g.alu("adds %s, %s, %s", ra, ra, rb)
			g.alu("it cs")
			g.alu("movcs %s, #-1", ra)
		default:
			g.alu("add %s, %s, %s", ra, ra, rb)
			g.alu("lsrs r12, %s, #%d", ra, n)
			g.satMax(a, t, "ne")
		}
	case "*|":
		switch {
		case t.Signed && n <= 16:
			g.emit(g.t.Mul, "mul %s, %s, %s", ra, ra, rb)
			g.alu("ssat %s, #%d, %s", ra, n, ra)
		case t.Signed:
			g.emit(g.t.Mul, "smull %s, r12, %s, %s", ra, ra, rb)
			g.alu("cmp r12, %s, asr #31", ra)
			// Overflow: MAX if the true product is positive, else MIN,
			// i.e. ~((hi >> 31) ^ 0x80000000).
			g.alu("ittt ne")
			g.alu("asrne %s, r12, #31", ra)
			g.alu("eorne %s, %s, #0x80000000", ra, ra)
			g.alu("mvnne %s, %s", ra, ra)
			if n < 32 {
				g.alu("ssat %s, #%d, %s", ra, n, ra)
			}
		case n <= 16:
			g.emit(g.t.Mul, "mul %s, %s, %s", ra, ra, rb)
			g.alu("lsrs r12, %s, #%d", ra, n)
			g.satMax(a, t, "ne")
		default:
			g.emit(g.t.Mul, "umull %s, r12, %s, %s", ra, ra, rb)
			g.alu("cmp r12, #0")
			g.alu("it ne")
			g.alu("movne %s, #-1", ra)
			if n < 32 {
				g.alu("lsrs r12, %s, #%d", ra, n)
				g.satMax(a, t, "ne")
			}
		}
	default:
		g.unsupported(at.EPos(), "operator "+op)
	}
}

// ---- conversions -----------------------------------------------------------

func (g *gen) convert(x *ast.Call) val {
	t := intOf(x.Target)
	from := intOf(x.Args[0].Info().Type)
	v := g.exprAs(x.Args[0], from)
	r := v.r
	switch x.Kind {
	case ast.CallConvert:
		if x.Info().Check {
			switch {
			case !t.Signed && t.Bits < 32:
				g.alu("lsrs r12, %s, #%d", rn[r], t.Bits)
				g.trapIf("ne")
			case !t.Signed:
				g.alu("cmp %s, #0", rn[r])
				g.trapIf("lt")
			case !from.Signed:
				g.alu("lsrs r12, %s, #%d", rn[r], t.Bits-1)
				g.trapIf("ne")
			default:
				g.rangeCheck(r, t)
			}
		}
	case ast.CallWrap:
		g.normalize(r, t)
	case ast.CallSat:
		switch {
		case from.Signed && t.Signed:
			if t.Bits < 32 {
				g.alu("ssat %s, #%d, %s", rn[r], t.Bits, rn[r])
			}
		case from.Signed && t.Bits < 32:
			g.alu("usat %s, #%d, %s", rn[r], t.Bits, rn[r])
		case from.Signed:
			g.alu("bic %s, %s, %s, asr #31", rn[r], rn[r], rn[r])
		default:
			bits := t.Bits
			if t.Signed {
				bits--
			}
			if bits < 32 {
				g.alu("lsrs r12, %s, #%d", rn[r], bits)
				g.satMax(r, t, "ne")
			}
		}
	}
	return v
}

// ---- calls -----------------------------------------------------------------

func (g *gen) addCallee(fn *types.Fn, pos ast.Expr) {
	b, ok := g.bounds[fn]
	if !ok {
		return
	}
	switch {
	case b.Unbounded:
		if g.st.unb == nil {
			g.st.unb = &unbounded{pos: pos.EPos(), why: "calls " + fn.Name + ": " + b.Why}
		}
	case b.NeverReturns:
		g.st.lin = path{}
	default:
		g.st.lin = g.st.lin.plus(path{b.Cycles, b.Insns, true})
	}
}

// callRaw emits a call; results are left in r0 (and r1). It returns the
// hidden-result temp offset for aggregate results, or -1.
func (g *gen) callRaw(x *ast.Call) int64 {
	fn := x.Callee
	base := g.tempTop
	var ws []int
	var hidden int64 = -1
	if aggregate(fn.Ret) {
		size, align := fn.Ret.Layout()
		hidden = g.temp(size, align)
		r := g.alloc(x.P)
		g.alu("add %s, sp, #%s", rn[r], tslot(hidden))
		ws = append(ws, r)
	}
	for i, a := range x.Args {
		pt := fn.Params[i].Type
		v := g.exprAs(a, pt)
		ws = append(ws, v.r)
		if v.r2 >= 0 {
			ws = append(ws, v.r2)
		}
	}
	for i, r := range ws {
		if i < 4 {
			g.alu("mov r%d, %s", i, rn[r])
		} else {
			g.emit(g.t.Store, "str %s, [sp, #%d]", rn[r], (i-4)*4)
		}
	}
	if len(ws) > 4 {
		g.outArgs = max(g.outArgs, int64(len(ws)-4)*4)
	}
	g.emit(g.t.Call, "bl %s", fn.CName)
	g.addCallee(fn, x)
	g.tempTop = base
	return hidden
}

func (g *gen) call(x *ast.Call) val {
	switch x.Kind {
	case ast.CallConvert, ast.CallWrap, ast.CallSat:
		return g.convert(x)
	case ast.CallFn:
		hidden := g.callRaw(x)
		r := g.alloc(x.P)
		if hidden >= 0 {
			g.alu("add %s, sp, #%s", rn[r], tslot(hidden))
		} else {
			g.alu("mov %s, r0", rn[r])
		}
		return one(r)
	}
	g.unsupported(x.P, "this call in expression position")
	return one(g.alloc(x.P))
}

// fallible emits a fallible call or queue operation. Afterwards r0 holds
// the error code and r1 a scalar value; aggregate values are at the
// returned temp offset.
func (g *gen) fallible(e ast.Expr) int64 {
	x, ok := unparen(e).(*ast.Call)
	if !ok {
		g.unsupported(e.EPos(), "this fallible expression")
		return -1
	}
	switch x.Kind {
	case ast.CallFn:
		return g.callRaw(x)
	case ast.CallSend, ast.CallRecv:
		g.queueOp(x)
		return -1
	}
	g.unsupported(x.P, "this fallible call")
	return -1
}

func unparen(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.Paren)
		if !ok {
			return e
		}
		e = p.X
	}
}

func (g *gen) queueOp(x *ast.Call) {
	qt := x.Recv.Info().Type
	if aggregate(qt.Elem) {
		g.unsupported(x.P, "queues of aggregates")
		return
	}
	esize, _ := qt.Elem.Layout()
	slots := qt.Len + 1
	id := unparen(x.Recv).(*ast.Ident)
	done := g.newLabel()
	// The early-exit (full/empty) path is a prefix of the straight-line
	// success path, so charging every instruction is a sound bound.
	if x.Kind == ast.CallSend {
		v := g.exprAs(x.Args[0], qt.Elem)
		g.movSym(2, id.Sym.CName)
		g.emit(g.t.Load, "ldr r0, [r2]")
		g.alu("adds r1, r0, #1")
		g.cmpImm(1, slots)
		g.alu("it eq")
		g.alu("moveq r1, #0")
		g.emit(g.t.Load, "ldr r3, [r2, #4]")
		g.alu("cmp r1, r3")
		g.alu("itt eq")
		g.alu("moveq r0, #%d", g.errCode("Full"))
		g.emit(g.t.NotTkn, "beq %s", done)
		g.alu("adds r3, r2, #8")
		g.elemAddr(3, 3, 0, esize)
		g.emit(g.t.Store, "%s %s, [r3]", storeOp(qt.Elem), rn[v.r])
		g.emit(g.t.Barrier, "dmb")
		g.emit(g.t.Store, "str r1, [r2]")
		g.alu("movs r0, #0")
		g.label(done)
		g.free(v)
	} else {
		g.movSym(2, id.Sym.CName)
		g.emit(g.t.Load, "ldr r0, [r2, #4]")
		g.emit(g.t.Load, "ldr r3, [r2]")
		g.alu("cmp r0, r3")
		g.alu("itt eq")
		g.alu("moveq r0, #%d", g.errCode("Empty"))
		g.emit(g.t.NotTkn, "beq %s", done)
		g.emit(g.t.Barrier, "dmb")
		g.alu("adds r3, r2, #8")
		g.elemAddr(3, 3, 0, esize)
		g.emit(g.t.Load, "%s r1, [r3]", loadOp(qt.Elem))
		g.emit(g.t.Barrier, "dmb")
		g.alu("adds r0, #1")
		g.cmpImm(0, slots)
		g.alu("it eq")
		g.alu("moveq r0, #0")
		g.emit(g.t.Store, "str r0, [r2, #4]")
		g.alu("movs r0, #0")
		g.label(done)
	}
}

// errBranch branches to an out-of-line stub when r0 != 0, charging the
// stub's exits as taken-branch paths.
func (g *gen) errBranch(stubFlow flow, label string) {
	g.alu("cmp r0, #0")
	g.mergeExit(g.st.lin, stubFlow)
	g.emit(g.t.NotTkn, "bne.w %s", label)
}

func (g *gen) resultVal(e ast.Expr, hidden int64) val {
	ut := e.Info().Type
	if ut.Kind != types.Union || ut.Elem.Kind == types.Void {
		return one(g.alloc(e.EPos()))
	}
	r := g.alloc(e.EPos())
	if hidden >= 0 {
		g.alu("add %s, sp, #%s", rn[r], tslot(hidden))
	} else {
		g.alu("mov %s, r1", rn[r])
	}
	return one(r)
}

func (g *gen) try(x *ast.Try) val {
	hidden := g.fallible(x.X)
	label, fl := g.outOfLine(func() {
		g.release()
		g.fb.WriteString("@EPI@\n")
		f := g.here()
		g.st.ret = f.fall
		g.st.lin = path{}
	})
	g.errBranch(fl, label)
	return g.resultVal(x.X, hidden)
}

func (g *gen) catchExpr(x *ast.Catch) val {
	hidden := g.fallible(x.X)
	ut := x.X.Info().Type
	switch {
	case x.Jump != nil:
		switch j := x.Jump.(type) {
		case *ast.BranchStmt:
			lb := g.loops[len(g.loops)-1]
			target := lb.brk
			if j.Tok == "continue" {
				target = lb.cont
			}
			g.alu("cmp r0, #0")
			taken := g.st.lin.plus(path{g.t.Taken, 1, true})
			if j.Tok == "break" {
				g.st.brk = maxp(g.st.brk, taken)
			} else {
				g.st.cont = maxp(g.st.cont, taken)
			}
			g.emit(g.t.NotTkn, "bne.w %s", target)
		case *ast.ReturnStmt:
			label, fl := g.outOfLine(func() {
				g.ret(j)
				f := g.here()
				g.st.ret = f.fall
				g.st.lin = path{}
			})
			g.errBranch(fl, label)
		}
		return g.resultVal(x.X, hidden)
	case x.Default != nil:
		v := g.resultVal(x.X, hidden)
		if aggregate(ut.Elem) {
			back := g.newLabel()
			size, _ := ut.Elem.Layout()
			label, fl := g.outOfLine(func() {
				d := g.exprAs(x.Default, ut.Elem)
				g.alu("mov r0, %s", rn[v.r])
				g.alu("mov r1, %s", rn[d.r])
				g.copyMem(0, 1, size)
				g.free(d)
				g.emit(g.t.Taken, "b.w %s", back)
			})
			g.alu("cmp r0, #0")
			errPath := g.st.lin.plus(path{g.t.Taken, 1, true}).plus(fl.fall)
			g.emit(g.t.NotTkn, "bne.w %s", label)
			g.label(back)
			g.st.lin = maxp(g.st.lin, errPath)
			return v
		}
		if k := x.Default.Info().Const; k != nil && k.Sign() >= 0 && k.Cmp(big.NewInt(255)) <= 0 {
			g.alu("cmp r0, #0")
			g.alu("it ne")
			g.alu("movne %s, #%d", rn[v.r], k.Int64())
			return v
		}
		back := g.newLabel()
		label, fl := g.outOfLine(func() {
			d := g.exprAs(x.Default, ut.Elem)
			g.alu("mov %s, %s", rn[v.r], rn[d.r])
			g.free(d)
			g.emit(g.t.Taken, "b.w %s", back)
		})
		g.alu("cmp r0, #0")
		errPath := g.st.lin.plus(path{g.t.Taken, 1, true}).plus(fl.fall)
		g.emit(g.t.NotTkn, "bne.w %s", label)
		g.label(back)
		g.st.lin = maxp(g.st.lin, errPath)
		return v
	case x.Body != nil:
		back := g.newLabel()
		var bodyFlow flow
		label, _ := g.outOfLine(func() {
			if x.ErrSym != nil {
				slot := g.local(4, 4)
				g.slots[x.ErrSym] = slot
				g.emit(g.t.Store, "str r0, [sp, #%s]", lslot(slot))
			}
			bodyFlow = seq(g.here(), g.block(x.Body))
			saved := g.st
			g.st = stmtState{lin: zero}
			g.emit(g.t.Taken, "b.w %s", back)
			jmp := g.st.lin
			g.st = saved
			bodyFlow.fall = bodyFlow.fall.plus(jmp)
		})
		g.alu("cmp r0, #0")
		g.mergeExit(g.st.lin, flow{brk: bodyFlow.brk, cont: bodyFlow.cont, ret: bodyFlow.ret, unb: bodyFlow.unb})
		errPath := g.st.lin.plus(path{g.t.Taken, 1, true}).plus(bodyFlow.fall)
		g.emit(g.t.NotTkn, "bne.w %s", label)
		g.label(back)
		g.st.lin = maxp(g.st.lin, errPath)
		return one(g.alloc(x.P))
	}
	return g.resultVal(x.X, hidden)
}

// ---- statements built on expressions ---------------------------------------

func (g *gen) exprStmt(e ast.Expr) {
	switch x := e.(type) {
	case *ast.Call:
		switch x.Kind {
		case ast.CallFn:
			g.callRaw(x)
			return
		case ast.CallPush:
			gt := x.Recv.Info().Type
			esize, _ := gt.Elem.Layout()
			p := g.expr(x.Recv)
			v := g.exprAs(x.Args[0], gt.Elem)
			g.emit(g.t.Load, "ldr r3, [%s]", rn[p.r])
			if x.Info().Check {
				g.cmpImm(3, gt.Len)
				g.trapIf("hs")
			}
			g.alu("adds r2, %s, #4", rn[p.r])
			g.elemAddr(2, 2, 3, esize)
			if aggregate(gt.Elem) {
				g.alu("mov r0, r2")
				g.alu("mov r1, %s", rn[v.r])
				g.copyMem(0, 1, esize)
				g.emit(g.t.Load, "ldr r3, [%s]", rn[p.r])
			} else {
				g.emit(g.t.Store, "%s %s, [r2]", storeOp(gt.Elem), rn[v.r])
			}
			g.alu("adds r3, #1")
			g.emit(g.t.Store, "str r3, [%s]", rn[p.r])
			g.free(p)
			return
		case ast.CallClear:
			p := g.expr(x.Recv)
			g.alu("movs r3, #0")
			g.emit(g.t.Store, "str r3, [%s]", rn[p.r])
			g.free(p)
			return
		}
	}
	v := g.expr(e)
	g.free(v)
}

func (g *gen) assign(s *ast.AssignStmt) {
	lt := s.LHS.Info().Type
	lhs := unparen(s.LHS)
	// Bitfield: read-modify-write with BFI.
	if f, ok := lhs.(*ast.Field); ok && f.Kind == ast.FieldBits {
		inner := unparen(f.X).(*ast.Field)
		b := g.expr(inner.X)
		wait := g.regWait(inner.X)
		var v val
		if s.Op == "=" {
			v = g.exprAs(s.RHS, lt)
		} else {
			cur := g.alloc(s.P)
			g.emit(g.t.Load+wait, "%s %s, [%s, #%d]", loadOp(inner.Reg.Type), rn[cur], rn[b.r], inner.Reg.Offset)
			g.alu("ubfx %s, %s, #%d, #%d", rn[cur], rn[cur], f.Bits.Bit, f.Bits.Width)
			v = g.compound(s, one(cur), intOf(lt))
		}
		g.emit(g.t.Load+wait, "%s r3, [%s, #%d]", loadOp(inner.Reg.Type), rn[b.r], inner.Reg.Offset)
		g.alu("bfi r3, %s, #%d, #%d", rn[v.r], f.Bits.Bit, f.Bits.Width)
		g.emit(g.t.Store+wait, "%s r3, [%s, #%d]", storeOp(inner.Reg.Type), rn[b.r], inner.Reg.Offset)
		g.free(b)
		return
	}
	// Register: plain store.
	if f, ok := lhs.(*ast.Field); ok && f.Kind == ast.FieldReg {
		b := g.expr(f.X)
		wait := g.regWait(f.X)
		var v val
		if s.Op == "=" {
			v = g.exprAs(s.RHS, lt)
		} else {
			cur := g.alloc(s.P)
			g.emit(g.t.Load+wait, "%s %s, [%s, #%d]", loadOp(f.Reg.Type), rn[cur], rn[b.r], f.Reg.Offset)
			v = g.compound(s, one(cur), intOf(lt))
		}
		g.emit(g.t.Store+wait, "%s %s, [%s, #%d]", storeOp(f.Reg.Type), rn[v.r], rn[b.r], f.Reg.Offset)
		g.free(b)
		return
	}
	// Scalar local: direct slot access.
	if id, ok := lhs.(*ast.Ident); ok && (id.Sym.Kind == types.SymLocal || id.Sym.Kind == types.SymParam) && scalar(lt) {
		slot := g.slots[id.Sym]
		var v val
		if s.Op == "=" {
			v = g.exprAs(s.RHS, lt)
		} else {
			cur := g.alloc(s.P)
			g.emit(g.t.Load, "ldr %s, [sp, #%s]", rn[cur], lslot(slot))
			v = g.compound(s, one(cur), intOf(lt))
		}
		g.emit(g.t.Store, "str %s, [sp, #%s]", rn[v.r], lslot(slot))
		g.free(v)
		return
	}
	a := g.addr(lhs)
	if aggregate(lt) {
		v := g.exprAs(s.RHS, lt)
		size, _ := lt.Layout()
		g.alu("mov r0, %s", rn[a])
		g.alu("mov r1, %s", rn[v.r])
		g.copyMem(0, 1, size)
		g.free(one(a))
		return
	}
	var v val
	if s.Op == "=" {
		v = g.exprAs(s.RHS, lt)
	} else {
		cur := g.alloc(s.P)
		g.emit(g.t.Load, "%s %s, [%s]", loadOp(lt), rn[cur], rn[a])
		v = g.compound(s, one(cur), intOf(lt))
	}
	g.emit(g.t.Store, "%s %s, [%s]", storeOp(lt), rn[v.r], rn[a])
	g.free(one(a))
}

// compound evaluates `cur op= rhs`, leaving the result in cur.
func (g *gen) compound(s *ast.AssignStmt, cur val, t *types.Type) val {
	op := strings.TrimSuffix(s.Op, "=")
	rhs := g.exprAs(s.RHS, t)
	if op == "<<" || op == ">>" {
		g.shift(op, cur.r, rhs.r, t, s.Check)
	} else {
		g.arith(op, cur.r, rhs.r, t, s.Check, s.LHS)
	}
	g.free(rhs)
	return cur
}
