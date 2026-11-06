#!/usr/bin/env python3
"""Secondary analysis of a pilot run (runs.jsonl + summary.json).

Splits each run's tokens into the fixed system prompt (spec charged on every
turn) and everything else (task, replies including thinking, feedback), and
lists the compiler diagnostics models hit and whether the next turn cleared
them. Usage: analyze.py <results dir>
"""
import json
import re
import statistics
import sys
from collections import Counter, defaultdict

d = sys.argv[1]
meta = json.load(open(f"{d}/summary.json"))["meta"]
spec = meta.get("spec_tokens") or {}
runs = [json.loads(l) for l in open(f"{d}/runs.jsonl")]
ceiling = meta["ceiling"]

def med(xs):
    return statistics.median(xs) if xs else float("nan")

def p90(xs):
    if not xs:
        return float("nan")
    s = sorted(xs)
    k = max(0, -(-9 * len(s) // 10) - 1)
    return s[k]

print("## Where the tokens go\n")
print("| Language | runs | spec tokens per turn | median turns | median total | median fixed prompt | median beyond the prompt | median output (incl. thinking) |")
print("| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |")
langs = sorted({r["lang"] for r in runs}, key=lambda x: (x != "quoin", x))
beyond_by = {}
for lang in langs:
    rs = [r for r in runs if r["lang"] == lang]
    s = spec.get(lang, 0)
    turns = [r["iters"] for r in rs]
    totals = [r["tokens"] for r in rs]
    fixed = [s * r["iters"] for r in rs]
    beyond = [r["tokens"] - s * r["iters"] for r in rs]
    outs = [r["usage"]["Out"] for r in rs]
    beyond_by[lang] = rs
    print(f"| {lang} | {len(rs)} | {s} | {med(turns):.0f} | {med(totals):.0f} | {med(fixed):.0f} | {med(beyond):.0f} | {med(outs):.0f} |")

print("\nThe same headline with the fixed prompt removed (failed runs still count at the ceiling):\n")
print("| Language | median | p90 |")
print("| --- | ---: | ---: |")
for lang in langs:
    s = spec.get(lang, 0)
    per = defaultdict(list)
    for r in beyond_by[lang]:
        per[r["task"]].append(r["tokens"] - s * r["iters"] if r["success"] else ceiling)
    meds = [med(v) for v in per.values()]
    print(f"| {lang} | {med(meds):.0f} | {p90(meds):.0f} |")

print("\n## Compiler diagnostics models hit\n")
code_re = re.compile(r"^(E\d{3}) \S+ (\w+)", re.M)
for lang in langs:
    rs = [r for r in runs if r["lang"] == lang]
    hits, cleared = Counter(), Counter()
    for r in rs:
        turns = r["turns"]
        for i, t in enumerate(turns):
            if t["phase"] != "compile":
                continue
            fb = t.get("feedback", "")
            if lang == "quoin":
                codes = {f"{c} {k}" for c, k in code_re.findall(fb)}
            else:
                codes = {m.strip() for m in re.findall(r"error: ([^\n\[]+)", fb)}
                codes = {re.sub(r"'[^']*'", "'X'", c)[:60] for c in codes}
            nxt = turns[i + 1] if i + 1 < len(turns) else None
            for c in codes:
                hits[c] += 1
                if nxt and nxt["phase"] != "compile":
                    cleared[c] += 1
                elif nxt and c.split()[0] not in nxt.get("feedback", ""):
                    cleared[c] += 1
    print(f"### {lang}\n")
    if not hits:
        print("No compile failures.\n")
        continue
    print("| diagnostic | times | cleared on the next turn |")
    print("| --- | ---: | ---: |")
    for c, n in hits.most_common(15):
        print(f"| {c} | {n} | {cleared[c]} |")
    print()

print("## Failed runs\n")
fails = [r for r in runs if not r["success"]]
if not fails:
    print("None.")
for r in fails:
    phases = [t["phase"] for t in r["turns"]]
    print(f"- {r['task']} / {r['lang']} / seed {r['seed']}: {' -> '.join(phases)} {r.get('error', '')}")
