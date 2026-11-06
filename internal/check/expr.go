package check

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/amken3d/quoin/internal/ast"
	"github.com/amken3d/quoin/internal/diag"
	"github.com/amken3d/quoin/internal/types"
)

// expr checks e with an optional expected type and records its type.
// Untyped integer constants stay untyped; callers coerce them.
func (c *checker) expr(e ast.Expr, want *types.Type) *types.Type {
	t := c.expr1(e, want)
	if t == nil {
		t = types.InvalidT
	}
	e.Info().Type = t
	return t
}

// value checks e as a value: a fallible call must be handled first.
func (c *checker) value(e ast.Expr, want *types.Type) *types.Type {
	t := c.expr(e, want)
	if t.Kind == types.Union {
		c.unhandled(e, t)
		e.Info().Type = types.InvalidT
		return types.InvalidT
	}
	return t
}

func (c *checker) unhandled(e ast.Expr, t *types.Type) {
	name := "call"
	if call, ok := e.(*ast.Call); ok {
		name = exprString(call.Fun) + "(...)"
	}
	fix := "append `?` to propagate, or `catch` to handle"
	if c.fn != nil && !c.fn.Fallible() {
		if c.fn.Main || c.fn.ISR {
			fix = "handle it with `catch` (e.g. `catch return`) — entry points cannot propagate errors"
		} else {
			fix = fmt.Sprintf("handle it with `catch`, or add `!%s` to %s's signature and append `?`", strings.Join(t.Errs, "|"), c.fn.Name)
		}
	}
	c.d.ErrorFix(e.EPos(), "E107", fmt.Sprintf("%s can fail with %s", name, strings.Join(t.Errs, "|")), fix)
}

// site counts an operation that needs a runtime check unless proven safe.
func (c *checker) site() {
	if c.fn != nil {
		c.fn.Sites++
	}
}

func (c *checker) needCheck(e ast.Expr, what string) {
	e.Info().Check = true
	if c.fn != nil {
		c.fn.Checks = append(c.fn.Checks, types.CheckSite{Pos: e.EPos(), What: what})
	}
}

// coerce checks that e (already checked) can be used as type to, binding
// untyped constants. It reports and returns false on mismatch.
func (c *checker) coerce(e ast.Expr, to *types.Type, what string) bool {
	info := e.Info()
	from := info.Type
	if from == nil || from.Kind == types.Invalid || to.Kind == types.Invalid {
		return true
	}
	if from.Kind == types.Union {
		c.unhandled(e, from)
		return false
	}
	if from.Kind == types.UntypedInt {
		if info.Const == nil {
			return true // already reported
		}
		if to.Kind != types.Int {
			c.d.Errorf(e.EPos(), "E104", "integer constant used as %s in %s", to, what)
			return false
		}
		if !to.Fits(info.Const) {
			c.d.ErrorFix(e.EPos(), "E103", fmt.Sprintf("constant %s does not fit %s (%s..%s)", info.Const, to, to.Min(), to.Max()), "")
			info.Type = types.InvalidT
			return false
		}
		info.Type = to
		info.Lo, info.Hi = info.Const, info.Const
		return true
	}
	if types.Identical(from, to) || types.Widens(from, to) {
		return true
	}
	if to.Kind == types.Slice && (from.Kind == types.Array || from.Kind == types.Grow) && types.Identical(from.Elem, to.Elem) {
		return c.viewable(e)
	}
	if from.Kind == types.Int && to.Kind == types.Int {
		c.d.ErrorFix(e.EPos(), "E117", fmt.Sprintf("%s does not fit %s implicitly (%s)", from, to, what),
			fmt.Sprintf("convert explicitly: %s(x) traps if out of range, %s.wrap(x) truncates", to, to))
		return false
	}
	if from.Kind == types.Void {
		c.d.Errorf(e.EPos(), "E104", "%s returns no value", exprString(e))
		return false
	}
	c.d.Errorf(e.EPos(), "E104", "want %s, got %s (%s)", to, from, what)
	return false
}

// viewable reports whether a slice may be taken of e: it must name storage
// that outlives any binding of the slice.
func (c *checker) viewable(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.Paren:
		return c.viewable(x.X)
	case *ast.Ident:
		if x.Sym == nil {
			return true
		}
		switch x.Sym.Kind {
		case types.SymLocal, types.SymParam:
			return true
		case types.SymGlobal:
			c.useGlobal(x.Sym, x.P, true)
			return true
		case types.SymConst:
			c.d.ErrorFix(x.P, "E118", fmt.Sprintf("cannot view constant `%s`", x.Name), "index it directly, or copy it into a `var` first")
			return false
		}
	case *ast.Field:
		if x.Kind == ast.FieldStruct {
			return c.viewable(x.X)
		}
	case *ast.Index:
		if t := x.X.Info().Type; t != nil && t.Kind == types.Slice {
			return true
		}
		return c.viewable(x.X)
	case *ast.SliceExpr:
		return true
	}
	c.d.ErrorFix(e.EPos(), "E118", "cannot view a temporary value", "bind it to a variable first")
	return false
}

func (c *checker) useGlobal(sym *types.Symbol, pos diag.Pos, write bool) {
	if c.fn == nil || sym.Kind != types.SymGlobal {
		return
	}
	u, ok := c.fn.Globals[sym]
	if !ok {
		u.Pos = pos
	}
	if write {
		u.Write = true
	}
	c.fn.Globals[sym] = u
}

