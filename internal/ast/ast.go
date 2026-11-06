// Package ast defines Quoin's syntax tree. The checker fills in the
// semantic fields (types, constant values, resolved symbols).
package ast

import (
	"math/big"

	"github.com/amken3d/quoin/internal/diag"
	"github.com/amken3d/quoin/internal/types"
)

// File is one parsed source file.
type File struct {
	Name  string
	Decls []Decl
}

// ---- Type expressions ------------------------------------------------------

// TypeExpr is a syntactic type.
type TypeExpr interface{ TPos() diag.Pos }

// NamedType is `u8`, `Point`, `Uart`.
type NamedType struct {
	P    diag.Pos
	Name string
}

// ArrayType is `[N]T`.
type ArrayType struct {
	P    diag.Pos
	Len  Expr
	Elem TypeExpr
}

// SliceType is `[]T`.
type SliceType struct {
	P    diag.Pos
	Elem TypeExpr
}

// GrowType is `grow [N]T`.
type GrowType struct {
	P    diag.Pos
	Cap  Expr
	Elem TypeExpr
}

// QueueType is `queue [N]T`.
type QueueType struct {
	P    diag.Pos
	Cap  Expr
	Elem TypeExpr
}

func (t *NamedType) TPos() diag.Pos { return t.P }
func (t *ArrayType) TPos() diag.Pos { return t.P }
func (t *SliceType) TPos() diag.Pos { return t.P }
func (t *GrowType) TPos() diag.Pos  { return t.P }
func (t *QueueType) TPos() diag.Pos { return t.P }

// ---- Declarations ----------------------------------------------------------

// Decl is a top-level declaration.
type Decl interface{ DPos() diag.Pos }

// ConstDecl is `const N = 64` or `const T: [4]u8 = [...]`.
type ConstDecl struct {
	P     diag.Pos
	Name  string
	Type  TypeExpr // may be nil
	Value Expr
	Sym   *types.Symbol
}

// VarDecl is a global `var x: T [= v]`.
type VarDecl struct {
	P     diag.Pos
	Name  string
	Type  TypeExpr
	Value Expr // may be nil
	Sym   *types.Symbol
}

// ErrorDecl is `error Timeout, Overrun`.
type ErrorDecl struct {
	P     diag.Pos
	Names []Ident
}

// EnumDecl is `enum Mode: u2` with members.
type EnumDecl struct {
	P       diag.Pos
	Name    string
	Base    TypeExpr
	Members []EnumMemberDecl
	T       *types.Type
}

// EnumMemberDecl is one enum member, optionally with an explicit value.
type EnumMemberDecl struct {
	P     diag.Pos
	Name  string
	Value Expr
}

// StructDecl is `struct Point` with fields.
type StructDecl struct {
	P      diag.Pos
	Name   string
	Fields []FieldDecl
	T      *types.Type
}

// FieldDecl is `name: T` in a struct, or `name: T @ bit` in a register.
type FieldDecl struct {
	P    diag.Pos
	Name string
	Type TypeExpr
	At   Expr // register offset or bit position; nil for struct fields
}

// RegsDecl is a register block type.
type RegsDecl struct {
	P    diag.Pos
	Name string
	Regs []RegDecl
	T    *types.Type
}

// RegDecl is one register, `NAME: u32 @ 0x04`, with optional bitfields.
type RegDecl struct {
	FieldDecl
	Bits []FieldDecl
}

// PeriphDecl is `periph USART2: Uart @ 0x40004400`.
type PeriphDecl struct {
	P    diag.Pos
	Name string
	Type string
	TP   diag.Pos
	Addr Expr
	Sym  *types.Symbol
}

// Param is a function parameter.
type Param struct {
	P    diag.Pos
	Name string
	Type TypeExpr
}

// FnDecl is a function, an isr, or an extern C function.
type FnDecl struct {
	P      diag.Pos
	Name   string
	Params []Param
	Ret    TypeExpr // nil if none
	Errs   []Ident  // nil if no `!`; empty slice is invalid
	Bang   bool     // `!` present
	Where  []Expr
	Stack  Expr // extern: `stack N`
	ISR    bool
	Extern bool
	Body   *Block // nil for extern
	Fn     *types.Fn
}

func (d *ConstDecl) DPos() diag.Pos  { return d.P }
func (d *VarDecl) DPos() diag.Pos    { return d.P }
func (d *ErrorDecl) DPos() diag.Pos  { return d.P }
func (d *EnumDecl) DPos() diag.Pos   { return d.P }
func (d *StructDecl) DPos() diag.Pos { return d.P }
func (d *RegsDecl) DPos() diag.Pos   { return d.P }
func (d *PeriphDecl) DPos() diag.Pos { return d.P }
func (d *FnDecl) DPos() diag.Pos     { return d.P }

