// Package check resolves names, checks types, folds constants, lays out
// data-stack frames and marks the runtime checks the emitter must insert.
package check

import (
	"fmt"
	"math/big"
	"regexp"
	"strconv"

	"github.com/amken3d/quoin/internal/ast"
	"github.com/amken3d/quoin/internal/diag"
	"github.com/amken3d/quoin/internal/types"
)

// Program is a checked whole program (all files together).
type Program struct {
	Files   []*ast.File
	Decls   []ast.Decl
	Fns     []*types.Fn // every fn, isr and extern, in declaration order
	Entries []*types.Fn // main first, then isrs in declaration order
	ErrTags []*types.Symbol
	Globals []*types.Symbol // global vars, in declaration order
	Consts  []*ast.ConstDecl
	Named   []*types.Type // structs, enums, regs in declaration order
	Periphs []*ast.PeriphDecl
}

// BuiltinErrs are error tags every program has; queues return them.
var BuiltinErrs = []string{"Full", "Empty"}

type scope struct {
	parent *scope
	syms   map[string]*types.Symbol
}

func (s *scope) lookup(name string) *types.Symbol {
	for ; s != nil; s = s.parent {
		if sym, ok := s.syms[name]; ok {
			return sym
		}
	}
	return nil
}

type constState uint8

const (
	unresolved constState = iota
	resolving
	resolved
)

type checker struct {
	d       *diag.List
	prog    *Program
	globals *scope
	cur     *scope

	constDecl  map[*types.Symbol]*ast.ConstDecl
	constState map[*types.Symbol]constState
	typeDecl   map[*types.Symbol]ast.Decl
	typeState  map[*types.Symbol]constState

	fn           *types.Fn
	fnScope      *scope
	loop         int
	shortCirc    int // >0 inside the right side of && or ||
	blockCatchOK bool
	dataOff      int64
	dataHigh     int64
	frame        int64
}

// Check type-checks all files as one program.
func Check(files []*ast.File, d *diag.List) *Program {
	c := &checker{
		d:          d,
		prog:       &Program{Files: files},
		globals:    &scope{syms: map[string]*types.Symbol{}},
		constDecl:  map[*types.Symbol]*ast.ConstDecl{},
		constState: map[*types.Symbol]constState{},
		typeDecl:   map[*types.Symbol]ast.Decl{},
		typeState:  map[*types.Symbol]constState{},
	}
	c.cur = c.globals
	for i, name := range BuiltinErrs {
		sym := &types.Symbol{Kind: types.SymErr, Name: name, CName: "QE_" + name, Type: types.ErrorValT, Const: big.NewInt(int64(i + 1))}
		c.globals.syms[name] = sym
		c.prog.ErrTags = append(c.prog.ErrTags, sym)
	}
	for _, f := range files {
		c.prog.Decls = append(c.prog.Decls, f.Decls...)
	}
	c.collect()
	c.resolveTypes()
	c.resolveConsts()
	c.resolveSignatures()
	c.resolveGlobals()
	for _, decl := range c.prog.Decls {
		if fd, ok := decl.(*ast.FnDecl); ok && fd.Body != nil && fd.Fn != nil {
			c.body(fd)
		}
	}
	c.entries()
	return c.prog
}

// ---- pass 1: declare every top-level name ----------------------------------

func (c *checker) declareGlobal(sym *types.Symbol) bool {
	if prev, ok := c.globals.syms[sym.Name]; ok {
		where := "a builtin"
		if prev.Pos.Line > 0 {
			where = prev.Pos.String()
		}
		c.d.ErrorFix(sym.Pos, "E106", fmt.Sprintf("`%s` already declared at %s", sym.Name, where), "rename one of them")
		return false
	}
	if builtinType(sym.Name) != nil {
		c.d.ErrorFix(sym.Pos, "E106", fmt.Sprintf("`%s` is a builtin type name", sym.Name), "rename it")
		return false
	}
	c.globals.syms[sym.Name] = sym
	return true
}