func (c *checker) expr1(e ast.Expr, want *types.Type) *types.Type {
	switch x := e.(type) {
	case *ast.IntLit:
		x.I.Const = x.Val
		x.I.Lo, x.I.Hi = x.Val, x.Val
		return types.UntypedT
	case *ast.BoolLit:
		return types.BoolT
	case *ast.Paren:
		t := c.expr(x.X, want)
		in := x.X.Info()
		x.I.Const, x.I.Lo, x.I.Hi = in.Const, in.Lo, in.Hi
		return t
	case *ast.Ident:
		return c.ident(x)
	case *ast.EnumLit:
		if want == nil || want.Kind != types.Enum {
			if want == nil || want.Kind != types.Invalid {
				c.d.ErrorFix(x.P, "E104", fmt.Sprintf("`.%s` needs an enum type from context", x.Name), "write `EnumName."+x.Name+"`")
			}
			return types.InvalidT
		}
		return c.enumMember(x, want, x.Name, &x.Member)
	case *ast.Unary:
		return c.unary(x, want)
	case *ast.Binary:
		return c.binary(x, want)
	case *ast.Call:
		return c.call(x, want)
	case *ast.Index:
		return c.index(x)
	case *ast.SliceExpr:
		return c.sliceExpr(x)
	case *ast.Field:
		return c.field(x)
	case *ast.Try:
		t := c.expr(x.X, nil)
		if t.Kind == types.Invalid {
			return t
		}
		if t.Kind != types.Union {
			c.d.ErrorFix(x.P, "E104", exprString(x.X)+" cannot fail", "drop the `?`")
			return types.InvalidT
		}
		if c.shortCirc > 0 {
			c.d.ErrorFix(x.P, "E119", "`?` on the right of && or ||", "bind the call to a variable first")
		}
		c.checkErrs(t.Errs, x.P)
		return t.Elem
	case *ast.Catch:
		return c.catch(x)
	case *ast.StructLit:
		return c.structLit(x)
	case *ast.ArrayLit:
		return c.arrayLit(x, want)
	}
	return types.InvalidT
}

func (c *checker) ident(x *ast.Ident) *types.Type {
	sym := c.cur.lookup(x.Name)
	if sym == nil {
		if builtinType(x.Name) != nil {
			c.d.ErrorFix(x.P, "E104", fmt.Sprintf("type `%s` used as a value", x.Name), "convert with "+x.Name+"(value)")
			return types.InvalidT
		}
		c.d.ErrorFix(x.P, "E101", fmt.Sprintf("undefined `%s`", x.Name), c.suggest(x.Name))
		return types.InvalidT
	}
	x.Sym = sym
	switch sym.Kind {
	case types.SymConst:
		if sym.Type == nil {
			c.resolveConst(sym)
		}
		if sym.Type == nil {
			return types.InvalidT
		}
		x.I.Const = sym.Const
		if sym.Const != nil {
			x.I.Lo, x.I.Hi = sym.Const, sym.Const
		}
		return sym.Type
	case types.SymGlobal:
		c.useGlobal(sym, x.P, false)
		return sym.Type
	case types.SymLocal, types.SymParam:
		x.I.Lo, x.I.Hi = sym.Lo, sym.Hi
		return sym.Type
	case types.SymErr:
		return types.ErrorValT
	case types.SymPeriph:
		return sym.Type
	case types.SymFn:
		c.d.ErrorFix(x.P, "E105", fmt.Sprintf("fn `%s` is not a value", x.Name), "call it: "+x.Name+"(...)")
	case types.SymType:
		c.d.ErrorFix(x.P, "E104", fmt.Sprintf("type `%s` used as a value", x.Name), "")
	}
	return types.InvalidT
}

