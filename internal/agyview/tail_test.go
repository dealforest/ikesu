package agyview

import "testing"

// 2026-11-26 に実物の agy（1.5.17・1.5.18）のペインで見た、質問の下の行。
func TestTailAcceptsObservedStatusAndQuotaRows(t *testing.T) {
	const status = "esc to cancel                     auto · Gemini 4 Neon · high"
	for name, rows := range map[string][]string{
		"status only":          {status},
		"old quota with Argon": {status, "Gemini 5h 0% (1h) · wk 2% (6d) │ Claude/GPT 5h 0% (<1m) · wk 50% (2d) │ Argon wk 0% (6d)"},
		"quota without Argon":  {status, "Gemini 5h 3% (2h39m) · wk 6% (6d16h) │ Claude/GPT 5h 0% (<1m) · wk 50% (1d22h)"},
		"quota wrapped":        {status, "Gemini 5h 11% (<1m) · wk 3% (6d18h)", "Claude/GPT 5h 100% (<1m) · wk 50% (2d1h)"},
		"quota cut off":        {status, "Gemini 5h 19% (11m) · wk 6%", "Claude/GPT 5h 0% (<1m) · wk"},
		"quota not yet":        {status, "quota: --"},
		"status with tasks":    {"esc to cancel                  auto · Gemini 4 Neon · medium · 1 task(s) · /tasks", "Gemini 5h 2% (2h46m) · wk 5% (6d16h) │ Claude/GPT 5h 0% (<1m) · wk 50% (1d22h)"},
		"blank rows":           {"", status, "", "quota: --", ""},
		"status without mode":  {"esc to cancel                                       Gemini 4 Neon · high", "Gemini 5h 3% (2h36m) · wk 6% (6d16h) │ Claude/GPT 5h 0% (<1m) · wk 50% (1d22h)"},
	} {
		if !Tail(rows) {
			t.Errorf("%s: rejected %q", name, rows)
		}
	}
}

func TestTailRejectsOtherRows(t *testing.T) {
	const status = "esc to cancel                     auto · Gemini 4 Neon · high"
	for name, rows := range map[string][]string{
		"no status":           {"Gemini 5h 3% (2h39m) · wk 6% (6d16h)"},
		"answer text below":   {status, "答え: ラーメン"},
		"prompt below":        {status, "> next question"},
		"too many quota rows": {status, "quota: --", "quota: --", "quota: --", "quota: --"},
		"other status":        {"? for shortcuts                   auto · Gemini 4 Neon · high"},
		"quota then text":     {status, "Gemini 5h 3% (2h39m) · wk 6% (6d16h)", "Thinking..."},
	} {
		if Tail(rows) {
			t.Errorf("%s: accepted %q", name, rows)
		}
	}
}