func (c *checker) collect() {
	for _, decl := range c.prog.Decls {
		switch d := decl.(type) {
		case *ast.ConstDecl:
			sym := &types.Symbol{Kind: types.SymConst, Name: d.Name, CName: "qn_" + d.Name, Pos: d.P, Decl: d}
			d.Sym = sym
			if c.declareGlobal(sym) {
				c.constDecl[sym] = d
				c.prog.Consts = append(c.prog.Consts, d)
			}
		case *ast.VarDecl:
			sym := &types.Symbol{Kind: types.SymGlobal, Name: d.Name, CName: "qn_" + d.Name, Pos: d.P, Decl: d}
			d.Sym = sym
			if c.declareGlobal(sym) {
				c.prog.Globals = append(c.prog.Globals, sym)
			}
		case *ast.ErrorDecl:
			for _, id := range d.Names {
				sym := &types.Symbol{Kind: types.SymErr, Name: id.Name, CName: "QE_" + id.Name, Type: types.ErrorValT,
					Pos: id.P, Const: big.NewInt(int64(len(c.prog.ErrTags) + 1))}
				if c.declareGlobal(sym) {
					c.prog.ErrTags = append(c.prog.ErrTags, sym)
				}
			}
		case *ast.EnumDecl:
			t := &types.Type{Kind: types.Enum, Name: d.Name, Pos: d.P}
			d.T = t
			c.declareType(d.Name, d.P, t, d)
		case *ast.StructDecl:
			t := &types.Type{Kind: types.Struct, Name: d.Name, Pos: d.P}
			d.T = t
			c.declareType(d.Name, d.P, t, d)
		case *ast.RegsDecl:
			t := &types.Type{Kind: types.Regs, Name: d.Name, Pos: d.P}
			d.T = t
			c.declareType(d.Name, d.P, t, d)
		case *ast.PeriphDecl:
			sym := &types.Symbol{Kind: types.SymPeriph, Name: d.Name, CName: "qn_" + d.Name, Pos: d.P, Decl: d}
			d.Sym = sym
			if c.declareGlobal(sym) {
				c.prog.Periphs = append(c.prog.Periphs, d)
			}
		case *ast.FnDecl:
			fn := &types.Fn{Name: d.Name, CName: "qn_" + d.Name, ISR: d.ISR, Extern: d.Extern, Pos: d.P, Decl: d,
				StackDecl: -1, Globals: map[*types.Symbol]types.GlobalUse{}, QueueUses: map[*types.Symbol]types.QueueUse{}}
			switch {
			case d.Name == "main" && !d.Extern && !d.ISR:
				fn.Main = true
				fn.CName = "main"
			case d.ISR || d.Extern:
				fn.CName = d.Name
			}
			if !d.Extern && d.Body == nil {
				c.d.ErrorFix(d.P, "E003", fmt.Sprintf("fn %s has no body", d.Name), "indent a body under the signature, or declare it `extern fn`")
			}
			if d.Extern && d.Body != nil {
				c.d.ErrorFix(d.P, "E003", fmt.Sprintf("extern fn %s has a body", d.Name), "drop `extern` or the body")
			}
			sym := &types.Symbol{Kind: types.SymFn, Name: d.Name, CName: fn.CName, Fn: fn, Pos: d.P, Decl: d}
			if c.declareGlobal(sym) {
				d.Fn = fn
				c.prog.Fns = append(c.prog.Fns, fn)
			}
		}
	}
}

func (c *checker) declareType(name string, pos diag.Pos, t *types.Type, d ast.Decl) {
	sym := &types.Symbol{Kind: types.SymType, Name: name, CName: "qn_" + name, Type: t, Pos: pos, Decl: d}
	if c.declareGlobal(sym) {
		c.typeDecl[sym] = d
		c.prog.Named = append(c.prog.Named, t)
	}
}

// ---- types -----------------------------------------------------------------

var intName = regexp.MustCompile(`^([ui])([1-9][0-9]?)$`)