func (c *checker) suggest(name string) string {
	best, bestD := "", 3
	for s := c.cur; s != nil; s = s.parent {
		for n := range s.syms {
			if d := editDistance(strings.ToLower(n), strings.ToLower(name)); d < bestD || (d == bestD && n < best) {
				best, bestD = n, d
			}
		}
	}
	if best != "" && bestD <= 2 {
		return "did you mean `" + best + "`?"
	}
	return ""
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

func (c *checker) enumMember(e ast.Expr, t *types.Type, name string, out **types.EnumMember) *types.Type {
	for _, m := range t.Members {
		if m.Name == name {
			*out = m
			e.Info().Const = m.Value
			e.Info().Lo, e.Info().Hi = m.Value, m.Value
			return t
		}
	}
	var names []string
	for _, m := range t.Members {
		names = append(names, m.Name)
	}
	c.d.ErrorFix(e.EPos(), "E112", fmt.Sprintf("enum %s has no member `%s`", t.Name, name), "members: "+strings.Join(names, ", "))
	return types.InvalidT
}

// ---- intervals -------------------------------------------------------------

// rng returns known bounds for an integer expression.
func rng(e ast.Expr) (lo, hi *big.Int) {
	in := e.Info()
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

func typeRange(t *types.Type) (lo, hi *big.Int) {
	switch t.Kind {
	case types.Enum:
		return t.Base.Min(), t.Base.Max()
	case types.Int:
		return t.Min(), t.Max()
	case types.Bool:
		return big.NewInt(0), big.NewInt(1)
	}
	return nil, nil
}

func within(lo, hi *big.Int, t *types.Type) bool {
	return lo != nil && hi != nil && t.Fits(lo) && t.Fits(hi)
}

func setRange(e ast.Expr, lo, hi *big.Int, t *types.Type) {
	in := e.Info()
	if lo == nil || hi == nil || t.Kind != types.Int {
		return
	}
	if lo.Cmp(t.Min()) < 0 {
		lo = t.Min()
	}
	if hi.Cmp(t.Max()) > 0 {
		hi = t.Max()
	}
	if lo.Cmp(hi) > 0 {
		return
	}
	in.Lo, in.Hi = lo, hi
}

func bigMin(a ...*big.Int) *big.Int {
	m := a[0]
	for _, v := range a[1:] {
		if v.Cmp(m) < 0 {
			m = v
		}
	}
	return m
}

func bigMax(a ...*big.Int) *big.Int {
	m := a[0]
	for _, v := range a[1:] {
		if v.Cmp(m) > 0 {
			m = v
		}
	}
	return m
}

// ---- operators -------------------------------------------------------------

func (c *checker) boolOperand(e ast.Expr) {
	t := c.value(e, types.BoolT)
	if t.Kind != types.Invalid && t.Kind != types.Bool {
		fix := ""
		if t.IsIntLike() {
			fix = "compare explicitly, e.g. `x != 0`"
		}
		c.d.ErrorFix(e.EPos(), "E104", fmt.Sprintf("want bool, got %s", t), fix)
	}
}

func (c *checker) unary(x *ast.Unary, want *types.Type) *types.Type {
	switch x.Op {
	case "!":
		c.boolOperand(x.X)
		return types.BoolT
	case "-":
		t := c.value(x.X, want)
		if t.Kind == types.Invalid {
			return t
		}
		if !t.IsIntLike() {
			c.d.Errorf(x.P, "E113", "unary - on %s", t)
			return types.InvalidT
		}
		if t.Kind == types.Int && !t.Signed {
			c.d.ErrorFix(x.P, "E113", "unary - on unsigned "+t.String(), "use `0 -% x` for wrapping negation, or a signed type")
			return types.InvalidT
		}
		if k := x.X.Info().Const; k != nil {
			v := new(big.Int).Neg(k)
			if t.Kind == types.Int && !t.Fits(v) {
				c.d.Errorf(x.P, "E103", "constant -(%s) overflows %s", k, t)
				return types.InvalidT
			}
			x.I.Const, x.I.Lo, x.I.Hi = v, v, v
			return t
		}
		lo, hi := rng(x.X)
		c.site()
		if lo.Cmp(t.Min()) == 0 {
			c.needCheck(x, "negate")
		}
		setRange(x, new(big.Int).Neg(hi), new(big.Int).Neg(lo), t)
		return t
	case "~":
		t := c.value(x.X, want)
		if t.Kind == types.UntypedInt {
			if want != nil && want.Kind == types.Int {
				c.coerce(x.X, want, "operand of ~")
				t = want
			} else {
				c.d.ErrorFix(x.P, "E113", "`~` needs a typed operand", "write e.g. ~u32(...)")
				return types.InvalidT
			}
		}
		if t.Kind == types.Invalid {
			return t
		}
		if t.Kind != types.Int {
			c.d.Errorf(x.P, "E113", "`~` on %s", t)
			return types.InvalidT
		}
		if k := x.X.Info().Const; k != nil {
			var v *big.Int
			if t.Signed {
				v = new(big.Int).Sub(new(big.Int).Neg(k), big.NewInt(1))
			} else {
				v = new(big.Int).Xor(k, t.Max())
			}
			x.I.Const, x.I.Lo, x.I.Hi = v, v, v
		}
		return t
	}
	return types.InvalidT
}

func ctxDependent(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.EnumLit, *ast.ArrayLit:
		return true
	case *ast.Paren:
		return ctxDependent(x.X)
	}
	return false
}

// operands checks both sides of a binary operator, letting each side give
// context to an enum literal or untyped constant on the other.
func (c *checker) operands(x, y ast.Expr, want *types.Type) (tx, ty *types.Type) {
	if ctxDependent(x) && !ctxDependent(y) {
		ty = c.value(y, want)
		tx = c.value(x, ty)
		return
	}
	tx = c.value(x, want)
	ctx := tx
	if tx.Kind == types.UntypedInt {
		ctx = want
	}
	ty = c.value(y, ctx)
	return
}

// unify returns the common type of two operands, binding untyped constants.
func (c *checker) unify(pos ast.Expr, x, y ast.Expr, tx, ty *types.Type) *types.Type {
	if tx.Kind == types.Invalid || ty.Kind == types.Invalid {
		return types.InvalidT
	}
	if tx.Kind == types.UntypedInt && ty.Kind == types.UntypedInt {
		return types.UntypedT
	}
	if tx.Kind == types.UntypedInt {
		if !c.coerce(x, ty, "operand") {
			return types.InvalidT
		}
		return ty
	}
	if ty.Kind == types.UntypedInt {
		if !c.coerce(y, tx, "operand") {
			return types.InvalidT
		}
		return tx
	}
	if types.Identical(tx, ty) {
		return tx
	}
	if types.Widens(tx, ty) {
		return ty
	}
	if types.Widens(ty, tx) {
		return tx
	}
	fix := ""
	if tx.Kind == types.Int && ty.Kind == types.Int {
		fix = fmt.Sprintf("convert one side explicitly, e.g. %s(%s)", tx, exprString(y))
	}
	c.d.ErrorFix(pos.EPos(), "E104", fmt.Sprintf("mismatched operands %s and %s", tx, ty), fix)
	return types.InvalidT
}

func isChecked(op string) bool {
	switch op {
	case "+", "-", "*", "/", "%":
		return true
	}
	return false
}

func (c *checker) binary(b *ast.Binary, want *types.Type) *types.Type {
	switch b.Op {
	case "&&", "||":
		c.boolOperand(b.X)
		c.shortCirc++
		c.boolOperand(b.Y)
		c.shortCirc--
		return types.BoolT
	case "<<", ">>":
		return c.shift(b, want)
	}
	cmp := false
	switch b.Op {
	case "==", "!=", "<", "<=", ">", ">=":
		cmp = true
		want = nil
	}
	tx, ty := c.operands(b.X, b.Y, want)
	t := c.unify(b, b.X, b.Y, tx, ty)
	if t.Kind == types.Invalid {
		return types.InvalidT
	}
	if cmp {
		switch b.Op {
		case "==", "!=":
			switch t.Kind {
			case types.Int, types.UntypedInt, types.Bool, types.Enum, types.ErrorVal:
			default:
				c.d.ErrorFix(b.P, "E113", fmt.Sprintf("cannot compare %s values with %s", t, b.Op), "compare fields one by one")
			}
		default:
			if !t.IsIntLike() {
				c.d.Errorf(b.P, "E113", "cannot order %s values", t)
			}
		}
		return types.BoolT
	}
	if !t.IsIntLike() {
		c.d.Errorf(b.P, "E113", "operator %s on %s", b.Op, t)
		return types.InvalidT
	}
	wrapSat := strings.HasSuffix(b.Op, "%") && b.Op != "%" || strings.HasSuffix(b.Op, "|") && b.Op != "|"
	if t.Kind == types.UntypedInt && wrapSat {
		if want != nil && want.Kind == types.Int {
			c.coerce(b.X, want, "operand")
			c.coerce(b.Y, want, "operand")
			t = want
		} else {
			c.d.ErrorFix(b.P, "E113", fmt.Sprintf("`%s` needs a typed operand", b.Op), "give one side a type, e.g. u8(...)")
			return types.InvalidT
		}
	}
	kx, ky := b.X.Info().Const, b.Y.Info().Const
	if kx != nil && ky != nil {
		v, err := foldArith(b.Op, kx, ky, t)
		if err != "" {
			c.d.Errorf(b.P, "E103", "%s", err)
			return types.InvalidT
		}
		b.I.Const, b.I.Lo, b.I.Hi = v, v, v
		return t
	}
	// Runtime operation: compute an interval and decide whether to check.
	lx, hx := rng(b.X)
	ly, hy := rng(b.Y)
	var lo, hi *big.Int
	switch b.Op {
	case "+":
		lo, hi = new(big.Int).Add(lx, ly), new(big.Int).Add(hx, hy)
	case "-":
		lo, hi = new(big.Int).Sub(lx, hy), new(big.Int).Sub(hx, ly)
	case "*":
		p := []*big.Int{new(big.Int).Mul(lx, ly), new(big.Int).Mul(lx, hy), new(big.Int).Mul(hx, ly), new(big.Int).Mul(hx, hy)}
		lo, hi = bigMin(p...), bigMax(p...)
	case "/":
		if ly.Sign() > 0 && lx.Sign() >= 0 {
			lo, hi = new(big.Int).Quo(lx, hy), new(big.Int).Quo(hx, ly)
		}
	case "%":
		if ly.Sign() > 0 && lx.Sign() >= 0 {
			lo, hi = big.NewInt(0), bigMin(hx, new(big.Int).Sub(hy, big.NewInt(1)))
		}
	case "&":
		if lx.Sign() >= 0 && ly.Sign() >= 0 {
			lo, hi = big.NewInt(0), bigMin(hx, hy)
		}
	case "|", "^":
		if lx.Sign() >= 0 && ly.Sign() >= 0 {
			n := bigMax(hx, hy).BitLen()
			lo, hi = big.NewInt(0), new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(n)), big.NewInt(1))
		}
	}
	if isChecked(b.Op) {
		c.site()
		switch b.Op {
		case "/", "%":
			// Division cannot overflow except MIN / -1; it can divide by zero.
			if ly.Sign() <= 0 && hy.Sign() >= 0 {
				c.needCheck(b, "divide by zero")
			} else if t.Signed && lx.Cmp(t.Min()) == 0 && ly.Cmp(big.NewInt(-1)) <= 0 && hy.Cmp(big.NewInt(-1)) >= 0 {
				c.needCheck(b, "divide overflow")
			}
		default:
			if !within(lo, hi, t) {
				c.needCheck(b, "overflow "+b.Op)
			}
		}
	}
	setRange(b, lo, hi, t)
	return t
}

