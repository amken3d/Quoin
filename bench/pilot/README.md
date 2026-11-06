# Tokens-to-correct pilot

A small, honest first measurement of the benchmark's headline metric ([docs/benchmark.md](../../docs/benchmark.md)): does a model reach a correct program in fewer tokens writing Quoin than writing C, on the same tasks, with the same model and harness?

## Protocol

- **Tasks**: two sets, each statement identical for every language and checked against a Go reference implementation.
  - `easy` ([tasks.go](tasks.go)): 10 small firmware-style tasks (CRC, packet framing with error codes, a bounded FIFO, debouncing, a moving average, a clamped PID step, a bitfield control register, saturating audio mixing, LEB128 decoding, RLE).
  - `hard` ([hard.go](hard.go)): 8 tasks built around traps that bite after a program compiles: saturating Q15 accumulation (signed overflow), COBS framing overruns, 32-bit-wide bit fields (shift by 32), integer square root near 2^32, a wrapping microsecond counter, packet medians under a 256-byte stack budget, decimal parsing with u32 overflow, and a sliding median of signed samples.
- **Arms**: Quoin with the full reference ([docs/language.md](../../docs/language.md), about 5.3k tokens), Quoin with the short brief ([docs/brief.md](../../docs/brief.md), about 2k tokens with platform notes), and freestanding C99 (`arm-none-eabi-gcc -O2`, about 0.4k tokens of platform notes). All link against the same QEMU runtime and input/output functions, so the oracle cannot favour any of them.
- **Prompt**: the system prompt is the language spec plus platform notes, charged on every turn.
- **Loop**: the model writes a complete program; the harness compiles it and, if that works, runs it on QEMU's Cortex-M33 against every edge-case input plus 3 fresh random inputs. The first failure goes back verbatim (compiler diagnostics, or the failing input with expected and actual output, a trap, a timeout or a stack-budget overrun). Up to 6 turns.
- **Pass**: output matches on every input, `main` returns without trapping or faulting, and the measured stack (call stack plus Quoin's data stack) is within the task's budget.
- **Tokens**: every input, cache-write, cache-read and output token reported by the API, summed over all turns. A run that fails counts at the token ceiling (150,000). Per task, the median over seeds; then the median and p90 over tasks.
- **Model**: Claude Opus 5.5, adaptive thinking (always on), effort `medium` for every arm. No refusal fallback, so every run stays on one model; a refusal counts as a failure.

Inputs are fresh on every attempt and loaded into memory by QEMU, so a program cannot hardcode answers it was shown in feedback.

## Running

```
go run . -mode selftest            # no API calls: references for both languages through the full pipeline
go test .                          # the oracle rejects wrong output, traps, timeouts, faults and stack overruns
go run . -mode run -tasks crc16 -seeds 1          # smoke test against the API
go run . -mode run -seeds 3 -max-usd 60           # easy set: 10 tasks x 2 languages x 3 seeds
go run . -mode run -set hard -langs quoin,quoin-brief,c -seeds 3
go run . -mode count -langs quoin,quoin-brief,c   # system-prompt sizes in tokens
```

The run reads the key from `QUOIN_PILOT_API_KEY` (not `ANTHROPIC_API_KEY`, so it cannot interfere with a Claude Code session). Results go to `../results/pilot-<date>/` (or `-out`): `report.md`, `summary.json`, and `runs.jsonl` with every reply, program and feedback message for audit. `go run . -mode report -out DIR` rebuilds a report from those files; `python3 analyze.py DIR` splits tokens into the fixed prompt and the rest and lists the compiler diagnostics models hit. With Claude Opus 5.5, a 60-run pilot has cost about $1 so far, because most runs pass on the first turn; `-max-usd` caps it.

## What this pilot cannot tell you

- **Not hardware.** QEMU is the oracle; by the benchmark's rule nothing here counts as correct until it runs on a board.
- **No cycle bounds.** QEMU is not cycle-accurate; tasks only carry a stack budget.
- **Small.** 10 tasks and 3 seeds per arm give a direction, not a confidence interval worth quoting.
- **The tasks and references were written by the same author as Quoin.** A hold-out set written by someone else is needed before drawing conclusions.
- **Two arms only.** TinyGo, Zig and pico-sdk C come next.