func builtinType(name string) *types.Type {
	switch name {
	case "bool":
		return types.BoolT
	case "usize":
		return types.Usize
	}
	if m := intName.FindStringSubmatch(name); m != nil {
		n, _ := strconv.Atoi(m[2])
		if n >= 1 && n <= 64 {
			return types.IntT(n, m[1] == "i")
		}
	}
	return nil
}

// typ resolves a type expression. It returns InvalidT after reporting.
func (c *checker) typ(te ast.TypeExpr) *types.Type {
	switch t := te.(type) {
	case *ast.NamedType:
		if bt := builtinType(t.Name); bt != nil {
			return bt
		}
		sym := c.cur.lookup(t.Name)
		if sym == nil {
			c.d.Errorf(t.P, "E101", "undefined type `%s`", t.Name)
			return types.InvalidT
		}
		if sym.Kind != types.SymType {
			c.d.Errorf(t.P, "E124", "`%s` is not a type", t.Name)
			return types.InvalidT
		}
		c.completeType(sym)
		return sym.Type
	case *ast.ArrayType:
		n, ok := c.constLen(t.Len)
		elem := c.typ(t.Elem)
		if !ok || elem.Kind == types.Invalid {
			return types.InvalidT
		}
		if !c.valueElem(elem, t.Elem.TPos(), "an array") {
			return types.InvalidT
		}
		return &types.Type{Kind: types.Array, Len: n, Elem: elem}
	case *ast.SliceType:
		elem := c.typ(t.Elem)
		if elem.Kind == types.Invalid || !c.valueElem(elem, t.Elem.TPos(), "a slice") {
			return types.InvalidT
		}
		return &types.Type{Kind: types.Slice, Elem: elem}
	case *ast.GrowType:
		n, ok := c.constLen(t.Cap)
		elem := c.typ(t.Elem)
		if !ok || elem.Kind == types.Invalid || !c.valueElem(elem, t.Elem.TPos(), "a grow array") {
			return types.InvalidT
		}
		return &types.Type{Kind: types.Grow, Len: n, Elem: elem}
	case *ast.QueueType:
		n, ok := c.constLen(t.Cap)
		elem := c.typ(t.Elem)
		if !ok || elem.Kind == types.Invalid || !c.valueElem(elem, t.Elem.TPos(), "a queue") {
			return types.InvalidT
		}
		return &types.Type{Kind: types.Queue, Len: n, Elem: elem}
	}
	return types.InvalidT
}

func (c *checker) valueElem(elem *types.Type, pos diag.Pos, in string) bool {
	if elem.IsValue() {
		return true
	}
	switch elem.Kind {
	case types.Slice:
		c.d.ErrorFix(pos, "E118", "slices cannot be stored in "+in, "store indices or copy the elements")
	case types.Grow, types.Queue:
		c.d.ErrorFix(pos, "E122", elem.String()+" cannot be an element of "+in, "declare it as its own variable")
	default:
		c.d.Errorf(pos, "E124", "%s cannot be an element of %s", elem, in)
	}
	return false
}

// constLen evaluates an array length or capacity.
func (c *checker) constLen(e ast.Expr) (int64, bool) {
	v, ok := c.constInt(e, nil)
	if !ok {
		return 0, false
	}
	if v.Sign() < 0 || !v.IsInt64() || v.Int64() > 1<<24 {
		c.d.Errorf(e.EPos(), "E103", "length %s is out of range", v)
		return 0, false
	}
	if v.Sign() == 0 {
		c.d.Errorf(e.EPos(), "E103", "length must be at least 1")
		return 0, false
	}
	return v.Int64(), true
}

// constInt checks e and requires a compile-time integer value.
func (c *checker) constInt(e ast.Expr, want *types.Type) (*big.Int, bool) {
	t := c.expr(e, want)
	if t.Kind == types.Invalid {
		return nil, false
	}
	if !t.IsIntLike() || e.Info().Const == nil {
		c.d.ErrorFix(e.EPos(), "E114", "value must be a compile-time integer", "use a literal or a `const`")
		return nil, false
	}
	return e.Info().Const, true
}

