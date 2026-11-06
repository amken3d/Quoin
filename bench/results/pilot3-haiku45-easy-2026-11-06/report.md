# Tokens-to-correct pilot

Model `claude-haiku-4-5`, effort `n/a`, 3 seeds per task and language, up to 6 turns per run, token ceiling 150000 per run (a failed run counts as the ceiling), 3 fresh random inputs per test. Oracle: QEMU mps2-an505 (Cortex-M33), output match plus measured stack within the task budget. **Not hardware: by the benchmark's own rule these results are provisional.** Cycle bounds are not part of this pilot.

## Headline

| Language | median tokens-to-correct | p90 | success | first-pass compile | first-pass correct | turns to green (median) | spent |
| --- | ---: | ---: | --- | --- | --- | ---: | ---: |
| c | 1126 | 1312 | 100% (30/30) | 100% (30/30) | 93% (28/30) | 1.0 | $0.12 |
| quoin-brief | 2714 | 4744 | 100% (30/30) | 73% (22/30) | 63% (19/30) | 1.0 | $0.25 |
| quoin | 10896 | 21531 | 93% (28/30) | 33% (10/30) | 33% (10/30) | 2.0 | $0.29 |

The median and p90 are over tasks, of each task's median over seeds.

## Per task (median tokens over seeds; successes)

| task | c | quoin-brief | quoin | quoin-brief / c | quoin / c |
| --- | ---: | ---: | ---: | ---: | ---: |
| crc16 | 820 (3/3) | 2936 (3/3) | 15437 (2/3) | 3.58 | 18.83 |
| packets | 1120 (3/3) | 2179 (3/3) | 10601 (3/3) | 1.95 | 9.47 |
| ringbuf | 1062 (3/3) | 2447 (3/3) | 27513 (2/3) | 2.30 | 25.91 |
| debounce | 1131 (3/3) | 2422 (3/3) | 9263 (3/3) | 2.14 | 8.19 |
| movavg | 1039 (3/3) | 2237 (3/3) | 4715 (3/3) | 2.15 | 4.54 |
| pid | 968 (3/3) | 4744 (3/3) | 21531 (3/3) | 4.90 | 22.24 |
| ctrlreg | 1287 (3/3) | 2809 (3/3) | 11191 (3/3) | 2.18 | 8.70 |
| mix | 1221 (3/3) | 5160 (3/3) | 15037 (3/3) | 4.23 | 12.32 |
| varint | 2929 (3/3) | 2855 (3/3) | 10152 (3/3) | 0.97 | 3.47 |
| rle | 1312 (3/3) | 2620 (3/3) | 5141 (3/3) | 2.00 | 3.92 |

- quoin-brief vs c: geometric mean of the per-task ratio **2.41** (below 1 means fewer tokens than C); cheaper on 1 of 10 tasks.
- quoin vs c: geometric mean of the per-task ratio **9.43** (below 1 means fewer tokens than C); cheaper on 0 of 10 tasks.

## Where attempts failed (all turns, all runs)

| Language | compile | wrong output | trap | timeout | fault | stack | no code | refusal |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| c | 0 | 2 | 0 | 0 | 0 | 0 | 0 | 0 |
| quoin-brief | 10 | 5 | 3 | 0 | 0 | 0 | 0 | 0 |
| quoin | 34 | 4 | 5 | 0 | 0 | 0 | 0 | 0 |

## Fixed prompt cost

System prompt (spec + platform notes), charged on every turn:

- c: 338 tokens
- quoin-brief: 1655 tokens
- quoin: 4137 tokens

## Static token density (reference solutions)

| task | c | quoin-brief | quoin |
| --- | ---: | ---: | ---: |
| crc16 | 233 | 201 | 201 |
| packets | 288 | 265 | 265 |
| ringbuf | 262 | 243 | 243 |
| debounce | 220 | 189 | 189 |
| movavg | 176 | 173 | 173 |
| pid | 295 | 267 | 267 |
| ctrlreg | 362 | 352 | 352 |
| mix | 211 | 169 | 169 |
| varint | 264 | 258 | 258 |
| rle | 188 | 178 | 178 |
