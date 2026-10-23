package architecture

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// lib.sh's `eventually ROUNDS CMD` once meant ROUNDS tries with 3s between
// them. A faster poll (KIND_POLL) must not cut that: a slow CMD, or a host that
// slept through the wait, still gets at least ROUNDS tries and at least
// ROUNDS x 3s of sleeping between them. The clock is faked (SECONDS is
// assignable, sleep is stubbed) so the check runs instantly.
func TestEventuallyNeverMakesFewerTries(t *testing.T) {
	lib := filepath.Join(kindDir(t), "lib.sh")
	cases := []struct {
		name, cmdCost, rounds string
		min                   int
	}{
		{"slow command", "7", "2", 2},             // each try takes 7s, past a 6s deadline
		{"host slept", "1000", "10", 10},          // the clock jumps while CMD runs
		{"fast poll waits as long", "0", "4", 12}, // 12s of 1s sleeps, not 4 tries
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			script := `source "$1"; KIND_POLL=1; unset KIND_TIMING; n=0
sleep() { SECONDS=$((SECONDS + $1)); }
cmd() { n=$((n + 1)); SECONDS=$((SECONDS + $1)); return 1; }
eventually "$3" cmd "$2" && exit 9; echo "$n"`
			out, err := exec.Command("bash", "-c", script, "t", lib, c.cmdCost, c.rounds).CombinedOutput() //nolint:gosec // fixed args
			if err != nil {
				t.Fatalf("bash: %v\n%s", err, out)
			}
			got, err := strconv.Atoi(strings.TrimSpace(string(out)))
			if err != nil {
				t.Fatalf("not a count: %q", out)
			}
			if got < c.min {
				t.Fatalf("eventually %s with a %ss command made %d tries, want at least %d", c.rounds, c.cmdCost, got, c.min)
			}
		})
	}
}

// CI runs delivery.sh's phases through run_phases, which prefixes each line
// with its phase, and scripts/ci/kind-run.sh reports the first FAIL: line with
// a leading FAIL: stripped. The prefix must not break that, and run_phases'
// own failure line must not be the one reported.
func TestDeliveryPhaseFailReadsInCI(t *testing.T) {
	kind := kindDir(t)
	script := `eval "$(awk '/^phase_body\(\) \{/{on=1} on{print} on && /^run_phases\(\) \{/{f=1} f && /^}$/{exit}' "$1")"
timing_end() { :; }; phase_runs() { :; }; phase_deps() { :; }
phase_health() { echo "  FAIL: boom" >&2; exit 1; }
STATE="$(mktemp -d)"; PHASES=health; unset DELIVERY_SEQUENTIAL
( run_phases ) > "$STATE/out" 2>&1
grep -m1 'FAIL:' "$STATE/out" | sed 's/^ *FAIL: *//'`
	out, err := exec.Command("bash", "-c", script, "t", filepath.Join(kind, "delivery.sh")).CombinedOutput() //nolint:gosec // fixed args
	if err != nil {
		t.Fatalf("bash: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "[health] boom" {
		t.Fatalf("CI would report %q, want %q", got, "[health] boom")
	}
}