func (c *checker) resolveTypes() {
	for _, decl := range c.prog.Decls {
		var name string
		switch d := decl.(type) {
		case *ast.EnumDecl:
			name = d.Name
		case *ast.StructDecl:
			name = d.Name
		case *ast.RegsDecl:
			name = d.Name
		default:
			continue
		}
		if sym := c.globals.syms[name]; sym != nil && sym.Kind == types.SymType && c.typeDecl[sym] == decl {
			c.completeType(sym)
		}
	}
}

// completeType fills in a named type's members, on demand so declaration
// order does not matter.
func (c *checker) completeType(sym *types.Symbol) {
	switch c.typeState[sym] {
	case resolved:
		return
	case resolving:
		c.d.ErrorFix(sym.Pos, "E124", fmt.Sprintf("type `%s` contains itself", sym.Name), "break the cycle")
		return
	}
	decl, ok := c.typeDecl[sym]
	if !ok {
		return
	}
	c.typeState[sym] = resolving
	defer func() { c.typeState[sym] = resolved }()
	t := sym.Type
	switch d := decl.(type) {
	case *ast.StructDecl:
		seen := map[string]bool{}
		for _, f := range d.Fields {
			ft := c.typ(f.Type)
			if seen[f.Name] {
				c.d.Errorf(f.P, "E106", "field `%s` declared twice", f.Name)
				continue
			}
			seen[f.Name] = true
			if ft.Kind != types.Invalid && !ft.IsValue() {
				c.valueElem(ft, f.P, "a struct")
				ft = types.InvalidT
			}
			t.Fields = append(t.Fields, &types.Field{Name: f.Name, Type: ft, Pos: f.P})
		}
	case *ast.EnumDecl:
		base := c.typ(d.Base)
		if base.Kind != types.Int {
			if base.Kind != types.Invalid {
				c.d.ErrorFix(d.Base.TPos(), "E124", "enum base must be an integer type", "write e.g. `enum "+d.Name+": u8`")
			}
			base = types.U8
		}
		t.Base = base
		next := big.NewInt(0)
		seen := map[string]bool{}
		for _, m := range d.Members {
			v := new(big.Int).Set(next)
			if m.Value != nil {
				if cv, ok := c.constInt(m.Value, base); ok {
					v = cv
				}
			}
			if !base.Fits(v) {
				c.d.Errorf(m.P, "E103", "%s.%s = %s does not fit %s", d.Name, m.Name, v, base)
			}
			if seen[m.Name] {
				c.d.Errorf(m.P, "E106", "enum member `%s` declared twice", m.Name)
			}
			seen[m.Name] = true
			t.Members = append(t.Members, &types.EnumMember{Name: m.Name, Value: v})
			next = new(big.Int).Add(v, big.NewInt(1))
		}
	case *ast.RegsDecl:
		c.regs(d, t)
	}
}

