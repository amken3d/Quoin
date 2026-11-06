# Tokens-to-correct pilot

Model `claude-opus-5-5`, effort `medium`, 3 seeds per task and language, up to 6 turns per run, token ceiling 150000 per run (a failed run counts as the ceiling), 3 fresh random inputs per test. Oracle: QEMU mps2-an505 (Cortex-M33), output match plus measured stack within the task budget. **Not hardware: by the benchmark's own rule these results are provisional.** Cycle bounds are not part of this pilot.

## Headline

| Language | median tokens-to-correct | p90 | success | first-pass compile | first-pass correct | turns to green (median) | spent |
| --- | ---: | ---: | --- | --- | --- | ---: | ---: |
| quoin | 6299 | 6645 | 100% (30/30) | 100% (30/30) | 100% (30/30) | 1.0 | $0.63 |
| c | 992 | 1601 | 100% (30/30) | 100% (30/30) | 100% (30/30) | 1.0 | $0.41 |

The median and p90 are over tasks, of each task's median over seeds.

## Per task (median tokens over seeds; successes)

| task | quoin | c | quoin / c |
| --- | ---: | ---: | ---: |
| crc16 | 6190 (3/3) | 995 (3/3) | 6.22 |
| packets | 6619 (3/3) | 1250 (3/3) | 5.30 |
| ringbuf | 6356 (3/3) | 897 (3/3) | 7.09 |
| debounce | 6060 (3/3) | 958 (3/3) | 6.33 |
| movavg | 6088 (3/3) | 897 (3/3) | 6.79 |
| pid | 6202 (3/3) | 1227 (3/3) | 5.05 |
| ctrlreg | 7020 (3/3) | 1742 (3/3) | 4.03 |
| mix | 6386 (3/3) | 989 (3/3) | 6.46 |
| varint | 6645 (3/3) | 1601 (3/3) | 4.15 |
| rle | 6242 (3/3) | 910 (3/3) | 6.86 |

Geometric mean of the per-task ratio quoin/c: **5.72** (below 1 means quoin needed fewer tokens). quoin was cheaper on 0 of 10 tasks.

## Where attempts failed (all turns, all runs)

| Language | compile | wrong output | trap | timeout | fault | stack | no code | refusal |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| quoin | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| c | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |

## Fixed prompt cost

System prompt (spec + platform notes), charged on every turn:

- quoin: 5261 tokens
- c: 423 tokens

## Static token density (reference solutions)

| task | quoin | c |
| --- | ---: | ---: |
| crc16 | 235 | 261 |
| packets | 305 | 321 |
| ringbuf | 277 | 287 |
| debounce | 217 | 237 |
| movavg | 206 | 195 |
| pid | 299 | 313 |
| ctrlreg | 426 | 417 |
| mix | 204 | 226 |
| varint | 290 | 293 |
| rle | 207 | 207 |
