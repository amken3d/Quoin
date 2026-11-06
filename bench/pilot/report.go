package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/v2"
)

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	s := append([]float64{}, xs...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// p90 uses the nearest-rank method.
func p90(xs []float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	s := append([]float64{}, xs...)
	sort.Float64s(s)
	k := int(math.Ceil(0.9*float64(len(s)))) - 1
	return s[max(k, 0)]
}

// score is a run's tokens-to-correct: its tokens if it passed, the ceiling
// if it did not.
func score(r Run, cfg config) float64 {
	if r.Success {
		return float64(r.Tokens)
	}
	return float64(cfg.ceiling)
}

func pct(a, b int) string {
	if b == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.0f%% (%d/%d)", 100*float64(a)/float64(b), a, b)
}

func report(runs []Run, ts []*Task, ls []*Lang, cfg config, meta map[string]any) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Tokens-to-correct pilot\n\n")
	fmt.Fprintf(&b, "Model `%v`, effort `%v`, %v seeds per task and language, up to %v turns per run, token ceiling %v per run (a failed run counts as the ceiling), %v fresh random inputs per test. Oracle: QEMU mps2-an505 (Cortex-M33), output match plus measured stack within the task budget. **Not hardware: by the benchmark's own rule these results are provisional.** Cycle bounds are not part of this pilot.\n\n",
		meta["model"], meta["effort"], meta["seeds"], meta["max_iters"], cfg.ceiling, cfg.vectors)
	if meta["stopped_by_budget"] == true {
		b.WriteString("**The run stopped early at its spending limit; some task/language/seed combinations are missing.**\n\n")
	}
	perTask := map[string]map[string][]float64{}
	for _, r := range runs {
		if perTask[r.Task] == nil {
			perTask[r.Task] = map[string][]float64{}
		}
		perTask[r.Task][r.Lang] = append(perTask[r.Task][r.Lang], score(r, cfg))
	}
	b.WriteString("## Headline\n\n| Language | median tokens-to-correct | p90 | success | first-pass compile | first-pass correct | turns to green (median) | spent |\n| --- | ---: | ---: | --- | --- | --- | ---: | ---: |\n")
	for _, l := range ls {
		var med []float64
		var succ, total, fc, fp int
		var turns []float64
		var usd float64
		for _, t := range ts {
			if xs := perTask[t.Name][l.Name]; len(xs) > 0 {
				med = append(med, median(xs))
			}
		}
		for _, r := range runs {
			if r.Lang != l.Name {
				continue
			}
			total++
			usd += r.USD
			if r.Success {
				succ++
				turns = append(turns, float64(r.Iters))
			}
			if r.FirstOK {
				fc++
			}
			if r.FirstPass {
				fp++
			}
		}
		fmt.Fprintf(&b, "| %s | %.0f | %.0f | %s | %s | %s | %.1f | $%.2f |\n", l.Name, median(med), p90(med), pct(succ, total), pct(fc, total), pct(fp, total), median(turns), usd)
	}
	b.WriteString("\nThe median and p90 are over tasks, of each task's median over seeds.\n\n")

	// Each arm is compared with the C baseline when it is present.
	var base *Lang
	var others []*Lang
	for _, l := range ls {
		if l.Name == "c" {
			base = l
		} else {
			others = append(others, l)
		}
	}
	b.WriteString("## Per task (median tokens over seeds; successes)\n\n| task |")
	for _, l := range ls {
		fmt.Fprintf(&b, " %s |", l.Name)
	}
	if base != nil {
		for _, l := range others {
			fmt.Fprintf(&b, " %s / c |", l.Name)
		}
	}
	b.WriteString("\n| --- |")
	for range ls {
		b.WriteString(" ---: |")
	}
	if base != nil {
		for range others {
			b.WriteString(" ---: |")
		}
	}
	b.WriteString("\n")
	logRatios := map[string][]float64{}
	wins := map[string]int{}
	for _, t := range ts {
		fmt.Fprintf(&b, "| %s |", t.Name)
		meds := map[string]float64{}
		for _, l := range ls {
			xs := perTask[t.Name][l.Name]
			s := 0
			for _, r := range runs {
				if r.Task == t.Name && r.Lang == l.Name && r.Success {
					s++
				}
			}
			meds[l.Name] = median(xs)
			fmt.Fprintf(&b, " %.0f (%d/%d) |", meds[l.Name], s, len(xs))
		}
		if base != nil {
			for _, l := range others {
				ratio := meds[l.Name] / meds["c"]
				logRatios[l.Name] = append(logRatios[l.Name], math.Log(ratio))
				if ratio < 1 {
					wins[l.Name]++
				}
				fmt.Fprintf(&b, " %.2f |", ratio)
			}
		}
		b.WriteString("\n")
	}
	if base != nil {
		b.WriteString("\n")
		for _, l := range others {
			var sum float64
			for _, x := range logRatios[l.Name] {
				sum += x
			}
			n := len(logRatios[l.Name])
			fmt.Fprintf(&b, "- %s vs c: geometric mean of the per-task ratio **%.2f** (below 1 means fewer tokens than C); cheaper on %d of %d tasks.\n",
				l.Name, math.Exp(sum/float64(n)), wins[l.Name], n)
		}
	}

	b.WriteString("\n## Where attempts failed (all turns, all runs)\n\n| Language | compile | wrong output | trap | timeout | fault | stack | no code | refusal |\n| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, l := range ls {
		c := map[string]int{}
		for _, r := range runs {
			if r.Lang == l.Name {
				for _, tu := range r.Turns {
					c[tu.Phase]++
				}
			}
		}
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | %d | %d | %d | %d |\n", l.Name, c["compile"], c["wrong"], c["trap"], c["timeout"], c["fault"], c["stack"], c["nocode"]+c["truncated"], c["refusal"])
	}
	if st, ok := meta["spec_tokens"].(map[string]int64); ok {
		b.WriteString("\n## Fixed prompt cost\n\nSystem prompt (spec + platform notes), charged on every turn:\n\n")
		for _, l := range ls {
			fmt.Fprintf(&b, "- %s: %d tokens\n", l.Name, st[l.Name])
		}
	}
	if rt, ok := meta["ref_tokens"].(map[string]map[string]int64); ok {
		b.WriteString("\n## Static token density (reference solutions)\n\n| task |")
		for _, l := range ls {
			fmt.Fprintf(&b, " %s |", l.Name)
		}
		b.WriteString("\n| --- |")
		for range ls {
			b.WriteString(" ---: |")
		}
		b.WriteString("\n")
		for _, t := range ts {
			fmt.Fprintf(&b, "| %s |", t.Name)
			for _, l := range ls {
				fmt.Fprintf(&b, " %d |", rt[t.Name][l.Name])
			}
			b.WriteString("\n")
		}
	}
	errs := 0
	for _, r := range runs {
		if r.Err != "" {
			errs++
		}
	}
	if errs > 0 {
		fmt.Fprintf(&b, "\n%d runs ended on a harness or API error (see runs.jsonl); they count as failures.\n", errs)
	}
	return b.String()
}

