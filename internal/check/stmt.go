package check

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/amken3d/quoin/internal/ast"
	"github.com/amken3d/quoin/internal/diag"
	"github.com/amken3d/quoin/internal/types"
)

func (c *checker) push() { c.cur = &scope{parent: c.cur, syms: map[string]*types.Symbol{}} }
func (c *checker) pop()  { c.cur = c.cur.parent }

// declareLocal adds a local. Quoin has no shadowing: a name visible from an
// enclosing scope (including globals) cannot be redeclared.
func (c *checker) declareLocal(name string, pos diag.Pos, t *types.Type) *types.Symbol {
	if prev := c.cur.lookup(name); prev != nil {
		where := "a builtin"
		if prev.Pos.Line > 0 {
			where = prev.Pos.String()
		}
		c.d.ErrorFix(pos, "E106", fmt.Sprintf("`%s` already declared at %s", name, where), "pick a new name; Quoin has no shadowing")
		return nil
	}
	if builtinType(name) != nil {
		c.d.ErrorFix(pos, "E106", fmt.Sprintf("`%s` is a builtin type name", name), "pick a new name")
		return nil
	}
	sym := &types.Symbol{Kind: types.SymLocal, Name: name, CName: CName(name), Type: t, Pos: pos}
	c.cur.syms[name] = sym
	c.frame += slot(t)
	return sym
}

// slot estimates the C stack bytes a local of type t occupies.
func slot(t *types.Type) int64 {
	switch t.Kind {
	case types.Slice:
		return 8
	case types.Grow, types.Regs, types.Invalid:
		return 4
	}
	s, _ := t.Layout()
	return types.AlignUp(max(s, 4), 4)
}

func (c *checker) body(fd *ast.FnDecl) {
	fn := fd.Fn
	c.fn = fn
	c.cur = &scope{parent: c.globals, syms: map[string]*types.Symbol{}}
	c.dataOff, c.dataHigh, c.frame = 0, 0, 0
	for _, p := range fn.Params {
		if prev := c.globals.lookup(p.Name); prev != nil {
			c.d.ErrorFix(p.Pos, "E106", fmt.Sprintf("parameter `%s` shadows the global declared at %s", p.Name, prev.Pos), "rename the parameter")
			continue
		}
		c.cur.syms[p.Name] = p
		c.frame += slot(p.Type)
	}
	for _, w := range fd.Where {
		t := c.value(w, types.BoolT)
		if t.Kind != types.Invalid && t.Kind != types.Bool {
			c.d.Errorf(w.EPos(), "E104", "where clause must be bool, got %s", t)
			continue
		}
		c.needCheck(w, "where clause")
	}
	c.stmts(fd.Body.Stmts)
	if fn.Ret.Kind != types.Void && fn.Ret.Kind != types.Invalid && !terminates(fd.Body) {
		pos := fd.P
		if n := len(fd.Body.Stmts); n > 0 {
			pos = fd.Body.Stmts[n-1].SPos()
		}
		c.d.ErrorFix(pos, "E111", fmt.Sprintf("%s can reach its end without returning %s", fn.Name, fn.Ret), "add `return <value>` at the end")
	}
	fn.DataFrame = types.AlignUp(c.dataHigh, 8)
	// Saved registers and LR are approximated as 8 bytes; gcc -fstack-usage
	// replaces this estimate with the real frame size (quoinc stack --su).
	fn.FrameEst = types.AlignUp(8+c.frame, 8)
	c.fn = nil
	c.cur = c.globals
}

func (c *checker) block(b *ast.Block) {
	c.push()
	saved := c.dataOff
	c.stmts(b.Stmts)
	c.dataOff = saved
	c.pop()
}

func (c *checker) stmts(ss []ast.Stmt) {
	for _, s := range ss {
		c.stmt(s)
	}
}