// ---- Statements ------------------------------------------------------------

// Stmt is a statement.
type Stmt interface{ SPos() diag.Pos }

// Block is an indented statement list.
type Block struct {
	P     diag.Pos
	Stmts []Stmt
}

// DefineStmt is `x := e`.
type DefineStmt struct {
	P     diag.Pos
	Name  string
	Value Expr
	Sym   *types.Symbol
}

// VarStmt is `var x: T [= e]`.
type VarStmt struct {
	P     diag.Pos
	Name  string
	Type  TypeExpr
	Value Expr
	Sym   *types.Symbol
}

// AssignStmt is `lhs = rhs` or a compound assignment like `+=`.
type AssignStmt struct {
	P   diag.Pos
	Op  string // "=" or "+=", "-=", ...
	LHS Expr
	RHS Expr
	// Check is set when a compound assignment needs a runtime check.
	Check bool
}

// ExprStmt is a call (or catch) used as a statement.
type ExprStmt struct {
	P diag.Pos
	X Expr
}

// ReturnStmt is `return [e]`.
type ReturnStmt struct {
	P     diag.Pos
	Value Expr
	// Set by the checker when Value names an error tag.
	ErrTag string
}

// IfStmt is `if c` ... [`else if` ...] [`else` ...].
type IfStmt struct {
	P    diag.Pos
	Cond Expr
	Then *Block
	Else Stmt // *Block, *IfStmt or nil
}

// ForKind distinguishes loop forms.
type ForKind uint8

const (
	ForEver  ForKind = iota // for
	ForCond                 // for cond
	ForEach                 // for [i,] x in s
	ForRange                // for i in lo..hi
)

// ForStmt is any loop.
type ForStmt struct {
	P      diag.Pos
	Kind   ForKind
	Cond   Expr
	Var    string // element or range variable
	Index  string // optional index variable for ForEach
	Iter   Expr   // ForEach
	Lo, Hi Expr   // ForRange
	Body   *Block

	VarSym, IndexSym *types.Symbol
}

// BranchStmt is `break` or `continue`.
type BranchStmt struct {
	P   diag.Pos
	Tok string
}

func (s *Block) SPos() diag.Pos      { return s.P }
func (s *DefineStmt) SPos() diag.Pos { return s.P }
func (s *VarStmt) SPos() diag.Pos    { return s.P }
func (s *AssignStmt) SPos() diag.Pos { return s.P }
func (s *ExprStmt) SPos() diag.Pos   { return s.P }
func (s *ReturnStmt) SPos() diag.Pos { return s.P }
func (s *IfStmt) SPos() diag.Pos     { return s.P }
func (s *ForStmt) SPos() diag.Pos    { return s.P }
func (s *BranchStmt) SPos() diag.Pos { return s.P }

// ---- Expressions -----------------------------------------------------------

// Expr is an expression. Info returns the checker's annotations.
type Expr interface {
	EPos() diag.Pos
	Info() *ExprInfo
}

// ExprInfo is filled in by the checker.
type ExprInfo struct {
	Type  *types.Type
	Const *big.Int // set when the expression is a compile-time integer
	// Lo and Hi bound the value of an integer expression when the checker
	// can tell (constants, masks, loop variables); nil otherwise.
	Lo, Hi *big.Int
	// Check is set when the operation needs a runtime check (bounds,
	// overflow, narrowing, push capacity, precondition) that the checker
	// could not discharge. The C emitter inserts exactly these checks.
	Check bool
}

type exprBase struct {
	P diag.Pos
	I ExprInfo
}

func (e *exprBase) EPos() diag.Pos  { return e.P }
func (e *exprBase) Info() *ExprInfo { return &e.I }

// IntLit is an integer or character literal.
type IntLit struct {
	exprBase
	Val  *big.Int
	Text string
}

// BoolLit is `true` or `false`.
type BoolLit struct {
	exprBase
	Val bool
}

// Ident is a name.
type Ident struct {
	exprBase
	Name string
	Sym  *types.Symbol
}

// EnumLit is `.Out`; its enum type comes from context.
type EnumLit struct {
	exprBase
	Name   string
	Member *types.EnumMember
}

// Unary is `-x`, `!x`, `~x`.
type Unary struct {
	exprBase
	Op string
	X  Expr
}

// Binary is `x op y`.
type Binary struct {
	exprBase
	Op   string
	X, Y Expr
}

// CallKind is how the checker resolved a call.
type CallKind uint8

