# Pilot 2: short spec and hard tasks (2026-11-06)

Same harness, model (Claude Opus 5.5, effort medium), oracle (QEMU Cortex-M33: output, no trap, measured stack within budget) and 3 seeds per task and arm as [pilot 1](pilot-2026-11-06/report.md). The C numbers for the easy set are pilot 1's.

| Median tokens-to-correct (p90) | easy set, 10 tasks | hard set, 8 tasks |
| --- | ---: | ---: |
| C (platform notes, 423 tokens) | 992 (1,601) | 1,350 (1,843) |
| Quoin, full reference (5,261 tokens) | 6,299 (6,645) | 6,776 (7,189) |
| Quoin, brief v1 (1,978 tokens) | 2,921 (3,516) | 3,372 (7,104) |
| Quoin, brief v2 (2,061 tokens) | 2,900 (3,287) | 3,399 (3,677) |

| First-turn pass rate | easy | hard |
| --- | --- | --- |
| C | 30/30 | 24/24 |
| Quoin, full reference | 30/30 | 24/24 |
| Quoin, brief v1 | 27/30 | 18/24 |
| Quoin, brief v2 | 30/30 | 24/24 |

| Spend per 30 (easy) / 24 (hard) runs | easy | hard |
| --- | ---: | ---: |
| C | $0.41 | $0.46 |
| Quoin, full reference | $0.63 | $0.76 |
| Quoin, brief v2 | $0.45 | $0.61 |

Every run in every arm eventually passed.

## Findings

1. **The short spec halves Quoin's cost without losing correctness, once its gaps are fixed.** Brief v1's only failures were two documentation gaps: 8 programs wrote typed locals as `x: u32 = 0` (the brief showed `var x: T [= e]` but no example), and 1 used `and`. The compiler's `fix:` line resolved every one in the next turn (9 of 9). Brief v2 adds one example line and one sentence about `&&`/`||`/`!`, and passed 54 of 54 on the first turn. Caveat: v2 was revised after seeing these tasks. The fixes are general, but they should be confirmed on unseen tasks.
2. **C still wins, by 2.5× on the hard set and 2.9× on the easy set.** Hard median, Quoin brief v2 against C: the fixed prompt is 2,061 vs 423 tokens, and everything else is 1,328 vs 936 (mostly extra thinking). Since every run passed on the first turn, those two differences are the whole gap.
3. **The hard tasks were not hard for this model.** Not one C program out of 54 produced wrong output, trapped or overran its stack, on either set. The traps (shift by 32, signed overflow, wrapping counters, overflow detection, stack budget) did not catch Claude Opus 5.5. Retries are where Quoin is meant to save tokens, and they never happened.
4. **In dollars the gap is small.** The spec is cached, so it costs a twentieth of fresh input. Quoin with brief v2 cost 1.3× C on the hard set and 1.1× on the easy set.

## What would change the outcome

- Tasks where the incumbent language actually fails: larger drivers (hundreds of lines, several peripherals, interrupts and shared state), proven stack and cycle bounds as pass conditions, and real hardware.
- A model that makes mistakes on these tasks; a smaller model would show whether Quoin's checks turn into fewer turns.
- A smaller fixed prompt: every 100 tokens cut from the brief saves 100 tokens per turn.
- A metric decision: under raw tokens with the spec charged on every turn, a new language starts behind by its spec size. Cost-to-correct (cached tokens at their price) is nearly at parity already.