// summarize drops replies and code from the runs for the JSON summary.
func summarize(runs []Run) []Run {
	out := make([]Run, len(runs))
	for i, r := range runs {
		r.Turns = nil
		out[i] = r
	}
	return out
}

// specTokens counts each arm's system prompt with the token-counting endpoint.
func specTokens(ctx context.Context, prov Provider, ls []*Lang) map[string]int64 {
	p, ok := prov.(*anthropicProvider)
	if !ok {
		return nil
	}
	out := map[string]int64{}
	for _, l := range ls {
		res, err := p.client.Messages.CountTokens(ctx, anthropic.MessageCountTokensParams{
			Model:    p.model,
			System:   anthropic.MessageCountTokensParamsSystemUnion{OfTextBlockArray: []anthropic.TextBlockParam{{Text: l.Spec}}},
			Messages: []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("x"))},
		})
		if err == nil {
			out[l.Name] = res.InputTokens
		}
	}
	return out
}

// refTokens counts each reference solution's tokens.
func refTokens(ctx context.Context, prov Provider, ts []*Task, ls []*Lang) map[string]map[string]int64 {
	p, ok := prov.(*anthropicProvider)
	if !ok {
		return nil
	}
	out := map[string]map[string]int64{}
	for _, t := range ts {
		out[t.Name] = map[string]int64{}
		for _, l := range ls {
			src, err := os.ReadFile(filepath.Join("refs", t.Name+l.Ext))
			if err != nil {
				continue
			}
			res, err := p.client.Messages.CountTokens(ctx, anthropic.MessageCountTokensParams{
				Model:    p.model,
				Messages: []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(string(src)))},
			})
			if err == nil {
				out[t.Name][l.Name] = res.InputTokens
			}
		}
	}
	return out
}