const (
	CallFn      CallKind = iota // user or extern function
	CallConvert                 // T(x), checked
	CallWrap                    // T.wrap(x)
	CallSat                     // T.sat(x)
	CallPush                    // g.push(x)
	CallClear                   // g.clear()
	CallSend                    // q.send(x) !Full
	CallRecv                    // q.recv() T !Empty
)

// Call is `f(args)` (also conversions and builtin methods).
type Call struct {
	exprBase
	Fun  Expr
	Args []Expr

	Kind   CallKind
	Callee *types.Fn
	Target *types.Type // conversion target
	Recv   Expr        // receiver for builtin methods
}

// Index is `x[i]`.
type Index struct {
	exprBase
	X, I Expr
}

// SliceExpr is `x[lo..hi]`; either bound may be nil.
type SliceExpr struct {
	exprBase
	X, Lo, Hi Expr
}

// FieldKind is how the checker resolved a selector.
type FieldKind uint8

const (
	FieldStruct FieldKind = iota
	FieldLen              // .len of array, slice, grow
	FieldCap              // .cap of grow, queue
	FieldReg              // register in a register block
	FieldBits             // bitfield in a register
	FieldMethod           // builtin method name, only valid as a callee
	FieldTypeFn           // T.wrap / T.sat, only valid as a callee
	FieldEnum             // Mode.Out
)

// Field is `x.name`.
type Field struct {
	exprBase
	X    Expr
	Name string

	Kind   FieldKind
	Reg    *types.Register
	Bits   *types.BitField
	Member *types.EnumMember
}

// Try is `x?`.
type Try struct {
	exprBase
	X Expr
}

// Catch is `x catch default` or, as a statement, `x catch [e]` + block.
type Catch struct {
	exprBase
	X       Expr
	Default Expr   // expression form
	Jump    Stmt   // `catch break`, `catch continue`, `catch return [e]`
	ErrName string // block form binding, may be ""
	Body    *Block // block form
	ErrSym  *types.Symbol
}

// StructLit is `Point{x: 1, y: 2}`.
type StructLit struct {
	exprBase
	TypeName string
	Fields   []FieldInit
}

// FieldInit is one `name: value` in a struct literal.
type FieldInit struct {
	P     diag.Pos
	Name  string
	Value Expr
}

// ArrayLit is `[a, b, c]`.
type ArrayLit struct {
	exprBase
	Elems []Expr
}

// Paren is `(x)`.
type Paren struct {
	exprBase
	X Expr
}

// WalkExpr calls f for e and every expression nested in it, in source order,
// stopping descent where f returns false. Catch handler blocks and jumps are
// statements and are not entered.
func WalkExpr(e Expr, f func(Expr) bool) {
	if e == nil || !f(e) {
		return
	}
	switch x := e.(type) {
	case *Unary:
		WalkExpr(x.X, f)
	case *Binary:
		WalkExpr(x.X, f)
		WalkExpr(x.Y, f)
	case *Call:
		WalkExpr(x.Fun, f)
		for _, a := range x.Args {
			WalkExpr(a, f)
		}
	case *Index:
		WalkExpr(x.X, f)
		WalkExpr(x.I, f)
	case *SliceExpr:
		WalkExpr(x.X, f)
		WalkExpr(x.Lo, f)
		WalkExpr(x.Hi, f)
	case *Field:
		WalkExpr(x.X, f)
	case *Try:
		WalkExpr(x.X, f)
	case *Catch:
		WalkExpr(x.X, f)
		WalkExpr(x.Default, f)
	case *StructLit:
		for _, fi := range x.Fields {
			WalkExpr(fi.Value, f)
		}
	case *ArrayLit:
		for _, el := range x.Elems {
			WalkExpr(el, f)
		}
	case *Paren:
		WalkExpr(x.X, f)
	}
}

// StmtExprs returns the expressions directly owned by a simple statement.
func StmtExprs(s Stmt) []Expr {
	switch x := s.(type) {
	case *DefineStmt:
		return []Expr{x.Value}
	case *VarStmt:
		if x.Value != nil {
			return []Expr{x.Value}
		}
	case *AssignStmt:
		return []Expr{x.LHS, x.RHS}
	case *ExprStmt:
		return []Expr{x.X}
	case *ReturnStmt:
		if x.Value != nil {
			return []Expr{x.Value}
		}
	case *IfStmt:
		return []Expr{x.Cond}
	case *ForStmt:
		var es []Expr
		for _, e := range []Expr{x.Cond, x.Iter, x.Lo, x.Hi} {
			if e != nil {
				es = append(es, e)
			}
		}
		return es
	}
	return nil
}
