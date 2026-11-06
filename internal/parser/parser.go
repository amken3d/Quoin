// Package parser builds an ast.File from Quoin tokens.
package parser

import (
	"fmt"

	"github.com/amken3d/quoin/internal/ast"
	"github.com/amken3d/quoin/internal/diag"
	"github.com/amken3d/quoin/internal/lexer"
)

type parser struct {
	toks  []lexer.Token
	i     int
	diags *diag.List
}

type bailout struct{}

// Parse lexes and parses one file. Syntax errors go to diags; the parser
// recovers at the next top-level declaration.
func Parse(file string, src []byte, diags *diag.List) *ast.File {
	p := &parser{toks: lexer.Lex(file, src, diags), diags: diags}
	f := &ast.File{Name: file}
	for p.tok().Kind != lexer.EOF {
		k := p.tok().Kind
		if k == lexer.Newline || k == lexer.Dedent {
			p.next()
			continue
		}
		if k == lexer.Indent {
			p.diags.ErrorFix(p.tok().Pos, "E002", "unexpected indent at top level", "start top-level declarations in column 1")
			p.sync()
			continue
		}
		start := p.i
		func() {
			defer func() {
				if r := recover(); r != nil {
					if _, ok := r.(bailout); !ok {
						panic(r)
					}
					if p.i == start {
						p.next()
					}
					p.sync()
				}
			}()
			f.Decls = append(f.Decls, p.decl())
		}()
	}
	return f
}

// sync skips to the next token that starts a line in column 1.
func (p *parser) sync() {
	for p.tok().Kind != lexer.EOF {
		t := p.tok()
		if t.LineStart && t.Pos.Col == 1 && t.Kind != lexer.Dedent && t.Kind != lexer.Newline {
			return
		}
		p.next()
	}
}

// ---- token helpers ---------------------------------------------------------

func (p *parser) tok() lexer.Token { return p.toks[p.i] }

func (p *parser) peekN(n int) lexer.Token {
	if p.i+n < len(p.toks) {
		return p.toks[p.i+n]
	}
	return p.toks[len(p.toks)-1]
}

func (p *parser) next() lexer.Token {
	t := p.toks[p.i]
	if p.i < len(p.toks)-1 {
		p.i++
	}
	return t
}

func (p *parser) isOp(s string) bool {
	t := p.tok()
	return t.Kind == lexer.Op && t.Text == s
}

func (p *parser) isKw(s string) bool {
	t := p.tok()
	return t.Kind == lexer.Keyword && t.Text == s
}

func (p *parser) fail(pos diag.Pos, msg, fix string) {
	last := p.toks[len(p.toks)-1]
	atEnd := true
	for _, t := range p.toks[p.i:] {
		if t.Kind != lexer.Newline && t.Kind != lexer.Dedent && t.Kind != lexer.EOF {
			atEnd = false
			break
		}
	}
	if !(atEnd && last.Text == "unclosed") {
		p.diags.ErrorFix(pos, "E003", msg, fix)
	}
	panic(bailout{})
}

func (p *parser) failHere(want string) {
	t := p.tok()
	fix := ""
	if want == "newline" && t.Kind != lexer.EOF {
		fix = "end the statement before " + t.String()
	}
	p.fail(t.Pos, fmt.Sprintf("expected %s, got %s", want, t), fix)
}

func (p *parser) expectOp(s string) diag.Pos {
	if !p.isOp(s) {
		p.failHere("`" + s + "`")
	}
	return p.next().Pos
}

func (p *parser) expectKw(s string) diag.Pos {
	if !p.isKw(s) {
		p.failHere("`" + s + "`")
	}
	return p.next().Pos
}

func (p *parser) expectIdent() (string, diag.Pos) {
	t := p.tok()
	if t.Kind != lexer.Ident {
		if t.Kind == lexer.Keyword {
			p.fail(t.Pos, fmt.Sprintf("expected name, got keyword `%s`", t.Text), "rename it; `"+t.Text+"` is reserved")
		}
		p.failHere("name")
	}
	p.next()
	return t.Text, t.Pos
}

func (p *parser) expectNewline() {
	if p.tok().Kind == lexer.EOF {
		return
	}
	if p.tok().Kind != lexer.Newline {
		p.failHere("newline")
	}
	p.next()
}

// ---- declarations ----------------------------------------------------------

