package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestTourStepsAreOneSentence keeps every tour step to one plain sentence. The
// fleet tour's first step is the data label itself and is left out.
func TestTourStepsAreOneSentence(t *testing.T) {
	js, err := os.ReadFile("boot.js")
	if err != nil {
		t.Fatal(err)
	}
	steps := regexp.MustCompile(`(?m)^\s+text: "([^"]+)",$`).FindAllSubmatch(js, -1)
	if len(steps) < 15 {
		t.Fatalf("found %d tour steps", len(steps))
	}
	for _, s := range steps {
		if text := string(s[1]); strings.Contains(text, ". ") {
			t.Errorf("more than one sentence: %s", text)
		}
	}
}

// tourStep is one step of a tour in boot.js: its text, its spotlight target with
// the selector constants expanded, and whether it waits for the reader to act.
type tourStep struct {
	text, target string
	gated        bool
}

func tourSteps(t *testing.T, name string) []tourStep {
	t.Helper()
	js, err := os.ReadFile("boot.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(js)
	consts := map[string]string{}
	for _, m := range regexp.MustCompile(`(?m)^\s+var ([A-Z_]+) = '([^']+)';$`).FindAllStringSubmatch(src, -1) {
		consts[m[1]] = m[2]
	}
	start := strings.Index(src, "var "+name+" = [{")
	if start < 0 {
		t.Fatalf("no %s in boot.js", name)
	}
	body := src[start:]
	body = body[:strings.Index(body, "\n    }];")]
	textRe := regexp.MustCompile(`text: (?:NOTICE \+ )?"([^"]+)"`)
	targetRe := regexp.MustCompile(`(?m)^\s+target: (.+),$`)
	var out []tourStep
	for _, chunk := range strings.Split(body, "\n    }, {\n") {
		// A step that waits on the system (observe) asks the reader for nothing.
		s := tourStep{gated: strings.Contains(chunk, "gate: function") && !strings.Contains(chunk, "observe: true")}
		if m := textRe.FindStringSubmatch(chunk); m != nil {
			s.text = m[1]
		}
		if m := targetRe.FindStringSubmatch(chunk); m != nil {
			s.target = m[1]
			for k, v := range consts {
				s.target = regexp.MustCompile(`\b`+k+`\b`).ReplaceAllLiteralString(s.target, v)
			}
		}
		out = append(out, s)
	}
	return out
}

// TestTourLightsTheControlEachStepNames: every step that asks for an action
// spotlights the control that action uses. The report: a step said "press
// Switch person" while the light sat on the decision card.
func TestTourLightsTheControlEachStepNames(t *testing.T) {
	promote := regexp.MustCompile(`press Promote on (\w+) ([\d.]+) in prod`)
	rules := []struct{ says, lights string }{
		{"open the person menu", "identity-trigger"},
		{"Press Switch person", `"sign-out"`},
		{"Pick Carol", "demo-persona-carol"},
		{"Write in the note", `decision-panel"] textarea`},
		{"Press Approve", "decide-approve"},
		{"Press Confirm", "decide-approve"},
		{"Press Accept the unknown", "decide-approve"},
		{"sign in again as Carol", "demo-signin-submit"},
		{"Propose promote", `propose-panel"] button[type=submit]`},
		{"Propose roll back", `propose-panel"] button[type=submit]`},
		{"press Propose on fraud", "Propose Advise for fraud in test"},
		{"press Roll back this change", "change-rollback"},
		{"press Compare revisions", "#sec-revisions button[type=submit]"},
		{"set Earlier revision", "#impact-old-rev"},
		{"typing orders", "graph-discovery"},
		{"Open orders-service", "graph-focus-link"},
		{"type payments in the search box", "svc-search"},
		{"open payments-service from the list", "/fleet/services/payments-service"},
	}
	for _, tour := range []string{"FLEET_TOUR", "DELIVERY_TOUR"} {
		for i, s := range tourSteps(t, tour) {
			for _, r := range rules {
				if strings.Contains(s.text, r.says) && !strings.Contains(s.target, r.lights) {
					t.Errorf("%s step %d says %q but lights %s, not %s", tour, i+1, r.says, s.target, r.lights)
				}
			}
			if m := promote.FindStringSubmatch(s.text); m != nil && !strings.Contains(s.target, "Promote "+m[1]+" in prod to "+m[2]) {
				t.Errorf("%s step %d asks to promote %s %s but lights %s", tour, i+1, m[1], m[2], s.target)
			}
		}
	}
}

// TestTourStepsAskOneThing: a step that waits for an action asks for exactly
// one, so the light can sit on it; switch person, pick Carol, write the note,
// approve and sign in again are five steps, never one.
func TestTourStepsAskOneThing(t *testing.T) {
	verbs := regexp.MustCompile(`(?i)\b(press|pick|write|type|typing|set|open|sign in|choose)\b`)
	for _, tour := range []string{"FLEET_TOUR", "DELIVERY_TOUR"} {
		steps := tourSteps(t, tour)
		if len(steps) < 8 {
			t.Fatalf("%s has %d steps", tour, len(steps))
		}
		for i, s := range steps {
			if !s.gated {
				continue
			}
			if n := len(verbs.FindAllString(strings.ReplaceAll(s.text, "press Enter", ""), -1)); n != 1 {
				t.Errorf("%s step %d asks for %d actions: %s", tour, i+1, n, s.text)
			}
			if s.target == "" {
				t.Errorf("%s step %d waits for an action but lights nothing", tour, i+1)
			}
		}
	}
}
