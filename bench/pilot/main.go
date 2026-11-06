// Command pilot runs the tokens-to-correct-program pilot: the same tasks,
// model and harness for each language arm, with QEMU's Cortex-M33 as the
// (unofficial, pre-hardware) oracle.
//
//	go run . -mode selftest              # replay reference solutions; no API calls
//	go run . -mode run -seeds 3          # real run; needs QUOIN_PILOT_API_KEY
//
// Every token the model reads or writes counts: the language spec in the
// system prompt (charged on every turn), the task, the replies, and all
// compiler and test feedback, until the program passes or the run hits its
// iteration or token ceiling (failures count at the ceiling).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

func main() {
	mode := flag.String("mode", "selftest", "selftest | run")
	taskList := flag.String("tasks", "all", "comma-separated task names, or all")
	set := flag.String("set", "easy", "task set: easy, hard or all")
	langList := flag.String("langs", "quoin,c", "comma-separated language arms")
	seeds := flag.Int("seeds", 3, "independent runs per task and language")
	iters := flag.Int("iters", 6, "maximum model turns per run")
	ceiling := flag.Int64("ceiling", 150000, "token ceiling per run; failures count as this many tokens")
	vectors := flag.Int("vectors", 3, "random inputs per test")
	workers := flag.Int("workers", 4, "runs in parallel")
	model := flag.String("model", "claude-opus-5-5", "model id")
	effort := flag.String("effort", "medium", "output effort (ignored by claude-haiku-4-5)")
	thinkingBudget := flag.Int64("thinking-budget", 0, "claude-haiku-4-5 only: extended-thinking budget in tokens (0 = no thinking)")
	maxUSD := flag.Float64("max-usd", 60, "stop starting new runs once this much has been spent")
	out := flag.String("out", "", "results directory (default ../results/pilot-<date>)")
	repo := flag.String("repo", "../..", "Quoin repository root")
	flag.Parse()

	ctx := context.Background()
	pr, ok := prices[*model]
	if !ok {
		check(fmt.Errorf("no pricing for model %q: add it to prices in model.go", *model))
	}
	price = pr
	if legacyModel(*model) {
		*effort = "n/a"
	}
	work, err := os.MkdirTemp("", "pilot-work-")
	check(err)
	defer os.RemoveAll(work)
	tc, err := setupToolchain(work, *repo)
	check(err)
	langs, err := tc.langs(*repo)
	check(err)

	var pool []Task
	switch *set {
	case "easy":
		pool = tasks
	case "hard":
		pool = hardTasks
	case "all":
		pool = append(append([]Task{}, tasks...), hardTasks...)
	default:
		check(fmt.Errorf("unknown set %q", *set))
	}
	var ts []*Task
	for i := range pool {
		if *taskList == "all" || contains(strings.Split(*taskList, ","), pool[i].Name) {
			ts = append(ts, &pool[i])
		}
	}
	var ls []*Lang
	for _, n := range strings.Split(*langList, ",") {
		l, ok := langs[n]
		if !ok {
			check(fmt.Errorf("unknown language %q", n))
		}
		ls = append(ls, l)
	}
	cfg := config{maxIters: *iters, ceiling: *ceiling, vectors: *vectors}

	switch *mode {
	case "selftest":
		os.Exit(selftest(ctx, tc, ts, ls, cfg))
	case "count":
		key := os.Getenv("QUOIN_PILOT_API_KEY")
		if key == "" {
			check(fmt.Errorf("QUOIN_PILOT_API_KEY is not set"))
		}
		for name, n := range specTokens(ctx, newAnthropic(key, *model, *effort, *thinkingBudget), ls) {
			fmt.Printf("%-12s %d tokens\n", name, n)
		}
		return
	case "report":
		// Rebuild report.md from a results directory's runs.jsonl and summary.json.
		check(rebuild(*out, ts, ls, cfg))
		return
	case "run":
	default:
		check(fmt.Errorf("unknown mode %q", *mode))
	}

	key := os.Getenv("QUOIN_PILOT_API_KEY")
	if key == "" {
		check(fmt.Errorf("QUOIN_PILOT_API_KEY is not set: add it to the environment's variables and start a new session"))
	}
	prov := newAnthropic(key, *model, *effort, *thinkingBudget)
	dir := *out
	if dir == "" {
		dir = filepath.Join("..", "results", "pilot-"+time.Now().UTC().Format("2006-01-02"))
	}
	check(os.MkdirAll(dir, 0o755))

	type job struct {
		t    *Task
		l    *Lang
		seed int
	}
	var jobs []job
	for s := 0; s < *seeds; s++ {
		for _, t := range ts {
			for _, l := range ls {
				jobs = append(jobs, job{t, l, s})
			}
		}
	}
	var mu sync.Mutex
	var runs []Run
	var spent float64
	stopped := false
	logf, err := os.Create(filepath.Join(dir, "runs.jsonl"))
	check(err)
	defer logf.Close()
	ch := make(chan job)
	var wg sync.WaitGroup
	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range ch {
				r := attempt(ctx, prov, tc, j.l, j.t, j.seed, cfg, true)
				mu.Lock()
				runs = append(runs, r)
				spent += r.USD
				b, _ := json.Marshal(r)
				logf.Write(append(b, '\n'))
				fmt.Printf("%-9s %-5s seed %d: %-4v iters=%d tokens=%-6d $%.2f (total $%.2f) %s\n",
					r.Task, r.Lang, r.Seed, r.Success, r.Iters, r.Tokens, r.USD, spent, r.Err)
				mu.Unlock()
			}
		}()
	}
	for _, j := range jobs {
		mu.Lock()
		over := spent >= *maxUSD
		mu.Unlock()
		if over {
			stopped = true
			break
		}
		ch <- j
	}
	close(ch)
	wg.Wait()

	sort.Slice(runs, func(i, k int) bool {
		a, b := runs[i], runs[k]
		if a.Task != b.Task {
			return a.Task < b.Task
		}
		if a.Lang != b.Lang {
			return a.Lang < b.Lang
		}
		return a.Seed < b.Seed
	})
	meta := map[string]any{
		"date": time.Now().UTC().Format(time.RFC3339), "model": *model, "effort": *effort, "seeds": *seeds,
		"max_iters": *iters, "ceiling": *ceiling, "vectors": *vectors, "stopped_by_budget": stopped, "set": *set,
		"thinking_budget": *thinkingBudget,
		"spec_tokens":     specTokens(ctx, prov, ls), "ref_tokens": refTokens(ctx, prov, ts, ls),
	}
	md := report(runs, ts, ls, cfg, meta)
	check(os.WriteFile(filepath.Join(dir, "report.md"), []byte(md), 0o644))
	b, _ := json.MarshalIndent(map[string]any{"meta": meta, "runs": summarize(runs)}, "", "  ")
	check(os.WriteFile(filepath.Join(dir, "summary.json"), b, 0o644))
	fmt.Println(md)
}