func (p *parser) decl() ast.Decl {
	t := p.tok()
	if t.Kind != lexer.Keyword {
		p.fail(t.Pos, fmt.Sprintf("expected declaration, got %s", t),
			"start with fn, isr fn, extern fn, const, var, error, enum, struct, regs or periph")
	}
	switch t.Text {
	case "const":
		p.next()
		d := &ast.ConstDecl{P: t.Pos}
		d.Name, _ = p.expectIdent()
		if p.isOp(":") {
			p.next()
			d.Type = p.typ()
		}
		p.expectOp("=")
		d.Value = p.expr()
		p.expectNewline()
		return d
	case "var":
		p.next()
		d := &ast.VarDecl{P: t.Pos}
		d.Name, _ = p.expectIdent()
		p.expectOp(":")
		d.Type = p.typ()
		if p.isOp("=") {
			p.next()
			d.Value = p.expr()
		}
		p.expectNewline()
		return d
	case "error":
		p.next()
		d := &ast.ErrorDecl{P: t.Pos}
		for {
			name, pos := p.expectIdent()
			id := ast.Ident{Name: name}
			id.P = pos
			d.Names = append(d.Names, id)
			if !p.isOp(",") {
				break
			}
			p.next()
		}
		p.expectNewline()
		return d
	case "enum":
		p.next()
		d := &ast.EnumDecl{P: t.Pos}
		d.Name, _ = p.expectIdent()
		p.expectOp(":")
		d.Base = p.typ()
		p.indented(func() {
			name, pos := p.expectIdent()
			m := ast.EnumMemberDecl{P: pos, Name: name}
			if p.isOp("=") {
				p.next()
				m.Value = p.expr()
			}
			p.expectNewline()
			d.Members = append(d.Members, m)
		})
		return d
	case "struct":
		p.next()
		d := &ast.StructDecl{P: t.Pos}
		d.Name, _ = p.expectIdent()
		p.indented(func() {
			name, pos := p.expectIdent()
			p.expectOp(":")
			d.Fields = append(d.Fields, ast.FieldDecl{P: pos, Name: name, Type: p.typ()})
			p.expectNewline()
		})
		return d
	case "regs":
		p.next()
		d := &ast.RegsDecl{P: t.Pos}
		d.Name, _ = p.expectIdent()
		p.indented(func() {
			r := ast.RegDecl{FieldDecl: p.atField()}
			if p.tok().Kind == lexer.Indent {
				p.next()
				for p.tok().Kind != lexer.Dedent && p.tok().Kind != lexer.EOF {
					r.Bits = append(r.Bits, p.atField())
				}
				p.next()
			}
			d.Regs = append(d.Regs, r)
		})
		return d
	case "periph":
		p.next()
		d := &ast.PeriphDecl{P: t.Pos}
		d.Name, _ = p.expectIdent()
		p.expectOp(":")
		d.Type, d.TP = p.expectIdent()
		p.expectOp("@")
		d.Addr = p.expr()
		p.expectNewline()
		return d
	case "isr":
		p.next()
		if !p.isKw("fn") {
			p.failHere("`fn` after `isr`")
		}
		d := p.fnDecl()
		d.P = t.Pos
		d.ISR = true
		return d
	case "fn":
		return p.fnDecl()
	case "extern":
		p.next()
		if !p.isKw("fn") {
			p.failHere("`fn` after `extern`")
		}
		d := p.fnDecl()
		d.P = t.Pos
		d.Extern = true
		return d
	}
	p.fail(t.Pos, fmt.Sprintf("expected declaration, got %s", t), "")
	return nil
}

// atField parses `NAME: type @ expr NEWLINE`.
func (p *parser) atField() ast.FieldDecl {
	name, pos := p.expectIdent()
	p.expectOp(":")
	f := ast.FieldDecl{P: pos, Name: name, Type: p.typ()}
	p.expectOp("@")
	f.At = p.expr()
	p.expectNewline()
	return f
}

// indented parses NEWLINE INDENT item* DEDENT, calling item for each line.
func (p *parser) indented(item func()) {
	p.expectNewline()
	if p.tok().Kind != lexer.Indent {
		p.failHere("an indented block")
	}
	p.next()
	for p.tok().Kind != lexer.Dedent && p.tok().Kind != lexer.EOF {
		item()
	}
	p.next()
}

