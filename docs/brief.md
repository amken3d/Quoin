# Quoin in brief

Quoin reads like Go with Zig's integers and indentation instead of braces; no heap, pointers, recursion or strings. Below is only what differs from Go or makes the compiler reject code.

## Shape

```
const N = 8                         // untyped integer constant
const TABLE: [4]u8 = [1, 2, 4, 8]
var count: u32                      // global, zeroed
error Bad, Late                     // error tags

struct Pair
  a: i16
  b: u8

fn scale(x: u16, k: u8) u32 !Bad    // params, result, error set (each optional)
  if k == 0
    return Bad                      // fail with a tag
  return u32(x) * u32(k)

fn main()                           // required; no params, no result
  var buf: [N]u8                    // zeroed local
  var total: u32 = 0                // typed locals always start with var
  t := scale(3, 2) catch 0          // := infers the type
  for i in 0..N                     // i = 0..N-1
    buf[i] = u8(i)
```

- Blocks are indented with spaces; no braces, semicolons or parentheses around conditions.
- `if`/`else if`/`else`; `for` (forever), `for cond`, `for i in a..b`, `for x in arr`, `for i, x in arr`; `break`, `continue`, `return`. An empty loop body is `continue`.
- `x := e` declares with an inferred type. A typed declaration always starts with `var`: `var x: u32 = 0`, never `x: u32 = 0`. `=` and compound forms (`+=`, `<<=`, `+%=`, `+|=`, ...) assign.
- Logical operators are `&&`, `||` and `!` (not `and`, `or`, `not`).

## Types and conversions

- Integers of any width: `u1`..`u32`, `i1`..`i32` (`u12` holds 0..4095). `usize` is `u32`; indexes and `.len` are `usize`. `bool` is not an integer.
- `[N]T` array (a value: copies on assign and call), `[]T` slice view, `grow [N]T` (capacity N, `.push(x)`, `.len`, `.cap`, `.clear()`, indexable), `struct`, `enum E: u8` (`.Member` literals).
- Only lossless widening is implicit (`u8` to `u32`, `u8` to `i16`). Narrow explicitly: `u8(x)` traps if out of range, `u8.wrap(x)` truncates, `u8.sat(x)` clamps. `u32(flag)` gives 0 or 1. `i8.wrap(b)` reinterprets a byte as signed.
- Both operands of a binary operator must have the same type after widening; constants adapt. `x := 5` is `i32`.

## Arithmetic (differs from C and Go)

- `+ - *` trap on overflow, including below zero for unsigned. Use `+% -% *%` to wrap and `+| -| *|` to saturate.
- `/` and `%` truncate toward zero and trap on division by zero.
- `<<` discards bits shifted out. A shift amount must be unsigned and smaller than the operand's width, or it traps: `u32(1) << 32` is an error.
- Precedence is Go's: `&` `|` `^` `<<` `>>` bind tighter than comparisons, so `x & 0x80 != 0` means `(x & 0x80) != 0`. Comparisons do not chain.
- Unary `-` is for signed types; for unsigned, use `0 -% x`.

## Errors

- A call to a function with `!E` must be handled: `f()?` (propagate; the caller must declare the same errors), `f() catch v` (default value), `f() catch return` / `catch break` / `catch continue`, or a statement `f() catch e` followed by an indented block where `e` is the error.

## Slices and memory

- A slice can be passed as an argument or bound once (`s := a[1..4]`, also `a[..n]`, `a[n..]`). It cannot be returned, stored in a struct, array or global, rebound, or taken of a `const` (copy it into a `var` first). Arrays and grow arrays pass to `[]T` parameters automatically.
- `grow` locals live on a separate data stack; a `grow [N]T` parameter takes the caller's array by reference.

## Registers

```
regs Dev
  CTRL: u32 @ 0x00          // register: type and byte offset
    EN: bool @ 0            // bitfield: type sets the width, number is the low bit
    MODE: u2 @ 4
periph DEV: Dev @ 0x40000000
DEV.CTRL.MODE = 2           // read-modify-write of bits 4..5
v := DEV.CTRL               // volatile read
```

## The compiler rejects

- Redeclaring any visible name, including globals and loop variables (there is no shadowing).
- Assigning to a loop variable; recursion; expression statements that are not calls; unhandled `!E` calls.
- A function with a result that can reach its end without `return`.
