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