func (p *parser) fnDecl() *ast.FnDecl {
	pos := p.expectKw("fn")
	d := &ast.FnDecl{P: pos}
	d.Name, _ = p.expectIdent()
	p.expectOp("(")
	for !p.isOp(")") {
		name, ppos := p.expectIdent()
		p.expectOp(":")
		d.Params = append(d.Params, ast.Param{P: ppos, Name: name, Type: p.typ()})
		if !p.isOp(",") {
			break
		}
		p.next()
	}
	p.expectOp(")")
	if p.startsType() {
		d.Ret = p.typ()
	}
	if p.isOp("!") {
		p.next()
		d.Bang = true
		d.Errs = []ast.Ident{}
		for {
			name, epos := p.expectIdent()
			id := ast.Ident{Name: name}
			id.P = epos
			d.Errs = append(d.Errs, id)
			if !p.isOp("|") {
				break
			}
			p.next()
		}
	}
	if p.isKw("where") {
		p.next()
		for {
			d.Where = append(d.Where, p.expr())
			if !p.isOp(",") {
				break
			}
			p.next()
		}
	}
	if p.isKw("stack") {
		p.next()
		d.Stack = p.expr()
	}
	if p.isKw("extern") {
		p.failHere("newline")
	}
	if p.toks[p.i].Kind == lexer.Newline && p.peekN(1).Kind == lexer.Indent {
		d.Body = p.block()
	} else {
		p.expectNewline()
	}
	return d
}

func (p *parser) startsType() bool {
	t := p.tok()
	return t.Kind == lexer.Ident || (t.Kind == lexer.Op && t.Text == "[") ||
		(t.Kind == lexer.Keyword && (t.Text == "grow" || t.Text == "queue"))
}

func (p *parser) typ() ast.TypeExpr {
	t := p.tok()
	switch {
	case t.Kind == lexer.Ident:
		p.next()
		return &ast.NamedType{P: t.Pos, Name: t.Text}
	case t.Kind == lexer.Op && t.Text == "[":
		p.next()
		if p.isOp("]") {
			p.next()
			return &ast.SliceType{P: t.Pos, Elem: p.typ()}
		}
		n := p.expr()
		p.expectOp("]")
		return &ast.ArrayType{P: t.Pos, Len: n, Elem: p.typ()}
	case t.Kind == lexer.Keyword && (t.Text == "grow" || t.Text == "queue"):
		p.next()
		p.expectOp("[")
		n := p.expr()
		p.expectOp("]")
		elem := p.typ()
		if t.Text == "grow" {
			return &ast.GrowType{P: t.Pos, Cap: n, Elem: elem}
		}
		return &ast.QueueType{P: t.Pos, Cap: n, Elem: elem}
	}
	p.failHere("type")
	return nil
}

// ---- statements ------------------------------------------------------------

// block parses NEWLINE INDENT stmt* DEDENT.
func (p *parser) block() *ast.Block {
	if p.tok().Kind == lexer.Newline {
		p.next()
	} else {
		p.failHere("newline")
	}
	if p.tok().Kind != lexer.Indent {
		p.failHere("an indented block")
	}
	b := &ast.Block{P: p.next().Pos}
	for p.tok().Kind != lexer.Dedent && p.tok().Kind != lexer.EOF {
		if p.tok().Kind == lexer.Newline {
			p.next()
			continue
		}
		b.Stmts = append(b.Stmts, p.stmt())
	}
	p.next()
	return b
}

func (p *parser) stmt() ast.Stmt {
	t := p.tok()
	if t.Kind == lexer.Keyword {
		switch t.Text {
		case "var":
			p.next()
			s := &ast.VarStmt{P: t.Pos}
			s.Name, _ = p.expectIdent()
			p.expectOp(":")
			s.Type = p.typ()
			if p.isOp("=") {
				p.next()
				s.Value = p.expr()
			}
			p.expectNewline()
			return s
		case "return":
			p.next()
			s := &ast.ReturnStmt{P: t.Pos}
			if p.tok().Kind != lexer.Newline && p.tok().Kind != lexer.EOF && p.tok().Kind != lexer.Dedent {
				s.Value = p.expr()
			}
			p.expectNewline()
			return s
		case "if":
			return p.ifStmt()
		case "for":
			return p.forStmt()
		case "break", "continue":
			p.next()
			p.expectNewline()
			return &ast.BranchStmt{P: t.Pos, Tok: t.Text}
		}
	}
	x := p.expr()
	if id, ok := x.(*ast.Ident); ok && p.isOp(":") {
		p.fail(p.tok().Pos, "typed locals are declared with var", "write `var "+id.Name+": <type> = <value>`")
	}
	if p.isOp(":=") {
		id, ok := x.(*ast.Ident)
		if !ok {
			p.fail(p.tok().Pos, "`:=` needs a plain name on the left", "use `=` to assign to an existing place")
		}
		p.next()
		s := &ast.DefineStmt{P: id.P, Name: id.Name, Value: p.expr()}
		p.noBlockCatch(s.Value)
		p.expectNewline()
		return s
	}
	if t := p.tok(); t.Kind == lexer.Op && isAssignOp(t.Text) {
		p.next()
		s := &ast.AssignStmt{P: t.Pos, Op: t.Text, LHS: x, RHS: p.expr()}
		p.noBlockCatch(s.RHS)
		p.expectNewline()
		return s
	}
	if c, ok := x.(*ast.Catch); ok && c.Body != nil {
		return &ast.ExprStmt{P: x.EPos(), X: x}
	}
	p.expectNewline()
	return &ast.ExprStmt{P: x.EPos(), X: x}
}