func rebuild(dir string, ts []*Task, ls []*Lang, cfg config) error {
	data, err := os.ReadFile(filepath.Join(dir, "summary.json"))
	if err != nil {
		return err
	}
	var sum struct {
		Meta map[string]any `json:"meta"`
	}
	if err := json.Unmarshal(data, &sum); err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "runs.jsonl"))
	if err != nil {
		return err
	}
	var runs []Run
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var r Run
		if err := json.Unmarshal([]byte(l), &r); err != nil {
			return err
		}
		runs = append(runs, r)
	}
	sort.Slice(runs, func(i, k int) bool {
		a, b := runs[i], runs[k]
		if a.Task != b.Task {
			return a.Task < b.Task
		}
		if a.Lang != b.Lang {
			return a.Lang < b.Lang
		}
		return a.Seed < b.Seed
	})
	// JSON numbers decode as float64; restore the typed maps report expects.
	if st, ok := sum.Meta["spec_tokens"].(map[string]any); ok {
		m := map[string]int64{}
		for k, v := range st {
			m[k] = int64(v.(float64))
		}
		sum.Meta["spec_tokens"] = m
	}
	if rt, ok := sum.Meta["ref_tokens"].(map[string]any); ok {
		m := map[string]map[string]int64{}
		for t, inner := range rt {
			m[t] = map[string]int64{}
			for k, v := range inner.(map[string]any) {
				m[t][k] = int64(v.(float64))
			}
		}
		sum.Meta["ref_tokens"] = m
	}
	cfg.ceiling = int64(sum.Meta["ceiling"].(float64))
	cfg.vectors = int(sum.Meta["vectors"].(float64))
	md := report(runs, ts, ls, cfg, sum.Meta)
	fmt.Println(md)
	return os.WriteFile(filepath.Join(dir, "report.md"), []byte(md), 0o644)
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "pilot:", err)
		os.Exit(2)
	}
}

// selftest replays the reference solutions through the full pipeline
// (build, QEMU, oracle), proving every task is satisfiable in every arm,
// and replays one broken reply first to exercise the feedback loop.
func selftest(ctx context.Context, tc *toolchain, ts []*Task, ls []*Lang, cfg config) int {
	prov := &replayProvider{script: map[string][]string{}, calls: map[string]int{}}
	for _, t := range ts {
		for _, l := range ls {
			src, err := os.ReadFile(filepath.Join("refs", t.Name+l.Ext))
			if err != nil {
				fmt.Printf("%-9s %-5s MISSING reference: %v\n", t.Name, l.Name, err)
				return 1
			}
			ref := "```\n" + string(src) + "```\n"
			prov.script[fmt.Sprintf("%s/%s/0", t.Name, l.Name)] = []string{ref}
			// seed 1: a reply without code, then a broken program, then the reference.
			prov.script[fmt.Sprintf("%s/%s/1", t.Name, l.Name)] = []string{"I will think about it.", "```\nthis is not a program\n```", ref}
		}
	}
	fail := 0
	for _, t := range ts {
		for _, l := range ls {
			for seed := 0; seed < 2; seed++ {
				r := attempt(ctx, prov, tc, l, t, seed, cfg, false)
				wantIters := 1 + 2*seed
				ok := r.Success && r.Iters == wantIters && r.Err == ""
				status := "ok"
				if !ok {
					status = "FAIL"
					fail++
				}
				var phases []string
				for _, tu := range r.Turns {
					phases = append(phases, tu.Phase)
				}
				fmt.Printf("%-9s %-5s seed %d: %-4s iters=%d phases=%v\n", t.Name, l.Name, seed, status, r.Iters, phases)
				if !ok {
					for _, tu := range r.Turns {
						if tu.Feedback != "" {
							fmt.Println("   ", strings.ReplaceAll(truncate(tu.Feedback, 1500), "\n", "\n    "))
						}
					}
				}
			}
		}
	}
	fmt.Printf("\nselftest: %d failures\n", fail)
	if fail > 0 {
		return 1
	}
	return 0
}
