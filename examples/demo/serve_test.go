package main

import (
	"os/exec"
	"strings"
	"testing"
)

// Regression: with the default relative BASE (./) the serve recipe built
// ".serve./" instead of a directory under .serve, so its `cd .serve` served
// nothing and the printed URL read "http://localhost:8088./".
func TestServeRecipeNestsBaseUnderServeDir(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not installed")
	}
	for base, url := range map[string]string{"./": "http://localhost:8088/\"", "/pacto/demo/": "http://localhost:8088/pacto/demo/\""} {
		out, err := exec.Command("make", "-n", "serve", "BASE="+base).CombinedOutput()
		if err != nil {
			t.Fatalf("make -n serve BASE=%s: %v\n%s", base, err, out)
		}
		if s := string(out); !strings.Contains(s, `mkdir -p ".serve/`+base+`"`) || !strings.Contains(s, url) {
			t.Errorf("BASE=%s: serve recipe = %s", base, s)
		}
	}
}