// noBlockCatch rejects `x := f() catch e` followed by a handler block: the
// block form handles an error but produces no value.
func (p *parser) noBlockCatch(x ast.Expr) {
	if c, ok := x.(*ast.Catch); ok && c.Body != nil {
		p.fail(c.P, "a catch handler block produces no value",
			"use `catch <default>`, `catch break` or `catch return`, or make the call a statement")
	}
}

func isAssignOp(s string) bool {
	switch s {
	case "=", "+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", "<<=", ">>=":
		return true
	}
	return false
}

func (p *parser) ifStmt() *ast.IfStmt {
	pos := p.expectKw("if")
	s := &ast.IfStmt{P: pos, Cond: p.expr()}
	s.Then = p.block()
	if p.isKw("else") {
		p.next()
		if p.isKw("if") {
			s.Else = p.ifStmt()
		} else {
			s.Else = p.block()
		}
	}
	return s
}

func (p *parser) forStmt() *ast.ForStmt {
	pos := p.expectKw("for")
	s := &ast.ForStmt{P: pos}
	if p.tok().Kind == lexer.Newline {
		s.Kind = ast.ForEver
		s.Body = p.block()
		return s
	}
	isIn := func(n int) bool {
		t := p.peekN(n)
		return t.Kind == lexer.Keyword && t.Text == "in"
	}
	if p.tok().Kind == lexer.Ident && (isIn(1) ||
		(p.peekN(1).Kind == lexer.Op && p.peekN(1).Text == "," && p.peekN(2).Kind == lexer.Ident && isIn(3))) {
		first, _ := p.expectIdent()
		if p.isOp(",") {
			p.next()
			s.Index = first
			s.Var, _ = p.expectIdent()
		} else {
			s.Var = first
		}
		p.expectKw("in")
		x := p.expr()
		if p.isOp("..") {
			if s.Index != "" {
				p.fail(pos, "a range loop has one variable", "write `for "+s.Var+" in lo..hi`")
			}
			p.next()
			s.Kind = ast.ForRange
			s.Lo = x
			s.Hi = p.expr()
		} else {
			s.Kind = ast.ForEach
			s.Iter = x
		}
		s.Body = p.block()
		return s
	}
	s.Kind = ast.ForCond
	s.Cond = p.expr()
	s.Body = p.block()
	return s
}

// ---- expressions -----------------------------------------------------------

var binPrec = map[string]int{
	"||": 1,
	"&&": 2,
	"==": 3, "!=": 3, "<": 3, "<=": 3, ">": 3, ">=": 3,
	"+": 4, "-": 4, "|": 4, "^": 4, "+%": 4, "-%": 4, "+|": 4, "-|": 4,
	"*": 5, "/": 5, "%": 5, "&": 5, "<<": 5, ">>": 5, "*%": 5, "*|": 5,
}

func (p *parser) expr() ast.Expr { return p.binary(1) }

func (p *parser) binary(minPrec int) ast.Expr {
	x := p.unary()
	for {
		t := p.tok()
		if t.Kind != lexer.Op {
			return x
		}
		prec, ok := binPrec[t.Text]
		if !ok || prec < minPrec {
			return x
		}
		p.next()
		y := p.binary(prec + 1)
		b := &ast.Binary{Op: t.Text, X: x, Y: y}
		b.P = t.Pos
		x = b
		if prec == 3 {
			if n := p.tok(); n.Kind == lexer.Op && binPrec[n.Text] == 3 {
				p.fail(n.Pos, "comparisons do not chain", "join them with `&&`")
			}
		}
	}
}

func (p *parser) unary() ast.Expr {
	t := p.tok()
	if t.Kind == lexer.Op && (t.Text == "-" || t.Text == "!" || t.Text == "~") {
		p.next()
		u := &ast.Unary{Op: t.Text, X: p.unary()}
		u.P = t.Pos
		return u
	}
	return p.postfix()
}