// foldArith evaluates a constant binary operation in type t.
func foldArith(op string, x, y *big.Int, t *types.Type) (*big.Int, string) {
	v := new(big.Int)
	base := strings.TrimRight(op, "%|")
	if op == "%" {
		base = "%"
	}
	switch base {
	case "+":
		v.Add(x, y)
	case "-":
		v.Sub(x, y)
	case "*":
		v.Mul(x, y)
	case "/", "%":
		if y.Sign() == 0 {
			return nil, "constant division by zero"
		}
		if base == "/" {
			v.Quo(x, y)
		} else {
			v.Rem(x, y)
		}
	case "&":
		v.And(x, y)
	case "|":
		v.Or(x, y)
	case "^":
		v.Xor(x, y)
	}
	if t.Kind != types.Int {
		return v, ""
	}
	switch {
	case op != "%" && strings.HasSuffix(op, "%"):
		return wrapTo(v, t), ""
	case op != "|" && strings.HasSuffix(op, "|"):
		return satTo(v, t), ""
	}
	if !t.Fits(v) {
		return nil, fmt.Sprintf("constant %s %s %s = %s overflows %s", x, op, y, v, t)
	}
	return v, ""
}

func wrapTo(v *big.Int, t *types.Type) *big.Int {
	mod := new(big.Int).Lsh(big.NewInt(1), uint(t.Bits))
	r := new(big.Int).Mod(v, mod)
	if t.Signed && r.Cmp(t.Max()) > 0 {
		r.Sub(r, mod)
	}
	return r
}

func satTo(v *big.Int, t *types.Type) *big.Int {
	if v.Cmp(t.Min()) < 0 {
		return t.Min()
	}
	if v.Cmp(t.Max()) > 0 {
		return t.Max()
	}
	return new(big.Int).Set(v)
}

func (c *checker) shift(b *ast.Binary, want *types.Type) *types.Type {
	tx := c.value(b.X, want)
	ty := c.value(b.Y, nil)
	if tx.Kind == types.Invalid || ty.Kind == types.Invalid {
		return types.InvalidT
	}
	if !ty.IsIntLike() || (ty.Kind == types.Int && ty.Signed) {
		c.d.ErrorFix(b.Y.EPos(), "E104", "shift amount must be an unsigned integer, got "+ty.String(), "convert it: u32(n)")
		return types.InvalidT
	}
	if ty.Kind == types.UntypedInt {
		if b.Y.Info().Const.Sign() < 0 {
			c.d.Errorf(b.Y.EPos(), "E126", "negative shift %s", b.Y.Info().Const)
			return types.InvalidT
		}
		if !c.coerce(b.Y, types.U32, "shift amount") {
			return types.InvalidT
		}
	}
	kx, ky := b.X.Info().Const, b.Y.Info().Const
	if tx.Kind == types.UntypedInt {
		if kx != nil && ky != nil {
			v := new(big.Int)
			if b.Op == "<<" {
				v.Lsh(kx, uint(ky.Uint64()))
			} else {
				v.Rsh(kx, uint(ky.Uint64()))
			}
			b.I.Const, b.I.Lo, b.I.Hi = v, v, v
			return types.UntypedT
		}
		if want != nil && want.Kind == types.Int {
			c.coerce(b.X, want, "shifted value")
			tx = want
		} else {
			c.d.ErrorFix(b.X.EPos(), "E113", "shifted constant needs a type", "write e.g. u32(1) << n")
			return types.InvalidT
		}
	}
	if tx.Kind != types.Int {
		c.d.Errorf(b.P, "E113", "shift of %s", tx)
		return types.InvalidT
	}
	bits := big.NewInt(int64(tx.Bits))
	if ky != nil {
		if ky.Cmp(bits) >= 0 {
			c.d.Errorf(b.Y.EPos(), "E126", "shift by %s is not smaller than the width of %s", ky, tx)
			return types.InvalidT
		}
		if kx != nil {
			var v *big.Int
			if b.Op == "<<" {
				v = wrapTo(new(big.Int).Lsh(kx, uint(ky.Uint64())), tx)
			} else {
				v = new(big.Int).Rsh(kx, uint(ky.Uint64()))
			}
			b.I.Const, b.I.Lo, b.I.Hi = v, v, v
			return tx
		}
	} else {
		c.site()
		if _, hy := rng(b.Y); hy == nil || hy.Cmp(bits) >= 0 {
			c.needCheck(b, "shift amount")
		}
	}
	if b.Op == ">>" && !tx.Signed {
		lx, hx := rng(b.X)
		ly, hy := rng(b.Y)
		if lx != nil && ly != nil {
			setRange(b, new(big.Int).Rsh(lx, uint(min(hy.Uint64(), 64))), new(big.Int).Rsh(hx, uint(min(ly.Uint64(), 64))), tx)
		}
	}
	return tx
}

