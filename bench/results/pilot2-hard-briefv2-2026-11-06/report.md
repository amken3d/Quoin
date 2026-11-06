# Tokens-to-correct pilot

Model `claude-opus-5-5`, effort `medium`, 3 seeds per task and language, up to 6 turns per run, token ceiling 150000 per run (a failed run counts as the ceiling), 3 fresh random inputs per test. Oracle: QEMU mps2-an505 (Cortex-M33), output match plus measured stack within the task budget. **Not hardware: by the benchmark's own rule these results are provisional.** Cycle bounds are not part of this pilot.

## Headline

| Language | median tokens-to-correct | p90 | success | first-pass compile | first-pass correct | turns to green (median) | spent |
| --- | ---: | ---: | --- | --- | --- | ---: | ---: |
| quoin-brief | 3399 | 3677 | 100% (24/24) | 100% (24/24) | 100% (24/24) | 1.0 | $0.61 |

The median and p90 are over tasks, of each task's median over seeds.

## Per task (median tokens over seeds; successes)

| task | quoin-brief |
| --- | ---: |
| q15fir | 3677 (3/3) |
| cobs | 3595 (3/3) |
| bitstream | 3465 (3/3) |
| isqrt | 3148 (3/3) |
| tsdelta | 2886 (3/3) |
| median | 3444 (3/3) |
| atoi | 3354 (3/3) |
| median5 | 3322 (3/3) |

## Where attempts failed (all turns, all runs)

| Language | compile | wrong output | trap | timeout | fault | stack | no code | refusal |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| quoin-brief | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |

## Fixed prompt cost

System prompt (spec + platform notes), charged on every turn:

- quoin-brief: 2061 tokens

## Static token density (reference solutions)

| task | quoin-brief |
| --- | ---: |
| q15fir | 335 |
| cobs | 362 |
| bitstream | 318 |
| isqrt | 315 |
| tsdelta | 316 |
| median | 331 |
| atoi | 352 |
| median5 | 324 |