func (p *parser) postfix() ast.Expr {
	x := p.primary()
	for {
		t := p.tok()
		switch {
		case t.Kind == lexer.Op && t.Text == "(":
			p.next()
			c := &ast.Call{Fun: x}
			c.P = x.EPos()
			for !p.isOp(")") {
				c.Args = append(c.Args, p.expr())
				if !p.isOp(",") {
					break
				}
				p.next()
			}
			p.expectOp(")")
			x = c
		case t.Kind == lexer.Op && t.Text == "[":
			p.next()
			var lo ast.Expr
			if !p.isOp("..") {
				lo = p.expr()
			}
			if p.isOp("..") {
				p.next()
				s := &ast.SliceExpr{X: x, Lo: lo}
				s.P = t.Pos
				if !p.isOp("]") {
					s.Hi = p.expr()
				}
				p.expectOp("]")
				x = s
			} else {
				p.expectOp("]")
				ix := &ast.Index{X: x, I: lo}
				ix.P = t.Pos
				x = ix
			}
		case t.Kind == lexer.Op && t.Text == ".":
			p.next()
			name, _ := p.expectIdent()
			f := &ast.Field{X: x, Name: name}
			f.P = t.Pos
			x = f
		case t.Kind == lexer.Op && t.Text == "?":
			p.next()
			q := &ast.Try{X: x}
			q.P = t.Pos
			x = q
		case t.Kind == lexer.Keyword && t.Text == "catch":
			return p.catch(x)
		default:
			return x
		}
	}
}

func (p *parser) catch(x ast.Expr) ast.Expr {
	t := p.next() // catch
	c := &ast.Catch{X: x}
	c.P = t.Pos
	switch {
	case p.tok().Kind == lexer.Newline && p.peekN(1).Kind == lexer.Indent:
		c.Body = p.block()
	case p.tok().Kind == lexer.Ident && p.peekN(1).Kind == lexer.Newline && p.peekN(2).Kind == lexer.Indent:
		c.ErrName = p.next().Text
		c.Body = p.block()
	case p.isKw("break") || p.isKw("continue"):
		b := p.next()
		c.Jump = &ast.BranchStmt{P: b.Pos, Tok: b.Text}
	case p.isKw("return"):
		r := &ast.ReturnStmt{P: p.next().Pos}
		if k := p.tok().Kind; k != lexer.Newline && k != lexer.EOF && k != lexer.Dedent && !(k == lexer.Op && p.tok().Text == ")") {
			r.Value = p.expr()
		}
		c.Jump = r
	default:
		c.Default = p.unary()
	}
	return c
}

func (p *parser) primary() ast.Expr {
	t := p.tok()
	switch t.Kind {
	case lexer.Int:
		p.next()
		e := &ast.IntLit{Val: t.Val, Text: t.Text}
		e.P = t.Pos
		return e
	case lexer.Ident:
		p.next()
		if p.isOp("{") {
			return p.structLit(t)
		}
		e := &ast.Ident{Name: t.Text}
		e.P = t.Pos
		return e
	case lexer.Keyword:
		if t.Text == "true" || t.Text == "false" {
			p.next()
			e := &ast.BoolLit{Val: t.Text == "true"}
			e.P = t.Pos
			return e
		}
	case lexer.Op:
		switch t.Text {
		case "(":
			p.next()
			e := &ast.Paren{X: p.expr()}
			e.P = t.Pos
			p.expectOp(")")
			return e
		case ".":
			p.next()
			name, _ := p.expectIdent()
			e := &ast.EnumLit{Name: name}
			e.P = t.Pos
			return e
		case "[":
			p.next()
			e := &ast.ArrayLit{}
			e.P = t.Pos
			for !p.isOp("]") {
				e.Elems = append(e.Elems, p.expr())
				if !p.isOp(",") {
					break
				}
				p.next()
			}
			p.expectOp("]")
			return e
		}
	}
	p.fail(t.Pos, fmt.Sprintf("expected expression, got %s", t), "")
	return nil
}

func (p *parser) structLit(name lexer.Token) ast.Expr {
	p.expectOp("{")
	e := &ast.StructLit{TypeName: name.Text}
	e.P = name.Pos
	for !p.isOp("}") {
		fname, fpos := p.expectIdent()
		p.expectOp(":")
		e.Fields = append(e.Fields, ast.FieldInit{P: fpos, Name: fname, Value: p.expr()})
		if !p.isOp(",") {
			break
		}
		p.next()
	}
	p.expectOp("}")
	return e
}
