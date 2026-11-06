# Benchmarking Quoin

The question the benchmark answers: does a model reach a correct, bounded firmware program with fewer tokens in Quoin than in TinyGo, pico-sdk C or Zig? Everything else here supports that one number or explains it.

## Headline: tokens-to-correct-program

For one task, one language, one model and one seed, count every token the harness spends until the program:

1. compiles,
2. passes its hardware test on a real board, and
3. meets its declared stack and cycle bounds on that board.

Counted tokens: every prompt token (including the fixed-size language spec file and the task statement, charged on every task), every completion token, every compiler-feedback token, and every retry. Report the **median and p90 over tasks**, per model, for each language, on the same tasks, models and harness. A program that never ran on hardware never counts as correct, and a run that exhausts its budget counts as a failure at the budget ceiling.

### Harness protocol

| Step | What happens | Tokens charged |
| --- | --- | --- |
| Prompt | system + language spec (fixed size per language) + task statement + oracle description | all input tokens |
| Generate | model writes a complete program | all output tokens |
| Compile | language's compiler; on failure its diagnostics go back verbatim | diagnostics as input tokens on the next turn |
| Flash and test | program runs on the board under the task's oracle; failures go back as structured text | failure report as input tokens |
| Bounds | measured worst-case stack (stack painting) and cycles (DWT_CYCCNT) compared with the task's declared bounds; violations go back | report as input tokens |
| Stop | all three pass, or the iteration or token budget runs out | |

The same harness drives every language. Only the spec file, compiler command and flash command differ. The language spec files are capped at the same token budget, so no language buys accuracy with a longer prompt. Token counts come from the provider's usage fields, never estimates.

### Suite

- The 49 TickTrace examples (`rp-asm/examples`), restated as tasks with a hardware oracle each.
- A private hold-out of 15 to 20 unseen driver tasks, never in any training or prompt material, also with hardware oracles.
- Every task has a reference solution in every baseline language, used for static token density and as proof that the oracle is satisfiable.
- Every task declares its stack and cycle bounds. Those bounds come from the reference solution's measured worst case plus a stated margin, so they are achievable in every language.

### Pilot

[bench/pilot](../bench/pilot) is a first, small version of this harness: 10 tasks, Quoin against freestanding C, one model, QEMU as an unofficial oracle (output plus measured stack). Its oracle is tested against deliberately broken programs, and its reference solutions pass in both languages. It needs `QUOIN_PILOT_API_KEY` to run.

**First result ([2026-11-06](../bench/results/pilot-2026-11-06/report.md), Claude Opus 5.5, effort medium, 3 seeds):** Quoin lost. The median tokens-to-correct was 6,299 for Quoin and 992 for C, a 5.7× gap, and C was cheaper on all 10 tasks. Both languages passed all 30 runs on the first turn with no compile errors, so the pilot measured no retries at all:

- 5,261 of Quoin's tokens per turn are the language spec, against 423 for C's platform notes. That is 83% of a typical Quoin run.
- Beyond the spec, Quoin runs still used 1.8× the tokens (1,038 vs 569 median). The programs are the same length; the model thinks about 3.6× longer before writing Quoin.
- In dollars the gap is 1.5× ($0.63 vs $0.41 for 30 runs), because the cached spec costs a twentieth of fresh input.
- A model with no prior exposure to Quoin wrote 30 correct programs out of 30 from the spec alone.

What it means: on tasks this easy, nothing is left for Quoin to save, and the spec is pure overhead charged on every turn. Quoin can only win where C solutions need several more turns than Quoin's. The next pilot needs tasks where that happens (subtle overflow and bounds bugs, tight stack budgets, cycle claims) and a much shorter spec.

### Task layout

```
bench/tasks/<name>/
  task.md          statement shown to the model (identical for every language)
  oracle.json      hardware test: pins/peripherals to drive and observe, expected trace, timeout
  bounds.json      {"stack_bytes": N, "cycles": {"<entry>": N}}
  ref/quoin/...    reference solutions, one per language
  ref/tinygo/...
  ref/c/...
  ref/zig/...
```

## Group 1: AI-interaction metrics

| Metric | Definition | Status |
| --- | --- | --- |
| First-pass compile rate | Share of first attempts that compile | needs the model harness |
| Iterations-to-green | Turns until all three conditions hold (median, p90) | needs the model harness and hardware |
| Compiler fix acceptance rate | Share of diagnostics with a `fix:` line whose suggested edit the model applies verbatim and that removes the diagnostic | needs the model harness |
| Static token density | Tokens in the reference solution relative to the Quoin reference, per task | needs reference solutions; token counts via the provider's count endpoint |

