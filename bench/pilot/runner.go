package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
)

// Turn records one model turn and what the harness did with it.
type Turn struct {
	Iter     int    `json:"iter"`
	Usage    Usage  `json:"usage"`
	Stop     string `json:"stop"`
	Phase    string `json:"phase"` // nocode, compile, wrong, trap, timeout, fault, stack, pass, refusal, truncated
	Feedback string `json:"feedback,omitempty"`
	Code     string `json:"code,omitempty"`
	Reply    string `json:"reply,omitempty"`
}

// Run is one (task, language, seed) attempt sequence.
type Run struct {
	Task      string  `json:"task"`
	Lang      string  `json:"lang"`
	Seed      int     `json:"seed"`
	Success   bool    `json:"success"`
	Iters     int     `json:"iters"`
	Usage     Usage   `json:"usage"`
	Tokens    int64   `json:"tokens"`
	USD       float64 `json:"usd"`
	FirstOK   bool    `json:"first_compile_ok"`
	FirstPass bool    `json:"first_pass"`
	Err       string  `json:"error,omitempty"`
	Turns     []Turn  `json:"turns"`
	Seconds   float64 `json:"seconds"`
}

type config struct {
	maxIters int
	ceiling  int64 // token ceiling per run; exceeding it ends the run as a failure
	vectors  int   // random inputs per test
}

const promptTail = `

Stack budget: at most %d bytes of stack, measured on the device.`

// attempt drives one run: prompt, generate, build, test, feed back, repeat.
func attempt(ctx context.Context, prov Provider, tc *toolchain, lang *Lang, t *Task, seed int, cfg config, keepCode bool) Run {
	start := time.Now()
	r := Run{Task: t.Name, Lang: lang.Name, Seed: seed}
	key := fmt.Sprintf("%s/%s/%d", t.Name, lang.Name, seed)
	msgs := []anthropic.MessageParam{
		anthropic.NewUserMessage(anthropic.NewTextBlock("Task: " + t.Name + "\n\n" + t.Statement + fmt.Sprintf(promptTail, t.StackBound))),
	}
	dir, err := os.MkdirTemp("", "pilot-")
	if err != nil {
		r.Err = err.Error()
		return r
	}
	defer os.RemoveAll(dir)
	for it := 1; it <= cfg.maxIters; it++ {
		rep, err := prov.Turn(ctx, key, lang.Spec, msgs)
		if err != nil {
			r.Err = err.Error()
			break
		}
		r.Iters = it
		r.Usage = r.Usage.add(rep.Usage)
		turn := Turn{Iter: it, Usage: rep.Usage, Stop: rep.Stop}
		if keepCode {
			turn.Reply = rep.Text
		}
		msgs = append(msgs, rep.Assistant)
		if rep.Stop == string(anthropic.StopReasonRefusal) {
			turn.Phase = "refusal"
			r.Turns = append(r.Turns, turn)
			break
		}
		feedback := ""
		code := extractCode(rep.Text)
		turn.Code = code
		switch {
		case code == "" && rep.Stop == string(anthropic.StopReasonMaxTokens):
			turn.Phase, feedback = "truncated", "Your reply was cut off before the code block ended. Reply with one complete fenced code block."
		case code == "":
			turn.Phase, feedback = "nocode", "No fenced code block found. Reply with exactly one fenced code block containing the complete program."
		default:
			bctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			elf, diag, ok := lang.Build(bctx, dir, code)
			cancel()
			if it == 1 {
				r.FirstOK = ok
			}
			if !ok {
				turn.Phase, feedback = "compile", "Compilation failed:\n"+truncate(diag, 6000)
				break
			}
			rng := rand.New(rand.NewSource(int64(seed)*1_000_003 + int64(it)*7919 + int64(len(t.Name))))
			turn.Phase = "pass"
			inputs := append([][]byte{}, t.Edges...)
			for v := 0; v < cfg.vectors; v++ {
				inputs = append(inputs, t.Gen(rng))
			}
			for _, in := range inputs {
				verdict, err := judge(ctx, dir, elf, t, in)
				if err != nil {
					r.Err = err.Error()
					break
				}
				if !verdict.Pass {
					turn.Phase, feedback = verdict.Kind, verdict.Feedback
					break
				}
			}
		}
		if !keepCode {
			turn.Code = ""
		}
		turn.Feedback = feedback
		r.Turns = append(r.Turns, turn)
		if it == 1 {
			r.FirstPass = turn.Phase == "pass"
		}
		if turn.Phase == "pass" {
			r.Success = true
			break
		}
		if r.Err != "" || r.Usage.Total() > cfg.ceiling {
			break
		}
		msgs = append(msgs, anthropic.NewUserMessage(anthropic.NewTextBlock(feedback)))
	}
	r.Tokens = r.Usage.Total()
	r.USD = r.Usage.USD()
	r.Seconds = time.Since(start).Seconds()
	return r
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "\n... (truncated)"
	}
	return s
}