// ---- calls -----------------------------------------------------------------

func (c *checker) call(x *ast.Call, want *types.Type) *types.Type {
	switch fun := x.Fun.(type) {
	case *ast.Ident:
		if bt := builtinType(fun.Name); bt != nil {
			return c.convert(x, bt, ast.CallConvert)
		}
		sym := c.cur.lookup(fun.Name)
		if sym == nil {
			c.d.ErrorFix(fun.P, "E101", fmt.Sprintf("undefined `%s`", fun.Name), c.suggest(fun.Name))
			c.argsOnly(x)
			return types.InvalidT
		}
		fun.Sym = sym
		switch sym.Kind {
		case types.SymFn:
			return c.callFn(x, sym.Fn)
		case types.SymType:
			c.completeType(sym)
			return c.convert(x, sym.Type, ast.CallConvert)
		}
		c.d.Errorf(fun.P, "E105", "`%s` is not a function", fun.Name)
		c.argsOnly(x)
		return types.InvalidT
	case *ast.Field:
		if id, ok := fun.X.(*ast.Ident); ok && (fun.Name == "wrap" || fun.Name == "sat") {
			bt := builtinType(id.Name)
			if bt == nil {
				if sym := c.cur.lookup(id.Name); sym != nil && sym.Kind == types.SymType {
					bt = sym.Type
				}
			}
			if bt != nil {
				kind := ast.CallWrap
				if fun.Name == "sat" {
					kind = ast.CallSat
				}
				return c.convert(x, bt, kind)
			}
		}
		rt := c.expr(fun.X, nil)
		switch rt.Kind {
		case types.Invalid:
			c.argsOnly(x)
			return types.InvalidT
		case types.Grow:
			return c.growMethod(x, fun, rt)
		case types.Queue:
			return c.queueMethod(x, fun, rt)
		}
		c.d.Errorf(fun.P, "E105", "%s has no method `%s`", rt, fun.Name)
		c.argsOnly(x)
		return types.InvalidT
	}
	c.d.Errorf(x.P, "E105", "cannot call %s", exprString(x.Fun))
	c.argsOnly(x)
	return types.InvalidT
}

func (c *checker) argsOnly(x *ast.Call) {
	for _, a := range x.Args {
		c.expr(a, nil)
	}
}

func signature(fn *types.Fn) string {
	var ps []string
	for _, p := range fn.Params {
		ps = append(ps, p.Name+": "+p.Type.String())
	}
	s := fn.Name + "(" + strings.Join(ps, ", ") + ")"
	if fn.Ret.Kind != types.Void {
		s += " " + fn.Ret.String()
	}
	if fn.Fallible() {
		s += " !" + strings.Join(fn.Errs, "|")
	}
	return s
}

func (c *checker) callFn(x *ast.Call, fn *types.Fn) *types.Type {
	x.Kind = ast.CallFn
	x.Callee = fn
	if fn.Main || fn.ISR {
		c.d.ErrorFix(x.P, "E105", "entry point "+fn.Name+" cannot be called", "move the shared code into a separate fn")
	}
	if len(x.Args) != len(fn.Params) {
		c.d.ErrorFix(x.P, "E105", fmt.Sprintf("%s takes %s, got %d", fn.Name, plural(len(fn.Params), "argument"), len(x.Args)), "signature: "+signature(fn))
		c.argsOnly(x)
		return fn.ResultType()
	}
	for i, a := range x.Args {
		c.passArg(a, fn.Params[i], fn)
	}
	if c.fn != nil {
		c.fn.Calls = append(c.fn.Calls, types.Call{Callee: fn, Pos: x.P})
	} else {
		c.d.ErrorFix(x.P, "E114", "call in a compile-time context", "")
	}
	return fn.ResultType()
}

