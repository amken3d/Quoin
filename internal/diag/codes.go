package diag

// Code describes one diagnostic code for `quoinc explain`.
type Code struct {
	Code    string
	Kind    string
	Explain string
}

// Codes is the stable diagnostic catalog. Codes are never reused.
//
//	E0xx  lexing and parsing
//	E1xx  names and types
//	E2xx  memory, stack and entry points
//	E3xx  contracts (reserved for the bounded-fragment checker)
//	W     warnings
var Codes = []Code{
	{"E001", "BadChar", "A character that is not part of Quoin's syntax."},
	{"E002", "BadIndent", "Indentation uses tabs, or a dedent does not return to an enclosing block's column. Indent with spaces; each block is deeper than its header line."},
	{"E003", "Unexpected", "The parser expected one token and found another."},
	{"E004", "BadLiteral", "A malformed integer or character literal."},

	{"E101", "Undefined", "A name is used that is not declared in scope."},
	{"E102", "BoundUnproven", "Reserved: an index or push could not be proven in-bounds. In v0 the compiler inserts a runtime check instead and counts it in the CHECKS line."},
	{"E103", "ConstOverflow", "A constant does not fit the type it is used as."},
	{"E104", "TypeMismatch", "A value of one type is used where another type is required."},
	{"E105", "BadCall", "Wrong number of arguments, or calling something that is not a function."},
	{"E106", "Redeclared", "A name is declared twice in the same or an enclosing scope. Quoin has no shadowing."},
	{"E107", "UnhandledError", "A call that can fail is used without `?` (propagate) or `catch` (handle)."},
	{"E108", "ErrorNotInSet", "`?` or `return` would produce an error the enclosing function does not declare."},
	{"E109", "NoErrorSet", "`?` or an error return is used in a function whose return type has no `!` error set."},
	{"E110", "NotAssignable", "The left side of an assignment is not a variable, field, element or register."},
	{"E111", "MissingReturn", "A function with a result type can reach its end without returning."},
	{"E112", "UnknownField", "A struct, register block, register or builtin has no member with that name."},
	{"E113", "BadOperator", "An operator is applied to operand types it does not accept."},
	{"E114", "NotConstant", "A value that must be known at compile time (array length, register offset) is not."},
	{"E116", "BadBranch", "`break` or `continue` outside a loop."},
	{"E117", "Narrowing", "An implicit conversion would lose information. Quoin only widens implicitly; narrow with T(x) (checked) or T.wrap(x) (truncating)."},
	{"E118", "ViewEscape", "A slice would outlive the storage it views: slices are second-class and cannot be returned, stored in structs, arrays or globals, reassigned, or taken of a constant or a temporary."},
	{"E119", "BadTryPosition", "`?` or `catch` inside the right side of `&&`/`||`. Bind the call to a variable first."},
	{"E120", "BadRegister", "A register or bitfield layout is invalid: unaligned, overlapping, or wider than its register."},
	{"E121", "BadEntry", "`main` and `isr` functions take no parameters and return nothing."},
	{"E122", "GrowPlacement", "A grow array appears somewhere other than a local, a global, or a function parameter."},
	{"E123", "NotCopyable", "Grow arrays, queues and register blocks cannot be copied by assignment."},
	{"E124", "BadType", "A type expression is malformed or names something that is not a type."},
	{"E126", "OutOfRange", "A constant index or slice bound is outside the array, or a constant shift is not smaller than the operand width."},
	{"E125", "UnusedValue", "An expression statement that is not a call has no effect."},

	{"E201", "Recursion", "The call graph has a cycle, so no static stack bound exists. Rewrite as a loop."},
	{"E202", "StackOverBudget", "Worst-case call stack plus data stack exceeds the --budget."},
	{"E203", "ExternStackUnknown", "A reachable `extern fn` does not declare its stack use. Add `stack <bytes>` to its declaration."},
	{"E204", "SharedState", "A global `var` is used from more than one entry point (main and an isr, or two isrs). Hand data across with a `queue`."},
	{"E205", "QueueRoles", "A queue must have exactly one sending entry point and one receiving entry point (single producer, single consumer)."},
	{"E206", "NoEntry", "The program has no `fn main()`."},
	{"E207", "NoStackUsage", "`quoinc stack` found no -fstack-usage entry for an entry point. Compile every generated .c with -fstack-usage and pass all .su files. Static functions missing from the .su files are treated as inlined."},

	{"E208", "UnsupportedOnTarget", "The selected target cannot compile this construct yet (for example 64-bit integers on the thumb backend), or an isr is not named after one of the target's vectors."},

	{"E301", "CycleClaimFailed", "The proven worst-case cycle count of a function exceeds its `where cycles <= N` claim. The message gives the bound; raise the claim or remove work from the worst path."},
	{"E302", "StackClaimFailed", "The proven worst-case stack (own frame plus deepest callee chain) exceeds the `where stack <= N` claim."},
	{"E303", "CyclesUnbounded", "A function claims `cycles <= N` but has no finite bound: a `for` without a bound, a slice loop without a `where s.len <= N`, or a call to something unbounded (including externs without `cycles N`)."},
	{"W301", "ClaimNotChecked", "Cycle claims are only proven on native targets (--target rp2350 or m33-qemu); the C backend cannot see the final instructions."},

	{"W101", "RuntimeCheck", "An index, push, conversion or arithmetic step could not be proven at compile time; a runtime check that calls ql_trap() was inserted."},
}

var codeIndex = func() map[string]Code {
	m := map[string]Code{}
	for _, c := range Codes {
		m[c.Code] = c
	}
	return m
}()

// KindOf returns the CamelCase kind for a code.
func KindOf(code string) string {
	if c, ok := codeIndex[code]; ok {
		return c.Kind
	}
	return "Unknown"
}

// Lookup returns the catalog entry for a code.
func Lookup(code string) (Code, bool) {
	c, ok := codeIndex[code]
	return c, ok
}