func (c *checker) stmt(s ast.Stmt) {
	switch s := s.(type) {
	case *ast.Block:
		c.block(s)
	case *ast.DefineStmt:
		t := c.value(s.Value, nil)
		switch t.Kind {
		case types.UntypedInt:
			c.coerce(s.Value, types.I32, "definition")
			t = types.I32
		case types.Void:
			c.d.ErrorFix(s.Value.EPos(), "E104", exprString(s.Value)+" returns no value", "call it as a statement")
			t = types.InvalidT
		case types.Grow, types.Queue:
			c.d.ErrorFix(s.Value.EPos(), "E123", t.String()+" cannot be copied", "pass it to a grow parameter, or take a slice: x[..]")
			t = types.InvalidT
		}
		s.Sym = c.declareLocal(s.Name, s.P, t)
		if s.Sym != nil && (t.Kind == types.Slice || t.Kind == types.Regs) {
			s.Sym.Immutable = true
		}
	case *ast.VarStmt:
		t := c.typ(s.Type)
		switch t.Kind {
		case types.Slice:
			c.d.ErrorFix(s.P, "E118", "slices cannot be declared with var", "bind a view with `"+s.Name+" := arr[lo..hi]`")
			t = types.InvalidT
		case types.Regs:
			c.d.ErrorFix(s.P, "E104", "register blocks cannot be declared with var", "bind with `"+s.Name+" := PERIPH`")
			t = types.InvalidT
		case types.Queue:
			c.d.ErrorFix(s.P, "E122", "queues are global", "declare it at top level")
			t = types.InvalidT
		}
		if s.Value != nil {
			if t.Kind == types.Grow {
				c.d.Errorf(s.Value.EPos(), "E104", "%s starts empty and takes no initializer", t)
			} else {
				c.value(s.Value, t)
				c.coerce(s.Value, t, "initializer of "+s.Name)
			}
		}
		s.Sym = c.declareLocal(s.Name, s.P, t)
		if s.Sym != nil && t.Kind == types.Grow {
			size, align := t.Layout()
			c.dataOff = types.AlignUp(c.dataOff, align)
			s.Sym.OnDataStack = true
			s.Sym.DataOff = c.dataOff
			c.dataOff += size
			c.dataHigh = max(c.dataHigh, c.dataOff)
		}
	case *ast.AssignStmt:
		c.assign(s)
	case *ast.ExprStmt:
		switch x := s.X.(type) {
		case *ast.Call, *ast.Try:
			if t := c.expr(x, nil); t.Kind == types.Union {
				c.unhandled(x, t)
			}
		case *ast.Catch:
			c.blockCatchOK = true
			c.expr(x, nil)
			c.blockCatchOK = false
		default:
			c.expr(x, nil)
			c.d.ErrorFix(s.P, "E125", exprString(x)+" has no effect", "assign it, or remove it")
		}
	case *ast.ReturnStmt:
		c.ret(s)
	case *ast.IfStmt:
		c.boolOperand(s.Cond)
		c.block(s.Then)
		if s.Else != nil {
			c.stmt(s.Else)
		}
	case *ast.ForStmt:
		c.forStmt(s)
	case *ast.BranchStmt:
		if c.loop == 0 {
			c.d.Errorf(s.P, "E116", "`%s` outside a loop", s.Tok)
		}
	}
}

func (c *checker) ret(s *ast.ReturnStmt) {
	fn := c.fn
	if s.Value != nil {
		if id, ok := s.Value.(*ast.Ident); ok {
			if sym := c.cur.lookup(id.Name); sym != nil && sym.Kind == types.SymErr {
				id.Sym = sym
				id.I.Type = types.ErrorValT
				s.ErrTag = sym.Name
				c.checkErrs([]string{sym.Name}, s.P)
				return
			}
		}
		if fn.Ret.Kind == types.Void {
			c.d.ErrorFix(s.P, "E104", fn.Name+" returns nothing", "drop the value, or add a result type to "+fn.Name)
			c.expr(s.Value, nil)
			return
		}
		c.value(s.Value, fn.Ret)
		c.coerce(s.Value, fn.Ret, "return value")
		return
	}
	if fn.Ret.Kind != types.Void && fn.Ret.Kind != types.Invalid {
		c.d.ErrorFix(s.P, "E104", fmt.Sprintf("missing return value; %s returns %s", fn.Name, fn.Ret), "write `return <value>`")
	}
}