func (c *checker) passArg(a ast.Expr, p *types.Symbol, fn *types.Fn) {
	pt := p.Type
	at := c.value(a, pt)
	if at.Kind == types.Invalid || pt.Kind == types.Invalid {
		return
	}
	what := fmt.Sprintf("argument %s of %s", p.Name, fn.Name)
	switch pt.Kind {
	case types.Grow:
		id, ok := unparen(a).(*ast.Ident)
		if !ok || !types.Identical(at, pt) {
			c.d.Errorf(a.EPos(), "E104", "want %s variable, got %s (%s)", pt, at, what)
			return
		}
		if id.Sym != nil && id.Sym.Kind == types.SymGlobal {
			c.useGlobal(id.Sym, id.P, true)
		}
		return
	case types.Regs:
		if !types.Identical(at, pt) {
			c.d.Errorf(a.EPos(), "E104", "want %s, got %s (%s)", pt, at, what)
		}
		return
	}
	c.coerce(a, pt, what)
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

func (c *checker) convert(x *ast.Call, target *types.Type, kind ast.CallKind) *types.Type {
	x.Kind = kind
	x.Target = target
	name := target.String()
	if len(x.Args) != 1 {
		c.d.Errorf(x.P, "E105", "conversion %s(...) takes 1 argument, got %d", name, len(x.Args))
		c.argsOnly(x)
		return types.InvalidT
	}
	a := x.Args[0]
	if target.Kind == types.Enum {
		if kind != ast.CallConvert {
			c.d.Errorf(x.P, "E113", "%s.wrap/.sat apply to integer types", name)
			return types.InvalidT
		}
		at := c.value(a, target.Base)
		if at.Kind == types.Invalid {
			return types.InvalidT
		}
		k := a.Info().Const
		if k == nil || !at.IsIntLike() {
			c.d.ErrorFix(a.EPos(), "E114", "integers convert to enums only as constants", "compare against members instead, e.g. `x == "+name+"."+target.Members[0].Name+"`")
			return types.InvalidT
		}
		for _, m := range target.Members {
			if m.Value.Cmp(k) == 0 {
				x.I.Const, x.I.Lo, x.I.Hi = k, k, k
				return target
			}
		}
		c.d.Errorf(a.EPos(), "E103", "%s is not a value of enum %s", k, name)
		return types.InvalidT
	}
	if target.Kind != types.Int {
		c.d.Errorf(x.P, "E113", "cannot convert to %s", name)
		c.argsOnly(x)
		return types.InvalidT
	}
	at := c.value(a, nil)
	switch at.Kind {
	case types.Invalid:
		return types.InvalidT
	case types.Int, types.UntypedInt:
	case types.Bool, types.Enum:
		if kind != ast.CallConvert {
			c.d.Errorf(x.P, "E113", "%s.%s takes an integer", name, map[ast.CallKind]string{ast.CallWrap: "wrap", ast.CallSat: "sat"}[kind])
			return types.InvalidT
		}
	default:
		c.d.Errorf(a.EPos(), "E113", "cannot convert %s to %s", at, name)
		return types.InvalidT
	}
	if k := a.Info().Const; k != nil {
		var v *big.Int
		switch kind {
		case ast.CallConvert:
			if !target.Fits(k) {
				c.d.ErrorFix(a.EPos(), "E103", fmt.Sprintf("constant %s does not fit %s", k, name), fmt.Sprintf("use %s.wrap(...) to truncate", name))
				return types.InvalidT
			}
			v = k
		case ast.CallWrap:
			v = wrapTo(k, target)
		case ast.CallSat:
			v = satTo(k, target)
		}
		x.I.Const, x.I.Lo, x.I.Hi = v, v, v
		if at.Kind == types.UntypedInt {
			a.Info().Type = target
		}
		return target
	}
	lo, hi := rng(a)
	if at.Kind == types.Bool {
		lo, hi = big.NewInt(0), big.NewInt(1)
	}
	if kind == ast.CallConvert {
		if tl, th := typeRange(at); !within(tl, th, target) {
			c.site()
		}
		if !within(lo, hi, target) {
			c.needCheck(x, "conversion to "+name)
		}
	}
	if kind == ast.CallConvert || within(lo, hi, target) {
		setRange(x, lo, hi, target)
	}
	return target
}

func (c *checker) growMethod(x *ast.Call, fun *ast.Field, gt *types.Type) *types.Type {
	x.Recv = fun.X
	if id, ok := unparen(fun.X).(*ast.Ident); ok && id.Sym != nil && id.Sym.Kind == types.SymGlobal {
		c.useGlobal(id.Sym, id.P, true)
	}
	switch fun.Name {
	case "push":
		x.Kind = ast.CallPush
		if len(x.Args) != 1 {
			c.d.Errorf(x.P, "E105", "push takes 1 argument, got %d", len(x.Args))
			c.argsOnly(x)
			return types.VoidT
		}
		c.value(x.Args[0], gt.Elem)
		c.coerce(x.Args[0], gt.Elem, "push")
		c.site()
		c.needCheck(x, fmt.Sprintf("push capacity %d", gt.Len))
		return types.VoidT
	case "clear":
		x.Kind = ast.CallClear
		if len(x.Args) != 0 {
			c.d.Errorf(x.P, "E105", "clear takes no arguments")
		}
		return types.VoidT
	}
	c.d.ErrorFix(fun.P, "E112", fmt.Sprintf("%s has no method `%s`", gt, fun.Name), "grow arrays have push(x), clear(), .len, .cap")
	c.argsOnly(x)
	return types.InvalidT
}

func (c *checker) queueMethod(x *ast.Call, fun *ast.Field, qt *types.Type) *types.Type {
	x.Recv = fun.X
	id, ok := unparen(fun.X).(*ast.Ident)
	if !ok || id.Sym == nil || id.Sym.Kind != types.SymGlobal {
		c.d.Errorf(fun.P, "E104", "queue methods apply to global queues")
		return types.InvalidT
	}
	use := c.fn.QueueUses[id.Sym]
	switch fun.Name {
	case "send":
		x.Kind = ast.CallSend
		if len(x.Args) != 1 {
			c.d.Errorf(x.P, "E105", "send takes 1 argument, got %d", len(x.Args))
			c.argsOnly(x)
		} else {
			c.value(x.Args[0], qt.Elem)
			c.coerce(x.Args[0], qt.Elem, "send")
		}
		if !use.Send {
			use.Send, use.SendPos = true, x.P
		}
		c.fn.QueueUses[id.Sym] = use
		return &types.Type{Kind: types.Union, Elem: types.VoidT, Errs: []string{"Full"}}
	case "recv":
		x.Kind = ast.CallRecv
		if len(x.Args) != 0 {
			c.d.Errorf(x.P, "E105", "recv takes no arguments")
		}
		if !use.Recv {
			use.Recv, use.RecvPos = true, x.P
		}
		c.fn.QueueUses[id.Sym] = use
		return &types.Type{Kind: types.Union, Elem: qt.Elem, Errs: []string{"Empty"}}
	}
	c.d.ErrorFix(fun.P, "E112", fmt.Sprintf("%s has no method `%s`", qt, fun.Name), "queues have send(x) !Full and recv() !Empty")
	c.argsOnly(x)
	return types.InvalidT
}

// ---- selectors, indexing, slicing -----------------------------------------

func (c *checker) field(x *ast.Field) *types.Type {
	// Enum member through the type name: Mode.Out.
	if id, ok := x.X.(*ast.Ident); ok {
		if sym := c.cur.lookup(id.Name); sym != nil && sym.Kind == types.SymType {
			id.Sym = sym
			c.completeType(sym)
			if sym.Type.Kind == types.Enum {
				x.Kind = ast.FieldEnum
				id.I.Type = sym.Type
				return c.enumMember(x, sym.Type, x.Name, &x.Member)
			}
			c.d.Errorf(x.P, "E112", "type %s has no member `%s`", sym.Name, x.Name)
			return types.InvalidT
		}
	}
	t := c.value(x.X, nil)
	if t.Kind == types.Invalid {
		return t
	}
	// Bitfield of a register access.
	if inner, ok := unparen(x.X).(*ast.Field); ok && inner.Kind == ast.FieldReg {
		for _, bf := range inner.Reg.Fields {
			if bf.Name == x.Name {
				x.Kind = ast.FieldBits
				x.Bits = bf
				return bf.Type
			}
		}
		var names []string
		for _, bf := range inner.Reg.Fields {
			names = append(names, bf.Name)
		}
		fix := "fields: " + strings.Join(names, ", ")
		if len(names) == 0 {
			fix = "declare bitfields indented under the register"
		}
		c.d.ErrorFix(x.P, "E112", fmt.Sprintf("register %s has no field `%s`", inner.Reg.Name, x.Name), fix)
		return types.InvalidT
	}
	switch t.Kind {
	case types.Struct:
		for _, f := range t.Fields {
			if f.Name == x.Name {
				x.Kind = ast.FieldStruct
				return f.Type
			}
		}
		var names []string
		for _, f := range t.Fields {
			names = append(names, f.Name)
		}
		c.d.ErrorFix(x.P, "E112", fmt.Sprintf("struct %s has no field `%s`", t.Name, x.Name), "fields: "+strings.Join(names, ", "))
		return types.InvalidT
	case types.Regs:
		for _, r := range t.Regs {
			if r.Name == x.Name {
				x.Kind = ast.FieldReg
				x.Reg = r
				return r.Type
			}
		}
		var names []string
		for _, r := range t.Regs {
			names = append(names, r.Name)
		}
		c.d.ErrorFix(x.P, "E112", fmt.Sprintf("register block %s has no register `%s`", t.Name, x.Name), "registers: "+strings.Join(names, ", "))
		return types.InvalidT
	case types.Array, types.Slice, types.Grow, types.Queue:
		switch x.Name {
		case "len":
			if t.Kind == types.Queue {
				break
			}
			x.Kind = ast.FieldLen
			if t.Kind == types.Array {
				v := big.NewInt(t.Len)
				x.I.Const, x.I.Lo, x.I.Hi = v, v, v
			} else if t.Kind == types.Grow {
				x.I.Lo, x.I.Hi = big.NewInt(0), big.NewInt(t.Len)
			} else if id, ok := x.X.(*ast.Ident); ok && id.Sym != nil && c.lenMax[id.Sym] != nil {
				x.I.Lo, x.I.Hi = big.NewInt(0), c.lenMax[id.Sym]
			}
			return types.Usize
		case "cap":
			if t.Kind == types.Grow || t.Kind == types.Queue {
				x.Kind = ast.FieldCap
				v := big.NewInt(t.Len)
				x.I.Const, x.I.Lo, x.I.Hi = v, v, v
				return types.Usize
			}
		}
		if (x.Name == "push" || x.Name == "clear" || x.Name == "send" || x.Name == "recv") && (t.Kind == types.Grow || t.Kind == types.Queue) {
			c.d.ErrorFix(x.P, "E105", "method `"+x.Name+"` used without a call", "call it: ."+x.Name+"(...)")
			return types.InvalidT
		}
		c.d.Errorf(x.P, "E112", "%s has no member `%s`", t, x.Name)
		return types.InvalidT
	}
	c.d.Errorf(x.P, "E112", "%s has no member `%s`", t, x.Name)
	return types.InvalidT
}

func (c *checker) indexOperand(e ast.Expr, what string) bool {
	t := c.value(e, types.Usize)
	switch {
	case t.Kind == types.Invalid:
		return false
	case t.Kind == types.UntypedInt:
		if e.Info().Const.Sign() < 0 {
			c.d.Errorf(e.EPos(), "E126", "negative %s %s", what, e.Info().Const)
			return false
		}
		return c.coerce(e, types.Usize, what)
	case t.Kind == types.Int && !t.Signed:
		if t.Bits > 32 {
			c.d.ErrorFix(e.EPos(), "E117", what+" must fit usize", "convert: u32(i)")
			return false
		}
		return true
	}
	c.d.ErrorFix(e.EPos(), "E104", what+" must be unsigned, got "+t.String(), "convert: u32(i)")
	return false
}

func (c *checker) index(x *ast.Index) *types.Type {
	t := c.value(x.X, nil)
	ok := c.indexOperand(x.I, "index")
	if t.Kind == types.Invalid {
		return t
	}
	switch t.Kind {
	case types.Array, types.Slice, types.Grow:
	default:
		c.d.Errorf(x.P, "E113", "cannot index %s", t)
		return types.InvalidT
	}
	if !ok {
		return t.Elem
	}
	if t.Kind == types.Array {
		if k := x.I.Info().Const; k != nil {
			if k.Cmp(big.NewInt(t.Len)) >= 0 {
				c.d.Errorf(x.I.EPos(), "E126", "index %s out of range for %s", k, t)
			}
			return t.Elem
		}
		c.site()
		if _, hi := rng(x.I); hi != nil && hi.Cmp(big.NewInt(t.Len)) < 0 {
			return t.Elem
		}
	}
	if t.Kind != types.Array {
		c.site()
	}
	c.needCheck(x, "index")
	return t.Elem
}

func (c *checker) sliceExpr(x *ast.SliceExpr) *types.Type {
	t := c.value(x.X, nil)
	okLo := x.Lo == nil || c.indexOperand(x.Lo, "slice bound")
	okHi := x.Hi == nil || c.indexOperand(x.Hi, "slice bound")
	if t.Kind == types.Invalid {
		return t
	}
	switch t.Kind {
	case types.Array, types.Grow:
		if !c.viewable(x.X) {
			return types.InvalidT
		}
	case types.Slice:
	default:
		c.d.Errorf(x.P, "E113", "cannot slice %s", t)
		return types.InvalidT
	}
	st := &types.Type{Kind: types.Slice, Elem: t.Elem}
	if !okLo || !okHi {
		return st
	}
	if t.Kind == types.Array {
		lo, hi := big.NewInt(0), big.NewInt(t.Len)
		constant := true
		if x.Lo != nil {
			if k := x.Lo.Info().Const; k != nil {
				lo = k
			} else {
				constant = false
			}
		}
		if x.Hi != nil {
			if k := x.Hi.Info().Const; k != nil {
				hi = k
			} else {
				constant = false
			}
		}
		if constant {
			if lo.Cmp(hi) > 0 || hi.Cmp(big.NewInt(t.Len)) > 0 {
				c.d.Errorf(x.P, "E126", "slice [%s..%s] out of range for %s", lo, hi, t)
			}
			return st
		}
	}
	if x.Lo == nil && x.Hi == nil {
		return st
	}
	c.site()
	c.needCheck(x, "slice bounds")
	return st
}

// ---- error handling --------------------------------------------------------

// checkErrs verifies that errs may propagate out of the current function.
func (c *checker) checkErrs(errs []string, p diag.Pos) {
	if c.fn == nil {
		return
	}
	if !c.fn.Fallible() {
		fix := fmt.Sprintf("add `!%s` to %s's signature, or handle it with `catch`", strings.Join(errs, "|"), c.fn.Name)
		if c.fn.Main || c.fn.ISR {
			fix = "handle it with `catch` — entry points cannot propagate errors"
		}
		c.d.ErrorFix(p, "E109", fmt.Sprintf("%s declares no errors, cannot propagate %s", c.fn.Name, strings.Join(errs, "|")), fix)
		return
	}
	have := map[string]bool{}
	for _, e := range c.fn.Errs {
		have[e] = true
	}
	var missing []string
	for _, e := range errs {
		if !have[e] {
			missing = append(missing, e)
		}
	}
	if len(missing) > 0 {
		all := append(append([]string{}, c.fn.Errs...), missing...)
		c.d.ErrorFix(p, "E108", fmt.Sprintf("%s does not declare %s", c.fn.Name, strings.Join(missing, "|")),
			fmt.Sprintf("change %s's error set to `!%s`", c.fn.Name, strings.Join(all, "|")))
	}
}

func (c *checker) catch(x *ast.Catch) *types.Type {
	t := c.expr(x.X, nil)
	if t.Kind == types.Invalid {
		if x.Body != nil {
			c.handlerBlock(x)
		}
		return t
	}
	if t.Kind != types.Union {
		c.d.ErrorFix(x.P, "E104", exprString(x.X)+" cannot fail", "drop the `catch`")
		if x.Body != nil {
			c.handlerBlock(x)
		}
		return types.InvalidT
	}
	if c.shortCirc > 0 {
		c.d.ErrorFix(x.P, "E119", "`catch` on the right of && or ||", "bind the call to a variable first")
	}
	switch {
	case x.Default != nil:
		if t.Elem.Kind == types.Void {
			c.d.ErrorFix(x.P, "E104", exprString(x.X)+" returns no value to default", "use the statement form: `catch` or `catch e` followed by an indented block")
			c.expr(x.Default, nil)
			return types.VoidT
		}
		c.value(x.Default, t.Elem)
		c.coerce(x.Default, t.Elem, "catch default")
		return t.Elem
	case x.Jump != nil:
		c.stmt(x.Jump)
		return t.Elem
	case x.Body != nil:
		if !c.blockCatchOK {
			c.d.ErrorFix(x.P, "E104", "a catch handler block produces no value", "use `catch <default>` or `catch return`")
		}
		c.handlerBlock(x)
		return types.VoidT
	}
	return t.Elem
}

func (c *checker) handlerBlock(x *ast.Catch) {
	c.push()
	if x.ErrName != "" {
		x.ErrSym = c.declareLocal(x.ErrName, x.P, types.ErrorValT)
		if x.ErrSym != nil {
			x.ErrSym.Immutable = true
		}
	}
	c.block(x.Body)
	c.pop()
}

// ---- literals --------------------------------------------------------------

func (c *checker) structLit(x *ast.StructLit) *types.Type {
	sym := c.cur.lookup(x.TypeName)
	if sym == nil || sym.Kind != types.SymType || sym.Type.Kind != types.Struct {
		c.d.Errorf(x.P, "E124", "`%s` is not a struct type", x.TypeName)
		for _, f := range x.Fields {
			c.expr(f.Value, nil)
		}
		return types.InvalidT
	}
	c.completeType(sym)
	t := sym.Type
	seen := map[string]bool{}
	for _, fi := range x.Fields {
		var ft *types.Type
		for _, f := range t.Fields {
			if f.Name == fi.Name {
				ft = f.Type
			}
		}
		if ft == nil {
			c.d.Errorf(fi.P, "E112", "struct %s has no field `%s`", t.Name, fi.Name)
			c.expr(fi.Value, nil)
			continue
		}
		if seen[fi.Name] {
			c.d.Errorf(fi.P, "E106", "field `%s` given twice", fi.Name)
		}
		seen[fi.Name] = true
		c.value(fi.Value, ft)
		c.coerce(fi.Value, ft, "field "+fi.Name)
	}
	return t
}

func (c *checker) arrayLit(x *ast.ArrayLit, want *types.Type) *types.Type {
	var elem *types.Type
	if want != nil && want.Kind == types.Array {
		elem = want.Elem
		if int64(len(x.Elems)) != want.Len {
			c.d.Errorf(x.P, "E104", "array literal has %d elements, want %s", len(x.Elems), want)
			for _, el := range x.Elems {
				c.expr(el, want.Elem)
			}
			return types.InvalidT
		}
	} else if want != nil && want.Kind == types.Invalid {
		elem = types.InvalidT
	}
	if len(x.Elems) == 0 {
		c.d.ErrorFix(x.P, "E104", "empty array literal", "declare `var a: [N]T` for a zeroed array")
		return types.InvalidT
	}
	for i, el := range x.Elems {
		t := c.value(el, elem)
		if i == 0 && elem == nil {
			if t.Kind == types.UntypedInt {
				t = types.I32
			}
			elem = t
		}
		c.coerce(el, elem, "array element")
	}
	if elem.Kind == types.Invalid {
		return types.InvalidT
	}
	return &types.Type{Kind: types.Array, Len: int64(len(x.Elems)), Elem: elem}
}

// exprString renders a short form of an expression for messages.
func exprString(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.IntLit:
		return x.Text
	case *ast.BoolLit:
		if x.Val {
			return "true"
		}
		return "false"
	case *ast.Field:
		return exprString(x.X) + "." + x.Name
	case *ast.Call:
		return exprString(x.Fun) + "(...)"
	case *ast.Index:
		return exprString(x.X) + "[" + exprString(x.I) + "]"
	case *ast.Paren:
		return "(" + exprString(x.X) + ")"
	case *ast.EnumLit:
		return "." + x.Name
	case *ast.Unary:
		return x.Op + exprString(x.X)
	case *ast.Binary:
		return exprString(x.X) + " " + x.Op + " " + exprString(x.Y)
	case *ast.Try:
		return exprString(x.X) + "?"
	}
	return "expression"
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
