## Where the tokens go

| Language | runs | spec tokens per turn | median turns | median total | median fixed prompt | median beyond the prompt | median output (incl. thinking) |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| quoin | 30 | 4137 | 2 | 10745 | 8274 | 2471 | 1176 |
| c | 30 | 338 | 1 | 1126 | 338 | 788 | 661 |
| quoin-brief | 30 | 1655 | 1 | 2803 | 1655 | 1148 | 878 |

The same headline with the fixed prompt removed (failed runs still count at the ceiling):

| Language | median | p90 |
| --- | ---: | ---: |
| quoin | 2476 | 5038 |
| c | 788 | 974 |
| quoin-brief | 1060 | 1434 |

## Compiler diagnostics models hit

### quoin

| diagnostic | times | cleared on the next turn |
| --- | ---: | ---: |
| E106 Redeclared | 14 | 10 |
| E003 Unexpected | 11 | 4 |
| E101 Undefined | 3 | 3 |
| E117 Narrowing | 2 | 2 |
| E004 BadLiteral | 1 | 1 |
| E110 NotAssignable | 1 | 1 |
| E208 UnsupportedOnTarget | 1 | 1 |
| E112 UnknownField | 1 | 1 |

### c

No compile failures.

### quoin-brief

| diagnostic | times | cleared on the next turn |
| --- | ---: | ---: |
| E003 Unexpected | 4 | 4 |
| E106 Redeclared | 4 | 2 |
| E101 Undefined | 1 | 1 |
| E117 Narrowing | 1 | 1 |
| E208 UnsupportedOnTarget | 1 | 1 |

## Failed runs

- crc16 / quoin / seed 0: compile -> trap -> trap -> compile -> trap -> trap 
- ringbuf / quoin / seed 2: compile -> compile -> compile -> compile -> compile -> compile 
