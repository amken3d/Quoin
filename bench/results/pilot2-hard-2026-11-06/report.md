# Tokens-to-correct pilot

Model `claude-opus-5-5`, effort `medium`, 3 seeds per task and language, up to 6 turns per run, token ceiling 150000 per run (a failed run counts as the ceiling), 3 fresh random inputs per test. Oracle: QEMU mps2-an505 (Cortex-M33), output match plus measured stack within the task budget. **Not hardware: by the benchmark's own rule these results are provisional.** Cycle bounds are not part of this pilot.

## Headline

| Language | median tokens-to-correct | p90 | success | first-pass compile | first-pass correct | turns to green (median) | spent |
| --- | ---: | ---: | --- | --- | --- | ---: | ---: |
| quoin | 6776 | 7189 | 100% (24/24) | 100% (24/24) | 100% (24/24) | 1.0 | $0.76 |
| quoin-brief | 3372 | 7104 | 100% (24/24) | 75% (18/24) | 75% (18/24) | 1.0 | $0.70 |
| c | 1350 | 1843 | 100% (24/24) | 100% (24/24) | 100% (24/24) | 1.0 | $0.46 |

The median and p90 are over tasks, of each task's median over seeds.

## Per task (median tokens over seeds; successes)

| task | quoin | quoin-brief | c | quoin / c | quoin-brief / c |
| --- | ---: | ---: | ---: | ---: | ---: |
| q15fir | 7189 (3/3) | 3579 (3/3) | 1583 (3/3) | 4.54 | 2.26 |
| cobs | 7167 (3/3) | 3591 (3/3) | 1843 (3/3) | 3.89 | 1.95 |
| bitstream | 6915 (3/3) | 7104 (3/3) | 1262 (3/3) | 5.48 | 5.63 |
| isqrt | 6731 (3/3) | 3239 (3/3) | 1245 (3/3) | 5.41 | 2.60 |
| tsdelta | 6643 (3/3) | 3045 (3/3) | 1263 (3/3) | 5.26 | 2.41 |
| median | 6768 (3/3) | 3470 (3/3) | 1614 (3/3) | 4.19 | 2.15 |
| atoi | 6784 (3/3) | 3273 (3/3) | 1436 (3/3) | 4.72 | 2.28 |
| median5 | 6534 (3/3) | 3198 (3/3) | 1067 (3/3) | 6.12 | 3.00 |

- quoin vs c: geometric mean of the per-task ratio **4.90** (below 1 means fewer tokens than C); cheaper on 0 of 8 tasks.
- quoin-brief vs c: geometric mean of the per-task ratio **2.63** (below 1 means fewer tokens than C); cheaper on 0 of 8 tasks.

## Where attempts failed (all turns, all runs)

| Language | compile | wrong output | trap | timeout | fault | stack | no code | refusal |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| quoin | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| quoin-brief | 6 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| c | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |

## Fixed prompt cost

System prompt (spec + platform notes), charged on every turn:

- quoin: 5261 tokens
- quoin-brief: 1978 tokens
- c: 423 tokens

## Static token density (reference solutions)

| task | quoin | quoin-brief | c |
| --- | ---: | ---: | ---: |
| q15fir | 335 | 335 | 456 |
| cobs | 362 | 362 | 390 |
| bitstream | 318 | 318 | 322 |
| isqrt | 315 | 315 | 356 |
| tsdelta | 316 | 316 | 333 |
| median | 331 | 331 | 360 |
| atoi | 352 | 352 | 366 |
| median5 | 324 | 324 | 313 |