func (c *checker) regs(d *ast.RegsDecl, t *types.Type) {
	seen := map[string]bool{}
	var end int64
	for _, r := range d.Regs {
		rt := c.typ(r.Type)
		off, ok := c.constInt(r.At, nil)
		if !ok || rt.Kind == types.Invalid {
			continue
		}
		if rt.Kind != types.Int || rt.Signed || (rt.Bits != 8 && rt.Bits != 16 && rt.Bits != 32) {
			c.d.ErrorFix(r.Type.TPos(), "E120", fmt.Sprintf("register %s has type %s", r.Name, rt), "registers are u8, u16 or u32")
			continue
		}
		size := int64(rt.Bits / 8)
		if !off.IsInt64() || off.Int64()%size != 0 {
			c.d.ErrorFix(r.At.EPos(), "E120", fmt.Sprintf("register %s at offset %s is not %d-byte aligned", r.Name, off, size), "")
			continue
		}
		if off.Int64() < end {
			c.d.ErrorFix(r.At.EPos(), "E120", fmt.Sprintf("register %s at offset %s overlaps the previous register", r.Name, off),
				"list registers in increasing offset order without overlap")
			continue
		}
		if seen[r.Name] {
			c.d.Errorf(r.P, "E106", "register `%s` declared twice", r.Name)
			continue
		}
		seen[r.Name] = true
		end = off.Int64() + size
		reg := &types.Register{Name: r.Name, Offset: off.Int64(), Type: rt, Pos: r.P}
		var used uint64
		fseen := map[string]bool{}
		for _, b := range r.Bits {
			ft := c.typ(b.Type)
			bit, ok := c.constInt(b.At, nil)
			if !ok || ft.Kind == types.Invalid {
				continue
			}
			width := 0
			switch ft.Kind {
			case types.Bool:
				width = 1
			case types.Int:
				if ft.Signed {
					c.d.ErrorFix(b.Type.TPos(), "E120", "bitfields are unsigned", "use u"+strconv.Itoa(ft.Bits))
					continue
				}
				width = ft.Bits
			case types.Enum:
				width = ft.Base.Bits
				if ft.Base.Signed {
					c.d.ErrorFix(b.Type.TPos(), "E120", "bitfield enum base must be unsigned", "")
					continue
				}
			default:
				c.d.ErrorFix(b.Type.TPos(), "E120", fmt.Sprintf("bitfield %s has type %s", b.Name, ft), "bitfields are bool, uN or an enum")
				continue
			}
			if !bit.IsInt64() || bit.Int64() < 0 || bit.Int64()+int64(width) > int64(rt.Bits) {
				c.d.ErrorFix(b.At.EPos(), "E120", fmt.Sprintf("bitfield %s.%s (bits %s..%s) exceeds %s", r.Name, b.Name, bit, new(big.Int).Add(bit, big.NewInt(int64(width-1))), rt),
					"")
				continue
			}
			mask := ((uint64(1) << width) - 1) << bit.Uint64()
			if used&mask != 0 {
				c.d.Errorf(b.At.EPos(), "E120", "bitfield %s.%s overlaps another field", r.Name, b.Name)
				continue
			}
			if fseen[b.Name] {
				c.d.Errorf(b.P, "E106", "bitfield `%s` declared twice", b.Name)
				continue
			}
			fseen[b.Name] = true
			used |= mask
			reg.Fields = append(reg.Fields, &types.BitField{Name: b.Name, Bit: int(bit.Int64()), Width: width, Type: ft, Pos: b.P})
		}
		t.Regs = append(t.Regs, reg)
	}
}

// ---- constants -------------------------------------------------------------

func (c *checker) resolveConsts() {
	for _, d := range c.prog.Consts {
		c.resolveConst(d.Sym)
	}
}

func (c *checker) resolveConst(sym *types.Symbol) {
	switch c.constState[sym] {
	case resolved:
		return
	case resolving:
		c.d.ErrorFix(sym.Pos, "E114", fmt.Sprintf("constant `%s` depends on itself", sym.Name), "break the cycle")
		sym.Type = types.InvalidT
		return
	}
	d := c.constDecl[sym]
	c.constState[sym] = resolving
	defer func() { c.constState[sym] = resolved }()
	saved := c.cur
	c.cur = c.globals
	defer func() { c.cur = saved }()
	var want *types.Type
	if d.Type != nil {
		want = c.typ(d.Type)
	}
	t := c.expr(d.Value, want)
	if want != nil && want.Kind != types.Invalid {
		c.coerce(d.Value, want, "constant "+d.Name)
		t = want
	}
	if t.Kind == types.Invalid {
		sym.Type = types.InvalidT
		return
	}
	if !t.IsIntLike() && !t.IsValue() {
		c.d.Errorf(d.P, "E104", "constant of type %s", t)
		sym.Type = types.InvalidT
		return
	}
	if !isConstExpr(d.Value) {
		c.d.ErrorFix(d.Value.EPos(), "E114", "constant initializer is not a compile-time value", "use literals and other constants")
		sym.Type = types.InvalidT
		return
	}
	sym.Type = t
	sym.Const = d.Value.Info().Const
}