func (c *checker) forStmt(s *ast.ForStmt) {
	c.loop++
	defer func() { c.loop-- }()
	c.push()
	defer c.pop()
	switch s.Kind {
	case ast.ForCond:
		c.boolOperand(s.Cond)
	case ast.ForRange:
		tl := c.value(s.Lo, nil)
		th := c.value(s.Hi, tl)
		t := c.unify(s.Lo, s.Lo, s.Hi, tl, th)
		if t.Kind == types.UntypedInt {
			c.coerce(s.Lo, types.Usize, "range start")
			c.coerce(s.Hi, types.Usize, "range end")
			t = types.Usize
		}
		if t.Kind != types.Int && t.Kind != types.Invalid {
			c.d.Errorf(s.Lo.EPos(), "E104", "range bounds must be integers, got %s", t)
			t = types.InvalidT
		}
		s.VarSym = c.declareLocal(s.Var, s.P, t)
		if s.VarSym != nil {
			s.VarSym.Immutable = true
			lo, _ := rng(s.Lo)
			_, hi := rng(s.Hi)
			if lo != nil && hi != nil {
				s.VarSym.Lo, s.VarSym.Hi = lo, new(big.Int).Sub(hi, big.NewInt(1))
			}
		}
	case ast.ForEach:
		t := c.value(s.Iter, nil)
		elem := types.InvalidT
		var hi *big.Int
		switch t.Kind {
		case types.Array:
			elem, hi = t.Elem, big.NewInt(t.Len-1)
		case types.Grow:
			elem, hi = t.Elem, big.NewInt(t.Len-1)
		case types.Slice:
			elem = t.Elem
		case types.Invalid:
		default:
			c.d.Errorf(s.Iter.EPos(), "E113", "cannot iterate over %s", t)
		}
		if s.Index != "" {
			s.IndexSym = c.declareLocal(s.Index, s.P, types.Usize)
			if s.IndexSym != nil {
				s.IndexSym.Immutable = true
				if hi != nil {
					s.IndexSym.Lo, s.IndexSym.Hi = big.NewInt(0), hi
				}
			}
		}
		s.VarSym = c.declareLocal(s.Var, s.P, elem)
		if s.VarSym != nil {
			s.VarSym.Immutable = true
		}
	}
	c.block(s.Body)
}

func (c *checker) assign(s *ast.AssignStmt) {
	lt := c.value(s.LHS, nil)
	if lt.Kind == types.Invalid {
		c.expr(s.RHS, nil)
		return
	}
	if !c.lvalue(s.LHS) {
		c.expr(s.RHS, nil)
		return
	}
	switch lt.Kind {
	case types.Grow, types.Queue, types.Regs:
		c.d.ErrorFix(s.P, "E123", lt.String()+" cannot be assigned", "push elements, or clear() and refill")
		return
	}
	if s.Op == "=" {
		c.value(s.RHS, lt)
		c.coerce(s.RHS, lt, "assignment")
		return
	}
	op := strings.TrimSuffix(s.Op, "=")
	if lt.Kind != types.Int {
		c.d.Errorf(s.P, "E113", "`%s` needs an integer target, got %s", s.Op, lt)
		c.expr(s.RHS, nil)
		return
	}
	if op == "<<" || op == ">>" {
		rt := c.value(s.RHS, nil)
		if rt.Kind == types.Invalid {
			return
		}
		if !rt.IsIntLike() || (rt.Kind == types.Int && rt.Signed) {
			c.d.ErrorFix(s.RHS.EPos(), "E104", "shift amount must be an unsigned integer", "convert it: u32(n)")
			return
		}
		if k := s.RHS.Info().Const; k != nil {
			if k.Sign() < 0 || k.Cmp(big.NewInt(int64(lt.Bits))) >= 0 {
				c.d.Errorf(s.RHS.EPos(), "E126", "shift by %s is out of range for %s", k, lt)
			}
			if rt.Kind == types.UntypedInt {
				c.coerce(s.RHS, types.U32, "shift amount")
			}
			return
		}
		if _, hi := rng(s.RHS); hi == nil || hi.Cmp(big.NewInt(int64(lt.Bits))) >= 0 {
			s.Check = true
			c.fn.Checks = append(c.fn.Checks, types.CheckSite{Pos: s.P, What: "shift amount"})
		}
		return
	}
	c.value(s.RHS, lt)
	if !c.coerce(s.RHS, lt, "operand of "+s.Op) {
		return
	}
	if isChecked(op) {
		need := true
		if op == "/" || op == "%" {
			ly, hy := rng(s.RHS)
			need = ly == nil || (ly.Sign() <= 0 && hy.Sign() >= 0) || lt.Signed
		}
		if need {
			s.Check = true
			c.fn.Checks = append(c.fn.Checks, types.CheckSite{Pos: s.P, What: "overflow " + s.Op})
		}
	}
}

