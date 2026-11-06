# Quoin v0 language reference

This is the language `quoinc` accepts today. It is deliberately small. Every rule here is enforced by the compiler, and each violation has a diagnostic code (`quoinc explain` lists them).

Source files use the `.qn` extension. A program is every file passed to one `quoinc` invocation, compiled as a whole.

## Lexical

- Blocks are indentation. A line ending a header (`fn`, `if`, `for`, `struct`, ...) is followed by lines indented deeper. Use spaces; tabs in indentation are an error (E002).
- Newlines end statements. Inside `(`, `[`, `{` newlines are ignored, so long argument lists can wrap.
- Comments: `// to end of line`.
- Integers: `42`, `0x2A`, `0b101010`, `0o52`, `1_000_000`. Character literals are integers: `'a'`, `'\n'`, `'\x41'`. There are no string literals in v0.
- Keywords: `fn isr extern const var error enum struct regs periph if else for in break continue return catch where grow queue stack true false`.

## Types

| Type | Meaning |
| --- | --- |
| `u1`..`u64`, `i1`..`i64` | Integers of any width. `u12` holds 0..4095. Stored in the next C width up. |
| `usize` | Alias of `u32` (v0 targets 32-bit cores). Indexes and lengths are `usize`. |
| `bool` | `true` / `false`. Not an integer. |
| `[N]T` | Fixed array, a value: assignment and parameters copy it. |
| `[]T` | Slice: a view of an array, grow array or another slice. See *Views*. |
| `grow [N]T` | Array with capacity `N` and a length that starts at 0. Lives on the data stack. |
| `queue [N]T` | Single-producer single-consumer ring buffer holding up to `N` values. Global only. |
| `struct`, `enum` | Declared types (below). |
| `regs` | Memory-mapped register block (below). |

There are no pointers, no heap, no function values, and no recursion.

### Conversions

Integers widen implicitly when no value can be lost (`u8` to `u16`, `u8` to `i16`, `i8` to `i32`). Nothing else converts implicitly. To narrow:

```
a := u8(x)        // checked: traps if x does not fit
b := u8.wrap(x)   // keeps the low 8 bits
c := u8.sat(x)    // clamps to 0..255
d := u32(flag)    // bool to 0 or 1
e := u8(color)    // enum to its integer value
```

Integer constants are untyped until used. They must fit the type they are used as (E103). A bare `x := 5` is `i32`; `for i in 0..n` with constant bounds is `usize`.

## Declarations

```
const N = 64                       // untyped constant
const MASK: u32 = 0xFF00
const TABLE: [4]u16 = [1, 10, 100, 1000]

var ticks: u32                     // global, zero-initialized
var log: grow [128]u8
var rx: queue [32]u8

error Timeout, Overrun             // error tags (Full and Empty are builtin)

struct Point
  x: i32
  y: i32

enum Mode: u2                      // the base type sets the width
  In
  Out = 1
  Alt
  Analog

fn name(a: u8, buf: []u8) u16 !Timeout|Overrun where buf.len <= 64
  ...

isr fn USART2_IRQHandler()         // interrupt entry; the C symbol keeps the name
  ...

extern fn delay_us(us: u32) stack 48   // C function; `stack` is its stack bound in bytes
```

`fn main()` is required. `main` and every `isr fn` are entry points: they take no parameters and return nothing (E121), and they cannot be called.

Names are never shadowed. A name visible from an enclosing scope, globals included, cannot be declared again (E106).

## Functions and errors

A function's result has an optional value type and an optional error set:

```
fn f()                  // returns nothing, cannot fail
fn g() u8               // returns u8
fn h() !Timeout         // returns nothing or fails with Timeout
fn k() u8 !Timeout|Bad  // returns u8 or fails with Timeout or Bad
```

Fail with `return Timeout`. Calling a fallible function forces a decision (E107):

```
v := k()?                       // propagate: the caller's error set must include k's (E108, E109)
v := k() catch 0                // default value on any error
v := k() catch return           // leave the function (or `catch return x`, `catch break`, `catch continue`)
k() catch e                     // statement form: handler block, `e` is the error
  if e == Timeout
    retry()
```

There are no exceptions and no unwinding. `?` and `catch` cannot appear on the right of `&&`/`||` (E119); bind the call first.

## Statements

```
x := expr                 // define with inferred type
var y: u8                 // define, zero value
var z: u8 = 3             // define with explicit type
y = expr                  // assign
y += 1                    // also -= *= /= %= &= |= ^= <<= >>=
if c
  ...
else if d
  ...
else
  ...
for                       // forever
  ...
for cond                  // while cond holds
  continue                // `continue` is the idiom for an empty spin loop
for i in 0..n             // i = 0, 1, ..., n-1
  ...
for b in buf              // each element (array, slice or grow array)
  ...
for i, b in buf           // with index
  ...
break
continue
return [value]
call(args)                // only calls can be expression statements (E125)
```