// isConstExpr reports whether e is built only from compile-time values
// (integers, bools, enum members, array and struct literals of those).
func isConstExpr(e ast.Expr) bool {
	if e.Info().Const != nil {
		return true
	}
	switch x := e.(type) {
	case *ast.BoolLit:
		return true
	case *ast.EnumLit:
		return x.Member != nil
	case *ast.ArrayLit:
		for _, el := range x.Elems {
			if !isConstExpr(el) {
				return false
			}
		}
		return true
	case *ast.StructLit:
		for _, f := range x.Fields {
			if !isConstExpr(f.Value) {
				return false
			}
		}
		return true
	case *ast.Paren:
		return isConstExpr(x.X)
	case *ast.Ident:
		return x.Sym != nil && x.Sym.Kind == types.SymConst
	case *ast.Field:
		return x.Kind == ast.FieldEnum
	}
	return false
}

// ---- signatures ------------------------------------------------------------

func (c *checker) resolveSignatures() {
	for _, decl := range c.prog.Decls {
		d, ok := decl.(*ast.FnDecl)
		if !ok || d.Fn == nil {
			continue
		}
		fn := d.Fn
		seen := map[string]bool{}
		for _, p := range d.Params {
			pt := c.typ(p.Type)
			if seen[p.Name] {
				c.d.Errorf(p.P, "E106", "parameter `%s` declared twice", p.Name)
			}
			seen[p.Name] = true
			switch {
			case pt.Kind == types.Invalid:
			case pt.Kind == types.Queue:
				c.d.ErrorFix(p.P, "E122", "queues cannot be parameters", "use the global queue directly")
				pt = types.InvalidT
			case pt.Kind == types.Grow && d.Extern:
				c.d.ErrorFix(p.P, "E122", "extern fns cannot take grow arrays", "pass a slice")
				pt = types.InvalidT
			}
			sym := &types.Symbol{Kind: types.SymParam, Name: p.Name, CName: CName(p.Name), Type: pt, Pos: p.P,
				Immutable: pt.Kind == types.Slice || pt.Kind == types.Regs || pt.Kind == types.Grow}
			fn.Params = append(fn.Params, sym)
		}
		fn.Ret = types.VoidT
		if d.Ret != nil {
			rt := c.typ(d.Ret)
			switch {
			case rt.Kind == types.Invalid:
			case rt.Kind == types.Slice:
				c.d.ErrorFix(d.Ret.TPos(), "E118", "functions cannot return slices", "take a slice or grow array parameter and fill it")
				rt = types.InvalidT
			case !rt.IsValue():
				c.d.ErrorFix(d.Ret.TPos(), "E122", fmt.Sprintf("functions cannot return %s", rt), "take a grow array parameter and push into it")
				rt = types.InvalidT
			}
			fn.Ret = rt
		}
		if d.Bang {
			fn.Errs = []string{}
			eseen := map[string]bool{}
			for _, id := range d.Errs {
				sym := c.globals.syms[id.Name]
				if sym == nil || sym.Kind != types.SymErr {
					c.d.ErrorFix(id.P, "E101", fmt.Sprintf("undefined error `%s`", id.Name), "declare it: `error "+id.Name+"`")
					continue
				}
				if !eseen[id.Name] {
					eseen[id.Name] = true
					fn.Errs = append(fn.Errs, id.Name)
				}
			}
			if d.Extern {
				c.d.ErrorFix(d.P, "E104", "extern fns cannot declare error sets", "return a status integer and wrap it in a Quoin fn")
				fn.Errs = nil
			}
		}
		if d.Stack != nil {
			if !d.Extern {
				c.d.ErrorFix(d.Stack.EPos(), "E003", "`stack` is only for extern fns", "drop it; Quoin computes stack use for its own functions")
			} else if v, ok := c.constInt(d.Stack, nil); ok {
				fn.StackDecl = v.Int64()
			}
		}
		if len(d.Where) > 0 && d.Extern {
			c.d.ErrorFix(d.Where[0].EPos(), "E003", "extern fns cannot have where clauses", "wrap the extern in a Quoin fn with the contract")
		}
	}
}

// ---- globals ---------------------------------------------------------------

