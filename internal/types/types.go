// Package types defines Quoin's semantic types and symbols.
package types

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/amken3d/quoin/internal/diag"
)

// Kind classifies a type.
type Kind uint8

const (
	Invalid Kind = iota
	Void
	Bool
	Int        // arbitrary width integer: Bits, Signed
	UntypedInt // integer constant not yet bound to a type
	Enum       // Name, Base, Members
	Struct     // Name, Fields
	Array      // [Len]Elem, value type
	Slice      // []Elem, a second-class view
	Grow       // grow [Len]Elem, fixed capacity, length starts at 0
	Queue      // queue [Len]Elem, single-producer single-consumer ring
	Regs       // register block (memory-mapped, volatile)
	ErrorVal   // value of an error tag
	Union      // result of a fallible call: Elem (Void if none) or one of Errs
)

// Type is a Quoin type. Named types (Struct, Enum, Regs) compare by pointer;
// the rest compare structurally via Identical.
type Type struct {
	Kind   Kind
	Bits   int
	Signed bool
	Len    int64
	Elem   *Type
	Name   string
	Base   *Type // Enum: underlying integer type

	Fields  []*Field      // Struct
	Members []*EnumMember // Enum
	Regs    []*Register   // Regs
	Errs    []string      // Union
	Pos     diag.Pos      // declaration position of named types
}

// Field is a struct field.
type Field struct {
	Name string
	Type *Type
	Pos  diag.Pos
}

// EnumMember is one enum value.
type EnumMember struct {
	Name  string
	Value *big.Int
}

// Register is one memory-mapped register in a Regs block.
type Register struct {
	Name   string
	Offset int64
	Type   *Type // u8, u16 or u32
	Fields []*BitField
	Pos    diag.Pos
}

// BitField is a named bit range inside a register.
type BitField struct {
	Name  string
	Bit   int
	Width int
	Type  *Type // bool, unsigned int or enum
	Pos   diag.Pos
}

var (
	InvalidT  = &Type{Kind: Invalid}
	VoidT     = &Type{Kind: Void}
	BoolT     = &Type{Kind: Bool}
	UntypedT  = &Type{Kind: UntypedInt}
	ErrorValT = &Type{Kind: ErrorVal}
)

var intCache = map[[2]int]*Type{}

// IntT returns the interned integer type of the given width and signedness.
func IntT(bits int, signed bool) *Type {
	s := 0
	if signed {
		s = 1
	}
	k := [2]int{bits, s}
	if t, ok := intCache[k]; ok {
		return t
	}
	t := &Type{Kind: Int, Bits: bits, Signed: signed}
	intCache[k] = t
	return t
}

// Common integer types.
var (
	U8    = IntT(8, false)
	U16   = IntT(16, false)
	U32   = IntT(32, false)
	I32   = IntT(32, true)
	Usize = U32 // v0 targets 32-bit cores only
)

func (t *Type) String() string {
	if t == nil {
		return "<nil>"
	}
	switch t.Kind {
	case Invalid:
		return "<invalid>"
	case Void:
		return "void"
	case Bool:
		return "bool"
	case Int:
		if t.Signed {
			return fmt.Sprintf("i%d", t.Bits)
		}
		return fmt.Sprintf("u%d", t.Bits)
	case UntypedInt:
		return "untyped int"
	case Enum, Struct, Regs:
		return t.Name
	case Array:
		return fmt.Sprintf("[%d]%s", t.Len, t.Elem)
	case Slice:
		return "[]" + t.Elem.String()
	case Grow:
		return fmt.Sprintf("grow [%d]%s", t.Len, t.Elem)
	case Queue:
		return fmt.Sprintf("queue [%d]%s", t.Len, t.Elem)
	case ErrorVal:
		return "error"
	case Union:
		s := "!" + strings.Join(t.Errs, "|")
		if t.Elem.Kind != Void {
			return t.Elem.String() + " " + s
		}
		return s
	}
	return "?"
}

// IsInt reports whether t is a typed integer.
func (t *Type) IsInt() bool { return t.Kind == Int }

// IsIntLike reports whether t is a typed or untyped integer.
func (t *Type) IsIntLike() bool { return t.Kind == Int || t.Kind == UntypedInt }

// Min returns the smallest value of an integer type.
func (t *Type) Min() *big.Int {
	if !t.Signed {
		return big.NewInt(0)
	}
	v := new(big.Int).Lsh(big.NewInt(1), uint(t.Bits-1))
	return v.Neg(v)
}

// Max returns the largest value of an integer type.
func (t *Type) Max() *big.Int {
	n := t.Bits
	if t.Signed {
		n--
	}
	v := new(big.Int).Lsh(big.NewInt(1), uint(n))
	return v.Sub(v, big.NewInt(1))
}

// Fits reports whether v is representable in integer type t.
func (t *Type) Fits(v *big.Int) bool {
	if t.Kind == Enum {
		t = t.Base
	}
	if t.Kind == Bool {
		return v.Sign() == 0 || v.Cmp(big.NewInt(1)) == 0
	}
	if t.Kind != Int {
		return false
	}
	return v.Cmp(t.Min()) >= 0 && v.Cmp(t.Max()) <= 0
}

