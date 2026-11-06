# Tokens-to-correct pilot

Model `claude-haiku-4-5`, effort `n/a`, 3 seeds per task and language, up to 6 turns per run, token ceiling 150000 per run (a failed run counts as the ceiling), 3 fresh random inputs per test. Oracle: QEMU mps2-an505 (Cortex-M33), output match plus measured stack within the task budget. **Not hardware: by the benchmark's own rule these results are provisional.** Cycle bounds are not part of this pilot.

## Headline

| Language | median tokens-to-correct | p90 | success | first-pass compile | first-pass correct | turns to green (median) | spent |
| --- | ---: | ---: | --- | --- | --- | ---: | ---: |
| c | 1706 | 5457 | 100% (24/24) | 100% (24/24) | 75% (18/24) | 1.0 | $0.19 |
| quoin-brief | 10722 | 150000 | 67% (16/24) | 46% (11/24) | 21% (5/24) | 3.0 | $0.71 |
| quoin | 91062 | 150000 | 58% (14/24) | 17% (4/24) | 4% (1/24) | 3.0 | $0.72 |

The median and p90 are over tasks, of each task's median over seeds.

## Per task (median tokens over seeds; successes)

| task | c | quoin-brief | quoin | quoin-brief / c | quoin / c |
| --- | ---: | ---: | ---: | ---: | ---: |
| q15fir | 1377 (3/3) | 2515 (2/3) | 150000 (1/3) | 1.83 | 108.93 |
| cobs | 2186 (3/3) | 150000 (0/3) | 32123 (3/3) | 68.62 | 14.69 |
| bitstream | 1334 (3/3) | 10876 (3/3) | 11863 (3/3) | 8.15 | 8.89 |
| isqrt | 5457 (3/3) | 150000 (1/3) | 150000 (1/3) | 27.49 | 27.49 |
| tsdelta | 1057 (3/3) | 3101 (3/3) | 150000 (1/3) | 2.93 | 141.91 |
| median | 2035 (3/3) | 150000 (1/3) | 150000 (0/3) | 73.71 | 73.71 |
| atoi | 2887 (3/3) | 10542 (3/3) | 31006 (3/3) | 3.65 | 10.74 |
| median5 | 1231 (3/3) | 10569 (3/3) | 18108 (2/3) | 8.59 | 14.71 |

- quoin-brief vs c: geometric mean of the per-task ratio **10.84** (below 1 means fewer tokens than C); cheaper on 0 of 8 tasks.
- quoin vs c: geometric mean of the per-task ratio **29.95** (below 1 means fewer tokens than C); cheaper on 0 of 8 tasks.

## Where attempts failed (all turns, all runs)

| Language | compile | wrong output | trap | timeout | fault | stack | no code | refusal |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| c | 0 | 10 | 0 | 0 | 0 | 2 | 0 | 0 |
| quoin-brief | 31 | 22 | 18 | 0 | 0 | 5 | 0 | 0 |
| quoin | 44 | 15 | 29 | 0 | 0 | 2 | 0 | 0 |

## Fixed prompt cost

System prompt (spec + platform notes), charged on every turn:

- c: 338 tokens
- quoin-brief: 1655 tokens
- quoin: 4137 tokens

## Static token density (reference solutions)

| task | c | quoin-brief | quoin |
| --- | ---: | ---: | ---: |
| q15fir | 408 | 288 | 288 |
| cobs | 354 | 321 | 321 |
| bitstream | 286 | 272 | 272 |
| isqrt | 317 | 268 | 268 |
| tsdelta | 302 | 276 | 276 |
| median | 324 | 292 | 292 |
| atoi | 333 | 311 | 311 |
| median5 | 286 | 287 | 287 |
