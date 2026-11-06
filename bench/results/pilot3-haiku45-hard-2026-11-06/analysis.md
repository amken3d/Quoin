## Where the tokens go

| Language | runs | spec tokens per turn | median turns | median total | median fixed prompt | median beyond the prompt | median output (incl. thinking) |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| quoin | 24 | 4137 | 5 | 32524 | 20685 | 12186 | 3070 |
| c | 24 | 338 | 1 | 1512 | 338 | 1174 | 975 |
| quoin-brief | 24 | 1655 | 4 | 14132 | 6620 | 7512 | 2432 |

The same headline with the fixed prompt removed (failed runs still count at the ceiling):

| Language | median | p90 |
| --- | ---: | ---: |
| quoin | 82788 | 150000 |
| c | 1368 | 4443 |
| quoin-brief | 5758 | 150000 |

## Compiler diagnostics models hit

### quoin

| diagnostic | times | cleared on the next turn |
| --- | ---: | ---: |
| E003 Unexpected | 17 | 11 |
| E106 Redeclared | 15 | 10 |
| E101 Undefined | 6 | 6 |
| E125 UnusedValue | 4 | 4 |
| E111 MissingReturn | 4 | 4 |
| E208 UnsupportedOnTarget | 3 | 3 |
| E104 TypeMismatch | 3 | 3 |
| E004 BadLiteral | 1 | 1 |
| E117 Narrowing | 1 | 1 |

### c

No compile failures.

### quoin-brief

| diagnostic | times | cleared on the next turn |
| --- | ---: | ---: |
| E106 Redeclared | 19 | 10 |
| E003 Unexpected | 10 | 8 |
| E104 TypeMismatch | 2 | 2 |
| E203 ExternStackUnknown | 1 | 1 |
| E101 Undefined | 1 | 1 |

## Failed runs

- q15fir / quoin / seed 0: compile -> compile -> trap -> trap -> trap -> trap 
- cobs / quoin-brief / seed 0: wrong -> wrong -> wrong -> wrong -> wrong -> wrong 
- isqrt / quoin-brief / seed 0: trap -> trap -> trap -> trap -> trap -> trap 
- median / quoin / seed 0: compile -> compile -> compile -> compile -> stack -> stack 
- tsdelta / quoin / seed 0: trap -> compile -> trap -> trap -> trap -> trap 
- median5 / quoin / seed 0: compile -> compile -> trap -> trap -> trap -> trap 
- q15fir / quoin / seed 1: compile -> trap -> compile -> trap -> trap -> compile 
- cobs / quoin-brief / seed 1: wrong -> compile -> wrong -> wrong -> wrong -> wrong 
- isqrt / quoin / seed 1: compile -> trap -> trap -> trap -> trap -> trap 
- isqrt / quoin-brief / seed 1: trap -> trap -> trap -> trap -> trap -> trap 
- tsdelta / quoin / seed 1: compile -> trap -> trap -> trap -> wrong -> trap 
- median / quoin-brief / seed 1: compile -> compile -> compile -> compile -> stack -> stack 
- median / quoin / seed 1: compile -> compile -> compile -> compile -> compile -> compile 
- q15fir / quoin-brief / seed 2: compile -> stack -> trap -> trap -> compile -> trap 
- cobs / quoin-brief / seed 2: compile -> wrong -> wrong -> wrong -> wrong -> wrong 
- isqrt / quoin / seed 2: compile -> trap -> trap -> trap -> compile -> wrong 
- median / quoin / seed 2: compile -> compile -> compile -> wrong -> compile -> compile 
- median / quoin-brief / seed 2: compile -> compile -> compile -> wrong -> compile -> compile 