// Identical reports type identity.
func Identical(a, b *Type) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil || a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case Void, Bool, UntypedInt, ErrorVal, Invalid:
		return true
	case Int:
		return a.Bits == b.Bits && a.Signed == b.Signed
	case Array, Grow, Queue:
		return a.Len == b.Len && Identical(a.Elem, b.Elem)
	case Slice:
		return Identical(a.Elem, b.Elem)
	case Union:
		if !Identical(a.Elem, b.Elem) || len(a.Errs) != len(b.Errs) {
			return false
		}
		for i := range a.Errs {
			if a.Errs[i] != b.Errs[i] {
				return false
			}
		}
		return true
	}
	return false // named types compare by pointer
}

// Widens reports whether integer type from converts losslessly to to.
func Widens(from, to *Type) bool {
	if from.Kind != Int || to.Kind != Int {
		return false
	}
	if from.Signed == to.Signed {
		return to.Bits >= from.Bits
	}
	return !from.Signed && to.Signed && to.Bits > from.Bits
}

// IsValue reports whether t is a plain value type that can live in arrays,
// struct fields and locals and be copied: ints, bools, enums, structs and
// fixed arrays of those.
func (t *Type) IsValue() bool {
	switch t.Kind {
	case Bool, Int, Enum, Struct, ErrorVal:
		return true
	case Array:
		return t.Elem.IsValue()
	}
	return false
}

// Layout returns the C size and alignment of a value type on a 32-bit target.
func (t *Type) Layout() (size, align int64) {
	switch t.Kind {
	case Bool:
		return 1, 1
	case Int:
		b := CBits(t.Bits)
		return int64(b / 8), int64(min(b/8, 8))
	case Enum:
		return t.Base.Layout()
	case ErrorVal:
		return 2, 2
	case Array:
		s, a := t.Elem.Layout()
		return s * t.Len, a
	case Struct:
		var off, maxA int64 = 0, 1
		for _, f := range t.Fields {
			s, a := f.Type.Layout()
			off = AlignUp(off, a)
			off += s
			maxA = max(maxA, a)
		}
		return AlignUp(off, maxA), maxA
	case Grow:
		s, a := t.Elem.Layout()
		a = max(a, 4)
		off := AlignUp(4, a)
		return AlignUp(off+s*t.Len, a), a
	case Queue:
		// struct { volatile uint32_t head, tail; T buf[N]; }
		s, a := t.Elem.Layout()
		a = max(a, 4)
		off := AlignUp(8, a)
		return AlignUp(off+s*t.Len, a), a
	case Slice:
		return 8, 4
	case Regs:
		return 4, 4 // passed as a pointer
	}
	return 0, 1
}

// CBits returns the C storage width (8, 16, 32 or 64) for a bit width.
func CBits(bits int) int {
	switch {
	case bits <= 8:
		return 8
	case bits <= 16:
		return 16
	case bits <= 32:
		return 32
	}
	return 64
}

// AlignUp rounds n up to a multiple of a.
func AlignUp(n, a int64) int64 {
	if a <= 1 {
		return n
	}
	return (n + a - 1) / a * a
}

// SymKind classifies a symbol.
type SymKind uint8

const (
	SymConst SymKind = iota
	SymGlobal
	SymLocal
	SymParam
	SymFn
	SymType
	SymErr
	SymPeriph
)

// Symbol is a resolved name.
type Symbol struct {
	Kind  SymKind
	Name  string
	CName string
	Type  *Type
	Const *big.Int // SymConst value; SymErr code
	Fn    *Fn
	Pos   diag.Pos
	Addr  *big.Int // SymPeriph base address

	// Locals of grow type live on the data stack at DataOff in their
	// function's data frame.
	OnDataStack bool
	DataOff     int64
	Immutable   bool // slice, register and loop bindings cannot be reassigned
	// Lo and Hi bound an immutable integer binding (loop variables).
	Lo, Hi *big.Int
	Decl   any // the declaring AST node, if any
}

// Fn is the semantic record of a function.
type Fn struct {
	Name      string
	CName     string
	Params    []*Symbol
	Ret       *Type    // result value type (Void if none)
	Errs      []string // declared error set; nil means infallible
	ISR       bool
	Extern    bool
	Main      bool
	StackDecl int64 // extern: declared stack bytes, -1 if unknown
	Pos       diag.Pos
	Decl      any

	Calls     []Call      // direct calls, in source order
	DataFrame int64       // bytes of data stack this function claims
	FrameEst  int64       // estimated C call-stack frame bytes
	Checks    []CheckSite // runtime checks inserted
	Globals   map[*Symbol]GlobalUse
	QueueUses map[*Symbol]QueueUse
}

// CheckSite is one runtime check the compiler could not discharge.
type CheckSite struct {
	Pos  diag.Pos
	What string
}

// Call is one call edge.
type Call struct {
	Callee *Fn
	Pos    diag.Pos
}

// GlobalUse records where a function touches a global var.
type GlobalUse struct {
	Pos   diag.Pos
	Write bool
}

// QueueUse records where a function sends to or receives from a queue.
type QueueUse struct {
	SendPos, RecvPos diag.Pos
	Send, Recv       bool
}

// Fallible reports whether the function declares an error set.
func (f *Fn) Fallible() bool { return f.Errs != nil }

// ResultType is the type a call expression to f has before `?`/`catch`.
func (f *Fn) ResultType() *Type {
	if f.Fallible() {
		return &Type{Kind: Union, Elem: f.Ret, Errs: f.Errs}
	}
	return f.Ret
}
