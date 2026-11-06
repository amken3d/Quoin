# Quoin: A No-Heap Language for AI-Written Firmware

Nov 5, 2026 · @Hriday Keni

> Drafted under the working name *Stackline*; the language is now Quoin. The text is otherwise the original design. What v0 implements, and where it departs from this document, is in [status.md](status.md).

## Problem

Firmware written by AI models is expensive in two ways current languages don't address: tokens spent on boilerplate, and tokens spent on retries after subtle memory or bounds bugs. C costs both. Go/TinyGo costs the first (`if err != nil` at every call) and relies on a GC. Rust costs the first through lifetime and trait annotations.

No existing language combines a provable no-heap memory model with a syntax and compiler designed for a model to write against. The no-heap languages (Goose, ZetZ, Zig, Austral) ignore AI; the AI-oriented languages (ll-lang, GlyphLang, Milo) ignore hardware.

## The idea

Combine Goose's memory model with ZetZ's verification, emit plain C, and shape every surface for a model to write against.

[Goose](https://aardappel.github.io/goose/) puts every dynamic value on a compiler-managed data stack, reclaimed at scope exit, with references validated by dependency inference instead of lifetime syntax. Its weakness: the data stack is unbounded in principle, so on a microcontroller a grow-only array is still a heap with nicer spelling.

[ZetZ](https://github.com/zetzit/zz) proves array bounds and arithmetic at compile time with symbolic execution over an SMT solver, and emits C99 for vendor toolchains. Its weakness: its memory story (checked tail pointers) is clunky and leaks C-isms.

The synergy: run ZetZ-style symbolic execution over Goose's growth sites and the compiler emits a static worst-case data-stack size per call tree, the way GNATstack bounds the call stack for Ada. "No heap" becomes a number the linker checks against the memory map.

## Design pillars

1. **Stack-only memory with a proven bound.** Fixed arrays `[N]T`, grow-only arrays on a scoped data stack, slices as views. No recursion (or bounded recursion). The compiler reports worst-case call-stack and data-stack bytes per entry point and ISR.
2. **One-token error propagation.** Error-union return types with `?` postfix (or Goose's `return E from f`). No exceptions, no unwinding.
3. **Inference inside, contracts at boundaries.** `x := f()` in bodies; full signatures with effects and `where` clauses at function boundaries, so a model reasons about a function from its first line.
4. **Hardware as types.** Volatile memory-mapped register structs with bitfields and enums; arbitrary-width ints (`u12`, `i7`), fixed-point (`q15`), explicit wrapping and saturating operators.
5. **Compile-time execution.** Zig-style comptime for lookup tables, pin maps, clock dividers.
6. **One way to do things.** Mandatory formatter, no operator overloading, no implicit conversions, no macros, no hidden control flow.
7. **Typed queues, no shared memory.** Goose's thread model mapped onto ISR-to-main handoff.
8. **Compiler as dialogue partner.** Structured errors with a concrete fix; when a proof fails, the compiler proposes the minimal `where` clause that would pass. Built-in `test` and `assert` blocks.
9. **Tokenizer-friendly surface.** Keywords models have seen billions of times (`fn`, `if`, `for`), indentation blocks, no exotic glyphs.

## What we take, what we drop

| Source | Keep | Drop |
| --- | --- | --- |
| Goose | Data stacks, `.=` reference binding, `return E from f`, fixed-mode ADTs, typed queues, whole-program compilation | Variable-mode ADTs, relative-offset references, graphics/physics packages |
| ZetZ | C99 emission for vendor toolchains, `where`/`model` contracts, SMT-proven indexing, `#if` as real expressions, no unwinding or coroutines | Checked tail pointers (superseded by data-stack arrays), raw C pointer interop beyond `extern fn` |
| Zig | Comptime, error unions, explicit overflow operators, no hidden control flow | Allocator passing (no heap means nothing to pass) |

## Example

A UART send and a bounded pack routine. The `where` clause is what lets the compiler prove `dst` never exceeds 64 bytes and fold that into the static stack bound.

```
fn send(u: Uart, buf: []u8) !Timeout
  for b in buf
    wait(u.SR.TXE, 1ms)?
    u.DR = b

fn pack(src: []u8, dst: grow [64]u8) where src.len <= 64
  for b in src
    dst.push(b)        // proven in-bounds, no runtime check

GPIOA.MODER.P5 = .Out  // register as type: no masks, no shifts
```

Compiler output is structured, not prose:

```
E102 pack.qn:7:5 BoundUnproven dst.push  max(src.len)=? 
  fix: add `where src.len <= 64` to pack
STACK main=412B isr_uart=96B data=1088B  total=1596B of 2048B
```

## Relationship to TickTrace

TickTrace is the reference hardware backend: on RP2350 the compiler emits calls into TickTrace routines instead of generic C, and the language becomes the front end for the Verify product.

| Layer | Role of TickTrace |
| --- | --- |
| Standard library | The 27 drivers become the first stdlib, bound as `extern fn` with signatures generated from the SDK |
| Register model | Peripheral structs generated from the same source as TickTrace's `.equ` definitions; one truth for the RP2350 memory map |
| Cycle bounds | Every TickTrace routine has a known cycle count, so the stack-bound analysis also emits a worst-case cycle count per entry point and ISR; `cycles<=N` becomes a checked contract |
| Trace | Compiler emits trace points mapped to source lines; Studio compares the static cycle bound with ETM/ITM measurement and reports mismatches in the structured error format |
| Tooling | Studio is the compiler driver and the agent-facing tool: compile, flash, trace, report |
| Corpus | The 49 examples ported to the language form the tokens-to-correct-program benchmark against TinyGo, with the assembly as ground truth for correctness and cycles; the book becomes the hardware tutorial |
| Inline asm | `asm` blocks in TickTrace style, with known rather than opaque cycle cost |
| Licensing | Permissive front end; output links TickTrace, so closed-source commercial use still requires the commercial TickTrace license |

The hardware layer is pluggable. TickTrace is the backend with cycle and trace support; C99 emission is the fallback for STM32 targets, which get stack bounds but no cycle bounds. RP2350's Hazard3 RISC-V cores are out of scope.

## Risks

| Risk | Mitigation |
| --- | --- |
| SMT solver timeouts stall an AI edit loop | Restrict contracts to a decidable fragment (linear arithmetic + bitvectors over bounded ints); the checker must answer or name the term it couldn't handle |
| Z3 as a toolchain dependency | Write a small custom checker for the restricted fragment; Z3 only for an optional deep-verify mode |
| Models have no training data for a new language | Compile to C, keep surface syntax within a few tokens of Zig/Go, ship a corpus of idiomatic drivers early |
| Not all algorithms fit stack-only memory | Pre-sized pools on the data stack; same answer embedded C already uses, now with a proven bound |
| One-person project risk (ZetZ was archived in 2022) | Smallest viable slice first; C emission means the output is usable even if the compiler stalls |

## Minimal viable slice

The smallest piece nobody else provides is the stack-size number. Build that first; contracts come after.

- [ ] Parser and type checker for the core: fixed arrays, grow arrays, slices, error unions, `?`
- [ ] Data-stack growth analysis producing a per-entry-point worst-case byte count
- [ ] C99 emission with volatile register structs; validate on RP2040 and an STM32 part
- [ ] Structured error format with a fix line; measure tokens-to-correct-program against TinyGo on 10 driver tasks
- [ ] Add `where` contracts and the bounded-fragment checker

Open question: indentation blocks or braces? Indentation saves about two tokens per block but complicates C-emission debugging and editor support.