Loop variables are read-only.

## Operators

Precedence, loosest to tightest:

| Level | Operators |
| --- | --- |
| 1 | `\|\|` |
| 2 | `&&` |
| 3 | `== != < <= > >=` (do not chain) |
| 4 | `+ - \| ^ +% -% +\| -\|` |
| 5 | `* / % & << >> *% *\|` |
| 6 | unary `- ! ~` |
| 7 | `f(x)` `a[i]` `a[lo..hi]` `x.f` `x?` `x catch y` |

Arithmetic is checked by default:

- `+ - *` trap on overflow, `/ %` trap on divide by zero. `/` truncates and `%` takes the dividend's sign, as in C and Go.
- `+% -% *%` wrap modulo 2^width.
- `+| -| *|` saturate at the type's bounds.
- `<<` discards bits shifted out; `>>` is arithmetic on signed types. The shift amount must be unsigned and smaller than the width (checked).
- Both operands of a binary operator have the same type after widening (E104).

The compiler drops a check when it can prove the operation safe from constants, masks and loop bounds. For example `u16(a) + u16(b)` with `a, b: u8` has no overflow check, and `arr[i]` inside `for i in 0..arr.len` has no bounds check. Every check it keeps is counted in the `CHECKS` line; `--checks` lists them.

## Memory model

- **Call stack.** Scalars, structs and fixed arrays live in C locals.
- **Data stack.** Every `var g: grow [N]T` local gets a fixed offset in its function's data frame. The frame is claimed on entry and released on every return. The data stack is one static array, shared by `main` and the isrs (an isr claims space above main's top and restores it before returning). Its size is the computed worst case, so it can never overflow.
- **Globals** are static. A global `var` may be touched by only one entry point's call tree (E204). Data crosses between an isr and `main` only through a `queue`, which must have one sending entry point and one receiving entry point (E205).

Grow arrays:

```
var buf: grow [64]u8
buf.push(b)        // traps if full (v0; the contract checker will prove pushes)
buf.len  buf.cap  buf[i]  buf.clear()
fill(buf)          // a `grow [64]u8` parameter takes the caller's array by reference
```

Capacity is part of the type: a `grow [64]u8` argument needs a `grow [64]u8` parameter.

Queues:

```
var rx: queue [32]u8
rx.send(b) catch return     // send(x) can fail with Full
b := rx.recv() catch break  // recv() can fail with Empty
```

### Views

Slices are second-class views, which keeps them from ever dangling (E118). A slice can be:

- passed as an argument;
- bound once with `s := a[lo..hi]`, after which it cannot be rebound;
- created implicitly when an array or grow array is passed to a `[]T` parameter.

A slice cannot be returned, stored in a struct, array or global, declared with `var`, or taken of a constant or a temporary. A view of a grow array sees the length at the time it was taken.

## Registers

```
enum Mode: u2
  In
  Out
  Alt
  Analog

regs Gpio
  MODER: u32 @ 0x00           // register: width u8/u16/u32, byte offset
    P5: Mode @ 10             // bitfield: type sets the width, number is the low bit
  ODR: u32 @ 0x14
    P5: bool @ 5

periph GPIOA: Gpio @ 0x40020000

GPIOA.MODER.P5 = .Out         // read-modify-write of bits 10..11
GPIOA.ODR = 0                 // whole-register write
on := GPIOA.ODR.P5            // volatile read, then extract
fn blink(g: Gpio)             // register blocks pass by reference
```

Offsets must be aligned and increasing; bitfields must fit their register and not overlap (E120). Enum literals (`.Out`) take their enum type from context. Every register read happens exactly once, in source order.

## Evaluation order

Operands and arguments are evaluated left to right, including calls and register reads. C leaves this unspecified; the generated code fixes it with temporaries. `&&` and `||` short-circuit.

## Compiler output

```
E107 uart.qn:12:5 UnhandledError wait_txe(...) can fail with Timeout
  fix: append `?` to propagate, or `catch` to handle
STACK main=56B isr:USART2_IRQHandler=48B data=72B total=176B of 2048B src=est
CHECKS runtime=2 push=1 where=1
```

- One diagnostic per line: code, position, kind, message, and a `fix:` line when there is a concrete edit. `--json` prints the same as one JSON object.
- After a syntax error, only syntax errors are reported, one per top-level declaration.
- `STACK` gives each entry point's worst-case call stack (isrs include the 32-byte exception frame; use `--exc-frame 104` for an FPU context), the total data stack, and the sum. The total assumes every isr can preempt main and each other. `src=est` marks estimated call frames; `quoinc stack --su` replaces them with gcc's `-fstack-usage` numbers (`src=su`). Data-stack figures are exact either way.
- `--budget N` turns an over-budget total into E202.