// lvalue reports whether e may be assigned, recording global writes.
func (c *checker) lvalue(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.Paren:
		return c.lvalue(x.X)
	case *ast.Ident:
		sym := x.Sym
		if sym == nil {
			return false
		}
		switch sym.Kind {
		case types.SymLocal, types.SymParam:
			if sym.Immutable {
				switch sym.Type.Kind {
				case types.Slice:
					c.d.ErrorFix(x.P, "E118", fmt.Sprintf("slice `%s` cannot be rebound", x.Name), "declare a new slice with :=")
				case types.ErrorVal:
					c.d.Errorf(x.P, "E110", "error binding `%s` is read-only", x.Name)
				case types.Grow, types.Regs:
					return true // reported as NotCopyable by the caller
				default:
					c.d.ErrorFix(x.P, "E110", fmt.Sprintf("loop variable `%s` is read-only", x.Name), "copy it: `v := "+x.Name+"`")
				}
				return false
			}
			return true
		case types.SymGlobal:
			c.useGlobal(sym, x.P, true)
			return true
		}
		c.d.Errorf(x.P, "E110", "cannot assign to %s `%s`", kindName(sym.Kind), x.Name)
		return false
	case *ast.Field:
		switch x.Kind {
		case ast.FieldStruct:
			return c.lvalue(x.X)
		case ast.FieldReg, ast.FieldBits:
			return true
		}
		c.d.Errorf(x.P, "E110", "cannot assign to .%s", x.Name)
		return false
	case *ast.Index:
		t := x.X.Info().Type
		if t != nil && t.Kind == types.Slice {
			return true
		}
		return c.lvalue(x.X)
	}
	c.d.Errorf(e.EPos(), "E110", "cannot assign to %s", exprString(e))
	return false
}

func kindName(k types.SymKind) string {
	switch k {
	case types.SymConst:
		return "constant"
	case types.SymFn:
		return "function"
	case types.SymType:
		return "type"
	case types.SymErr:
		return "error"
	case types.SymPeriph:
		return "peripheral"
	}
	return "name"
}

// terminates reports whether control cannot fall off the end of s.
func terminates(s ast.Stmt) bool {
	switch x := s.(type) {
	case *ast.Block:
		return len(x.Stmts) > 0 && terminates(x.Stmts[len(x.Stmts)-1])
	case *ast.ReturnStmt:
		return true
	case *ast.IfStmt:
		return x.Else != nil && terminates(x.Then) && terminates(x.Else)
	case *ast.ForStmt:
		return x.Kind == ast.ForEver && !breaks(x.Body)
	}
	return false
}

// breaks reports whether b contains a break that targets the enclosing loop.
func breaks(b *ast.Block) bool {
	for _, s := range b.Stmts {
		if stmtBreaks(s) {
			return true
		}
	}
	return false
}

func stmtBreaks(s ast.Stmt) bool {
	switch x := s.(type) {
	case *ast.BranchStmt:
		return x.Tok == "break"
	case *ast.Block:
		return breaks(x)
	case *ast.IfStmt:
		if breaks(x.Then) {
			return true
		}
		if x.Else != nil && stmtBreaks(x.Else) {
			return true
		}
	case *ast.ForStmt:
		return false // a break inside targets the inner loop
	}
	found := false
	for _, e := range ast.StmtExprs(s) {
		ast.WalkExpr(e, func(e ast.Expr) bool {
			if ct, ok := e.(*ast.Catch); ok {
				if br, ok := ct.Jump.(*ast.BranchStmt); ok && br.Tok == "break" {
					found = true
				}
				if ct.Body != nil && breaks(ct.Body) {
					found = true
				}
			}
			return !found
		})
	}
	return found
}
