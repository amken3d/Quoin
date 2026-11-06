## Where the tokens go

| Language | runs | spec tokens per turn | median turns | median total | median fixed prompt | median beyond the prompt | median output (incl. thinking) |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| quoin | 30 | 5261 | 1 | 6303 | 5261 | 1042 | 907 |
| c | 30 | 423 | 1 | 994 | 423 | 571 | 456 |

The same headline with the fixed prompt removed (failed runs still count at the ceiling):

| Language | median | p90 |
| --- | ---: | ---: |
| quoin | 1038 | 1384 |
| c | 569 | 1178 |

## Compiler diagnostics models hit

### quoin

No compile failures.

### c

No compile failures.

## Failed runs

None.