## Group 2: verification metrics

| Metric | Definition | Status |
| --- | --- | --- |
| Proof coverage without annotations | Operations that would need a runtime check (overflow, index, conversion, shift, push, precondition) that the checker discharges, over all such operations, in code without `where` clauses | **measured** (`quoin-bench`) |
| `where` clauses per 100 lines | Annotation burden | **measured** |
| False-reject rate | Correct programs (passing the hardware oracle in another language, faithfully ported) that the Quoin checker rejects | needs the ported corpus |
| Checker time p99, timeout rate | Per-file check time; the checker must never time out (target zero) | **measured**; v0 has no solver, so timeouts cannot occur |

## Group 3: bound tightness

Static bound divided by measured worst case, per entry point and per kernel. It must always be at least 1.0 (a value below 1.0 is a soundness bug and fails the suite). The target is under 1.3, with zero violations across the suite.

| Bound | How it is measured | Status |
| --- | --- | --- |
| Stack | Paint the stack and data stack, run, scan for the high-water mark | **measured on QEMU** for every kernel and test; the measurement is architectural, so QEMU's value equals the board's for the same input |
| Instructions | QEMU single-step trace, per function call | **measured on QEMU**; validates the path analysis |
| Cycles | DWT_CYCCNT around the function on an RP2350 running from SRAM | **pending hardware**. QEMU is not cycle-accurate, and the Cortex-M33 timing model is uncalibrated until this runs |

Measured worst case needs worst-case inputs. Each kernel states its worst-case input in a comment (for example, reverse-sorted input for insertion sort).

## Group 4: resources and performance

| Metric | Definition | Status |
| --- | --- | --- |
| Flash, RAM | `.text`; `.data` + `.bss` + proven stack | **measured** |
| Kernel cycles vs GCC -O2 | Same kernel, Quoin vs C compiled by `arm-none-eabi-gcc -O2`, DWT_CYCCNT on RP2350 | **proxy measured**: executed instructions of both on the same QEMU core; cycles pending hardware |
| ISR latency and jitter | Pin toggle at IRQ assertion to first handler instruction, logic analyzer or ETM trace; jitter = max − min | pending hardware; the static figure is the exception entry (12 cycles in the model) plus the handler's bound |
| Whole-program compile time | Median wall time of `quoinc` for the program | **measured** |
| Toolchain install size | Bytes on disk needed to go from source to a flashable image | **measured** for Quoin: `quoinc` (about 4.8 MB) + `as`/`ld` (about 3.9 MB) for the native RP2350 path; the C path also needs GCC (about 480 MB). Baselines pending |

## What is measured today

`go run ./cmd/quoin-bench` runs over `testdata/kernels` and prints the measured rows above; `-json` writes them for tracking. Each run that changes the numbers is committed under `bench/results/`. The first run ([2026-11-06](../bench/results/2026-11-06.md)) shows:

- **Bounds are sound and stack is exact.** On all five kernels the proven stack equals the measured stack, there are zero violations, and instruction bounds are within 1.07× except insertion sort (1.84×), whose triangular loop the per-loop bounds over-count.
- **Proof coverage is 55% overall.** One kernel (`pack`) is fully proven. The rest keep checks on slice indexing and on subtractions the interval analysis cannot see are safe.
- **Code quality is the biggest gap.** The native backend executes 1.6× to 7.9× the instructions of GCC -O2 and uses 1.4× to 4.4× the code. v0 keeps every local in a stack slot. Register allocation for locals, frameless leaf functions and hoisting of checks are the next steps, and this table measures them.

## Order of work

1. **RP2350 Thumb-2 with full claims** (in progress): calibrate the timing model with DWT_CYCCNT on a Pico 2, add a hardware runner (flash over SWD or BOOTSEL, observe GPIO and UART, read DWT and the painted stack), close the code-quality gap.
2. **AArch64 bare-metal via C with stack bounds**: QEMU `virt` with `qemu-system-aarch64`, GCC `-fstack-usage` for exact frames, no cycle claims.
3. **Hazard3 RISC-V**: RP2350's RISC-V cores, `qemu-system-riscv32` for functional tests, then a native backend with its own timing model.

## Prerequisites still missing

- A board and a runner: no result counts as correct without hardware.
- Model API access for the harness, plus a spend limit.
- Baseline toolchains (TinyGo, Zig, pico-sdk) and the reference solutions per task.
- The hold-out tasks, written and stored outside this repository.
