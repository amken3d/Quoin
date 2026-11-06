# Tokens-to-correct pilot

Model `claude-opus-5-5`, effort `medium`, 3 seeds per task and language, up to 6 turns per run, token ceiling 150000 per run (a failed run counts as the ceiling), 3 fresh random inputs per test. Oracle: QEMU mps2-an505 (Cortex-M33), output match plus measured stack within the task budget. **Not hardware: by the benchmark's own rule these results are provisional.** Cycle bounds are not part of this pilot.

## Headline

| Language | median tokens-to-correct | p90 | success | first-pass compile | first-pass correct | turns to green (median) | spent |
| --- | ---: | ---: | --- | --- | --- | ---: | ---: |
| quoin-brief | 2921 | 3516 | 100% (30/30) | 90% (27/30) | 90% (27/30) | 1.0 | $0.54 |

The median and p90 are over tasks, of each task's median over seeds.

## Per task (median tokens over seeds; successes)

| task | quoin-brief |
| --- | ---: |
| crc16 | 2828 (3/3) |
| packets | 3092 (3/3) |
| ringbuf | 2838 (3/3) |
| debounce | 2446 (3/3) |
| movavg | 5582 (3/3) |
| pid | 2986 (3/3) |
| ctrlreg | 3516 (3/3) |
| mix | 2856 (3/3) |
| varint | 3208 (3/3) |
| rle | 2756 (3/3) |

## Where attempts failed (all turns, all runs)

| Language | compile | wrong output | trap | timeout | fault | stack | no code | refusal |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| quoin-brief | 3 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |

## Fixed prompt cost

System prompt (spec + platform notes), charged on every turn:

- quoin-brief: 1978 tokens

## Static token density (reference solutions)

| task | quoin-brief |
| --- | ---: |
| crc16 | 235 |
| packets | 305 |
| ringbuf | 277 |
| debounce | 217 |
| movavg | 206 |
| pid | 299 |
| ctrlreg | 426 |
| mix | 204 |
| varint | 290 |
| rle | 207 |
