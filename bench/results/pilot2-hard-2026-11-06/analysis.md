## Where the tokens go

| Language | runs | spec tokens per turn | median turns | median total | median fixed prompt | median beyond the prompt | median output (incl. thinking) |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| quoin | 24 | 5261 | 1 | 6812 | 5261 | 1552 | 1382 |
| c | 24 | 423 | 1 | 1359 | 423 | 936 | 774 |
| quoin-brief | 24 | 1978 | 1 | 3474 | 1978 | 1496 | 1292 |

The same headline with the fixed prompt removed (failed runs still count at the ceiling):

| Language | median | p90 |
| --- | ---: | ---: |
| quoin | 1515 | 1928 |
| c | 926 | 1420 |
| quoin-brief | 1394 | 3148 |

## Compiler diagnostics models hit

### quoin

No compile failures.

### c

No compile failures.

### quoin-brief

No compile failures.

## Failed runs

None.
