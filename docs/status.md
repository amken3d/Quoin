# Status: v0 against the design

[design.md](design.md) is the plan, [language.md](language.md) is what the compiler accepts today, and this file is the difference between the two.

## Minimal viable slice

| Item from the design | State |
| --- | --- |
| Parser and type checker: fixed arrays, grow arrays, slices, error unions, `?` | Done. Also structs, enums, arbitrary-width ints, wrapping and saturating operators, `catch` (value, jump and handler forms), queues, globals and constants. |
| Data-stack growth analysis with a per-entry-point worst-case byte count | Done. Data frames are exact. Call frames are estimated by the front end, or exact from gcc `-fstack-usage` (`quoinc stack --su`), GNATstack-style. Recursion is rejected (E201), and `extern fn` must declare its stack (E203). |
| C99 emission with volatile register structs; validate on RP2040 and an STM32 part | Emission done, with register structs (offsets checked by `QL_STATIC_ASSERT`) and bitfields as read-modify-write. The examples build warning-free (`-std=c99 -pedantic -Wall -Wextra -Werror -O2`) for Cortex-M0+ (RP2040), Cortex-M4 (STM32F4) and Cortex-M33 (RP2350), and the exact stack bound is computed from the resulting `.su` files. Language semantics are tested by running the generated C on the host. |
| Native RP2350 backend with full claims (added after the design) | Thumb-2 assembly emitted directly, so frames are exact and every instruction's cost is known. Worst-case cycle and instruction bounds per function; `where cycles <= N` and `where stack <= N` proven statically. Verified on QEMU's Cortex-M33 (see below). Builds SRAM-resident RP2350 ELF and UF2 images. **Not yet run on an RP2350.** |
| Structured error format with a fix line | Done. 37 codes with `quoinc explain`, `--json`, name suggestions (`did you mean`). |
| Measure tokens-to-correct-program against TinyGo on 10 driver tasks | Not started. [benchmark.md](benchmark.md) defines it with the full metric set; the metrics that need no model or hardware are measured by `cmd/quoin-bench`. |
| `where` contracts and the bounded-fragment checker | Parsed and type-checked. In v0 a `where` clause is a **runtime precondition** at function entry, not a proof. |

## How the native backend is verified

| Check | Where | Result |
| --- | --- | --- |
| Same output as the C backend on every semantic test (arithmetic of all widths, error unions, grow arrays, slices, queues, structs, stack-passed arguments, aggregate returns) | `TestQEMURun`, QEMU mps2-an505 | all pass |
| Measured stack ≤ proven stack, measured data stack ≤ proven | stack painting on QEMU, every run test and kernel | zero violations; equal on every kernel |
| Executed instructions ≤ static instruction bound | `TestKernels`, single-step trace per function call | zero violations; 1.00 to 1.07× except insertion sort at 1.84× |
| RP2350 image boots into the right MMIO sequence | `TestRP2350MMIO`: Unicorn with mocked RESETS, exact write sequence | pass |
| IMAGE_DEF block placement and contents | inspected; byte-identical to the canonical minimal RP2350 block, inside the first 4 KB | pass |
| Cycle bounds | needs DWT_CYCCNT on an RP2350 | **not verified**. The timing model takes the worst case of each Cortex-M4 TRM range, assumes zero-wait SRAM and no bus contention, and uses placeholder wait states for peripherals |

## Design decisions made in v0 (please review)

1. **Indentation blocks** (the design's open question), following its example. Syntax errors map back to `.qn` lines through `#line`, so C-level debugging lands on Quoin source. Switching to braces is confined to the lexer.
2. **Implicit lossless widening.** The design says "no implicit conversions". v0 allows conversions that cannot lose information (`u8` to `u32`, `u8` to `i16`) and nothing else, as Zig does. Without this the doc's own `u.DR = b` (`u8` into a `u32` register) needs a cast.
3. **Error-union spelling.** `fn f() u8 !Timeout|Overrun`, raised with `return Timeout`. Goose's `return E from f` is not implemented.
4. **Grow capacity is part of the type**, and grow parameters take the caller's array by reference.
5. **No shadowing**, and slices are second-class (cannot be returned, stored or rebound). This makes view safety a syntactic rule instead of an inference.
6. **Constants cannot be viewed as slices.** v0 has no read-only slices, and a writable view of a flash table would let code write to flash. Copy the table into a `var`, or index it directly.
7. **No globals shared between entry points (E204).** This is strict: even "configure in main, then read in the isr" is rejected. Queues are the only channel. Registers are exempt.
8. **Defined evaluation order.** Left to right, calls and register reads included, by hoisting into temporaries.
9. **Stack total = main + every isr** (assumes full nesting). ISRs include a 32-byte exception frame (`--exc-frame` to change it).
10. **Static functions missing from the `.su` files count as inlined** (0 bytes; their frame is inside the caller's). Entry points must be present (E207).

## A note on the design's `pack` example

```
fn pack(src: []u8, dst: grow [64]u8) where src.len <= 64
  for b in src
    dst.push(b)        // proven in-bounds, no runtime check
```

`src.len <= 64` is not enough to prove the push. `dst` comes from the caller and can already hold elements. The proof needs `where dst.len + src.len <= dst.cap`, or a rule that grow parameters arrive empty (out-parameters). v0 compiles the example with a runtime capacity check on the push. The contract checker should settle which rule applies.

11. **The native backend favours predictability over speed.** Every local has a stack slot and expressions use callee-saved registers r4–r11, so calls never spill. It runs 1.6× to 7.9× more instructions than GCC -O2 on the same kernels ([benchmark](benchmark.md)).
12. **Bitfield writes are read-modify-write**, as in C. TickTrace uses the RP2350's atomic SET/CLR/XOR aliases (one store, interrupt-safe). Quoin should model those.
13. **Externs declare `cycles N`**, and anything calling an extern without it has no cycle bound.

## Not in v0

- The contract prover (the E102 `BoundUnproven` code is reserved for it). Intervals currently carry through constants, masks, conversions and loop variables. They do not carry through mutable locals.
- Comptime, `q15` fixed point, duration literals and a `wait` builtin, effects in signatures, `test`/`assert` blocks, the mandatory formatter, inline `asm`.
- Register arrays (`GPIO[n].CTRL`), read-only and write-1-to-clear registers, RP2040/RP2350 atomic set/clear aliases, register ownership across entry points.
- Cycle bounds, trace points and the TickTrace backend (stdlib bindings, `.equ`-generated register model, Studio driver).
- Multiple targets: `usize` is 32 bits and the stack figures assume a 32-bit Arm core. AArch64 (via C, stack bounds) and Hazard3 RISC-V are next, in that order.
- On the native backend: 64-bit integers, register allocation, frameless leaf functions, and atomic register aliases.

## Suggested next steps

1. Flash `examples/rp2350_blink.qn` (UF2) on a Pico 2, then add a hardware runner that reads DWT_CYCCNT and the painted stack. This calibrates the timing model and turns the cycle claims from model bounds into measured ones.
2. Close the code-quality gap measured by `quoin-bench`: register-allocate scalar locals, skip frames in leaf functions, hoist checks out of loops.
3. AArch64 bare-metal via C with stack bounds (QEMU `virt`), then Hazard3 RISC-V.
4. The tokens-to-correct-program harness ([benchmark.md](benchmark.md)): needs model API access, baseline toolchains and reference solutions.
5. Generate `regs` declarations from TickTrace's `.equ` files, so RP2350 has one register source of truth.