func (c *checker) resolveGlobals() {
	for _, decl := range c.prog.Decls {
		switch d := decl.(type) {
		case *ast.VarDecl:
			if d.Sym == nil {
				continue
			}
			t := c.typ(d.Type)
			d.Sym.Type = t
			switch {
			case t.Kind == types.Invalid:
			case t.Kind == types.Slice:
				c.d.ErrorFix(d.P, "E118", "global slices are not allowed", "use a global array and index it")
				d.Sym.Type = types.InvalidT
			case t.Kind == types.Regs:
				c.d.ErrorFix(d.P, "E104", "register blocks are declared with `periph`", "write `periph "+d.Name+": "+t.Name+" @ <address>`")
				d.Sym.Type = types.InvalidT
			}
			if d.Value != nil {
				if t.Kind == types.Grow || t.Kind == types.Queue {
					c.d.Errorf(d.Value.EPos(), "E104", "%s starts empty and takes no initializer", t)
				} else {
					c.expr(d.Value, t)
					c.coerce(d.Value, t, "global "+d.Name)
					if !isConstExpr(d.Value) {
						c.d.ErrorFix(d.Value.EPos(), "E114", "global initializer is not a compile-time value", "assign it at the start of main")
					}
				}
			}
		case *ast.PeriphDecl:
			if d.Sym == nil {
				continue
			}
			sym := c.globals.syms[d.Type]
			if sym == nil || sym.Kind != types.SymType || sym.Type.Kind != types.Regs {
				c.d.ErrorFix(d.TP, "E124", fmt.Sprintf("`%s` is not a register block", d.Type), "declare it with `regs "+d.Type+"`")
				d.Sym.Type = types.InvalidT
				continue
			}
			d.Sym.Type = sym.Type
			if v, ok := c.constInt(d.Addr, nil); ok {
				if v.Sign() < 0 || v.BitLen() > 32 {
					c.d.Errorf(d.Addr.EPos(), "E103", "address %s is not a 32-bit address", v)
				}
				d.Sym.Addr = v
			}
		}
	}
}

// ---- entry points ----------------------------------------------------------

func (c *checker) entries() {
	var main *types.Fn
	var isrs []*types.Fn
	for _, fn := range c.prog.Fns {
		if fn.Main {
			main = fn
		}
		if fn.ISR {
			isrs = append(isrs, fn)
		}
		if fn.Main || fn.ISR {
			kind := "main"
			if fn.ISR {
				kind = "isr " + fn.Name
			}
			if len(fn.Params) > 0 || fn.Ret.Kind != types.Void || fn.Fallible() {
				c.d.ErrorFix(fn.Pos, "E121", kind+" must take no parameters and return nothing",
					"write `fn "+fn.Name+"()` and handle errors inside with catch")
			}
		}
	}
	if main == nil {
		pos := diag.Pos{Line: 1, Col: 1}
		if len(c.prog.Files) > 0 {
			pos.File = c.prog.Files[0].Name
		}
		c.d.ErrorFix(pos, "E206", "no `fn main()`", "add `fn main()`; it is the root of the stack analysis")
		return
	}
	c.prog.Entries = append([]*types.Fn{main}, isrs...)
}

// cKeywords would collide with C identifiers in generated code.
var cKeywords = map[string]bool{
	"auto": true, "break": true, "case": true, "char": true, "const": true, "continue": true,
	"default": true, "do": true, "double": true, "else": true, "enum": true, "extern": true,
	"float": true, "for": true, "goto": true, "if": true, "inline": true, "int": true, "long": true,
	"register": true, "restrict": true, "return": true, "short": true, "signed": true, "sizeof": true,
	"static": true, "struct": true, "switch": true, "typedef": true, "union": true, "unsigned": true,
	"void": true, "volatile": true, "while": true, "bool": true, "true": true, "false": true,
	"main": true, "NULL": true,
}

// CName maps a local name to a C identifier that cannot collide with C
// keywords or the generated ql_/qn_/QE_ namespaces.
func CName(name string) string {
	if cKeywords[name] || len(name) >= 3 && (name[:3] == "ql_" || name[:3] == "qn_" || name[:3] == "QE_") || name[0] == '_' {
		return name + "_"
	}
	return name
}
