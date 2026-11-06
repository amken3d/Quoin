# Pilot 3: an older, cheaper model (2026-11-06)

Pilots 1 and 2 used Claude Opus 5.5, which never failed in C, so there were no retries for Quoin to save. This pilot asks whether a weaker model, one that does make mistakes, gains from Quoin's checks.

Same harness, tasks, oracle (QEMU Cortex-M33: output, no trap, measured stack within budget) and 3 seeds per task and arm as [pilot 2](pilot2-summary.md). The model is Claude Haiku 4.5, the previous-generation Haiku ($1 / $5 per million input / output tokens), run without extended thinking (it has no effort setting). Arms: C, Quoin with brief v2, and Quoin with the full reference. 162 runs cost $2.28.

| Median tokens-to-correct (p90) | easy set, 10 tasks | hard set, 8 tasks |
| --- | ---: | ---: |
| C (platform notes, 338 tokens) | 1,126 (1,312) | 1,706 (5,457) |
| Quoin, brief v2 (1,655 tokens) | 2,714 (4,744) | 10,722 (150,000) |
| Quoin, full reference (4,137 tokens) | 10,896 (21,531) | 91,062 (150,000) |

Prompt sizes are smaller than in pilot 2 because this model's tokenizer counts the same text as fewer tokens. A failed run counts at the 150,000-token ceiling.

| Runs | solved | right on the first turn | solved after a retry | failed |
| --- | --- | --- | --- | --- |
| C, easy | 30/30 | 28 | 2 | 0 |
| C, hard | 24/24 | 18 | 6 | 0 |
| Quoin brief, easy | 30/30 | 19 | 11 | 0 |
| Quoin brief, hard | 16/24 | 5 | 11 | 8 |
| Quoin full, easy | 28/30 | 10 | 18 | 2 |
| Quoin full, hard | 14/24 | 1 | 13 | 10 |

| Spend | easy (30 runs) | hard (24 runs) |
| --- | ---: | ---: |
| C | $0.12 | $0.19 |
| Quoin, brief v2 | $0.25 | $0.71 |
| Quoin, full reference | $0.29 | $0.72 |

Per-set reports: [easy](pilot3-haiku45-easy-2026-11-06/report.md), [hard](pilot3-haiku45-hard-2026-11-06/report.md); the token split and every compiler diagnostic are in each directory's `analysis.md`.

## Findings

1. **The weaker model does make mistakes in C, and fixes them.** 8 of 54 C runs needed a retry (on the hard set: 10 wrong outputs and 2 stack overruns across all turns), and every C run was solved, usually one turn later.
2. **It makes many more mistakes in Quoin, and they cost more to fix.** On the hard set, Quoin with the brief solved 16 of 24 runs and got 5 right on the first turn; C solved 24 and got 18. The per-task geometric mean of Quoin's token cost relative to C is 10.8× with the brief and 30× with the full reference on the hard set, and 2.4× and 9.4× on the easy set. In dollars, Quoin with the brief cost 3.7× C on the hard set and 2.1× on the easy set.
3. **The full reference is worse than the brief.** More documentation meant more compile errors (33% first-turn compile on the easy set, 17% on the hard set) and more tokens on every task.
4. **Most compile errors are C and Go habits Quoin rejects.** Counting turns that failed to compile (119 across both sets):

   | Pattern | turns |
   | --- | ---: |
   | A name declared twice, almost always `var i: u32 = 0` followed by `for i in ...` | 51 |
   | A `catch` block used as a value | 12 |
   | The I/O `extern` declarations left out (`input_byte` undefined); every C program declared them | 9 |
   | `var x := e` | 6 |
   | A typed local without `var` | 6 |
   | A value returned from `main` | 4 |
   | A variable named like a type (`i2`) | 1 |

   For the most common one, the compiler's hint is unhelpful: `fix: pick a new name; Quoin has no shadowing`, when the fix is to delete the earlier `var i`, because `for` declares its own variable.
5. **Runtime traps caught real bugs but gave the model nothing to act on.** Quoin's checks stopped 55 turns on an overflow or an out-of-range read. The feedback says only "a runtime check failed", with no location and no kind of check, and the model often repeated the same trap until it ran out of turns. In C a bug like that shows up as wrong output, and the feedback names the input with the expected and actual output, which the model could use.

## What it means

The hypothesis was that a weaker model, one that makes mistakes, would gain from Quoin's checks. On these tasks it did the opposite: the cost of an unfamiliar language grew as the model got weaker, and Quoin's checks never got the chance to pay off.

The data points at changes to test, on tasks other than these:

- **Trap reports that say where and what.** For example, `trap: overflow in + at prog.qn:14:9 (a = 32767, b = 1)`. A located, typed trap is the most specific feedback a model can get, and it is the advantage Quoin is supposed to have over C; today it is thrown away in the last step.
- **Diagnostics that name the habit.** E106 on a `for` loop should say to delete the earlier declaration. A `catch` block used as a value and `var x := e` deserve the same kind of targeted hint.
- **Reconsider rules that only cost.** Accepting `var x := e`, or letting `for` reuse a variable declared just before it, would remove most compile failures without weakening any guarantee.

Caveats: one model, run without extended thinking; the brief was revised after pilot 2's failures with a different model; tasks and references come from the same author as the language; QEMU, not hardware.
