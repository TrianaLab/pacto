package tui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// verbByKey returns the write verb bound to key, failing if it is missing. A
// missing verb is a real failure rather than a skip: the table is the only
// definition of what the TUI can do, so a key that has fallen out of it is a
// capability that silently disappeared.
func verbByKey(t *testing.T, key string) Verb {
	t.Helper()
	for _, v := range writeVerbs() {
		if v.Key == key {
			return v
		}
	}
	t.Fatalf("no write verb bound to %q", key)
	return Verb{}
}

// localSel and remoteSel are the two selection shapes the write verbs split on.
func localSel() Selection {
	return Selection{
		Kind: fleet.KindRevision, Key: "local@1.0.0", Label: "local 1.0.0",
		Ref: "/tmp/local", Local: true,
	}
}

func remoteSel() Selection {
	return Selection{
		Kind: fleet.KindRevision, Key: "remote@1.0.0", Label: "remote 1.0.0",
		Ref: "oci://ghcr.io/acme/remote:1.0.0",
	}
}

// answerThePrompt drives a verb that has to ask for an argument before it can
// build a command. It takes what Run returned, finds the prompt that was pushed
// and hands it an answer through the same field promptScreen.Update calls;
// Update wraps that call in a tea.Sequence whose contents are unexported, and
// prompt_test.go is what proves Update calls it with the typed value.
func answerThePrompt(t *testing.T, cmd tea.Cmd, answer string) tea.Cmd {
	t.Helper()
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("the verb produced %T, want the batch that opens a prompt", cmd())
	}
	msg, ok := batch[0]().(pushMsg)
	if !ok {
		t.Fatalf("the verb pushed %T, want a pushMsg", batch[0]())
	}
	p, ok := msg.s.(*promptScreen)
	if !ok {
		t.Fatalf("the verb pushed %T, want a *promptScreen", msg.s)
	}
	if !strings.Contains(p.question, "(") {
		t.Errorf("the prompt %q does not say what shape of answer it wants", p.question)
	}
	return p.onSubmit(answer)
}

// TestEveryWriteVerbOpensAConfirmation is the guarantee that matters most about
// this table: there is no key that changes anything without asking first. It
// drives every verb through its own Applies predicate, so a verb whose predicate
// and prompt disagree fails here rather than at a reader's terminal. The two
// verbs that need an argument no selection carries ask for it first, and the
// confirmation still comes after.
func TestEveryWriteVerbOpensAConfirmation(t *testing.T) {
	for _, tt := range []struct {
		key    string
		sel    Selection
		answer string // non-empty when the verb prompts before it can build a command
	}{
		{key: "p", sel: localSel(), answer: "oci://ghcr.io/acme/local:1.0.0"},
		{key: "P", sel: remoteSel()},
		{key: "L", sel: localSel()},
		{key: "G", sel: localSel(), answer: "schema-infer"},
	} {
		t.Run(tt.key, func(t *testing.T) {
			c := newLoadedContext(t)
			v := verbByKey(t, tt.key)
			if why := v.Applies(tt.sel); why != "" {
				t.Fatalf("%q does not apply to the selection it is meant for: %s", tt.key, why)
			}
			cmd := v.Run(c, tt.sel)
			if tt.answer != "" {
				cmd = answerThePrompt(t, cmd, tt.answer)
			}
			msg, ok := cmd().(pushMsg)
			if !ok {
				t.Fatalf("%q produced %T, want a pushMsg opening the confirmation", tt.key, cmd())
			}
			if msg.s.Title() != "Confirm" {
				t.Fatalf("%q pushed %q, want the confirmation screen", tt.key, msg.s.Title())
			}
		})
	}
}

// TestWriteVerbArgvsMatchTheRealSignatures pins each line against the command
// it invokes. push takes the DESTINATION as its positional and the bundle on -p
// (internal/cli/push.go:55), generate takes a plugin NAME first
// (internal/cli/generate.go:16), lock takes a directory (lock.go:14) and pull
// names its output explicitly so the confirmation can say where the files land.
func TestWriteVerbArgvsMatchTheRealSignatures(t *testing.T) {
	c := newLoadedContext(t)
	for _, tt := range []struct {
		key  string
		sel  Selection
		want []string
	}{
		{"p", localSel(), []string{"pacto", "push", "<ref>", "-p", "/tmp/local"}},
		{"P", remoteSel(), []string{"pacto", "pull", "oci://ghcr.io/acme/remote:1.0.0", "-o", "remote"}},
		{"L", localSel(), []string{"pacto", "lock", "--update", "/tmp/local"}},
		{"G", localSel(), []string{"pacto", "generate", "<plugin>", "/tmp/local"}},
	} {
		t.Run(tt.key, func(t *testing.T) {
			if got := verbByKey(t, tt.key).Argv(c, tt.sel); !slices.Equal(got, tt.want) {
				t.Fatalf("%q argv = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}

// TestThePromptedVerbsRunTheLineTheyAdvertise is the anti-drift assertion. Each
// prompted verb has one argv builder, so what the confirmation runs must be
// what Argv shows with the placeholder replaced by the answer. Two definitions
// of the same command is exactly how push came to publish the wrong bundle.
func TestThePromptedVerbsRunTheLineTheyAdvertise(t *testing.T) {
	for _, tt := range []struct {
		key         string
		answer      string
		placeholder string
	}{
		{"p", "oci://ghcr.io/acme/local:1.0.0", refPlaceholder},
		{"G", "schema-infer", pluginPlaceholder},
	} {
		t.Run(tt.key, func(t *testing.T) {
			c := newLoadedContext(t)
			sel := localSel()
			v := verbByKey(t, tt.key)

			want := slices.Clone(v.Argv(c, sel))
			i := slices.Index(want, tt.placeholder)
			if i < 0 {
				t.Fatalf("%q advertises %v, which has no %s to fill in", tt.key, want, tt.placeholder)
			}
			want[i] = tt.answer

			cmd := answerThePrompt(t, v.Run(c, sel), tt.answer)
			confirm, ok := cmd().(pushMsg).s.(*confirmScreen)
			if !ok {
				t.Fatalf("%q did not reach the confirmation", tt.key)
			}
			if !slices.Equal(confirm.argv, want) {
				t.Fatalf("%q confirms %v but advertises %v", tt.key, confirm.argv, want)
			}
		})
	}
}

// TestWriteVerbApplicability pins which selections each write verb offers
// itself for, and what it says when it declines. Pull on a directory that is
// already here and generate against a registry reference are both nonsense, and
// generate is the one that would run plugin binaries if it got this wrong. The
// reason matters as much as the verdict: these predicates key on locality, so a
// rejection that blames the kind sends the reader looking in the wrong place.
func TestWriteVerbApplicability(t *testing.T) {
	for _, tt := range []struct {
		name      string
		sel       Selection
		wantPull  string
		wantLocal string
	}{
		{
			"a registry reference",
			Selection{Kind: fleet.KindRevision, Ref: "oci://ghcr.io/acme/svc:1.0"},
			"", "not a directory on disk",
		},
		{
			"a local directory",
			Selection{Kind: fleet.KindRevision, Ref: "/tmp/svc", Local: true},
			"already a directory on disk", "",
		},
		{
			"no bundle at all",
			Selection{Kind: fleet.KindOwner},
			"owner has no bundle", "owner has no bundle",
		},
		{
			// Local without a ref is not a local bundle. Nothing produces this,
			// but the ref check has to come first or the message would claim a
			// directory that is not there.
			"no bundle, local set",
			Selection{Kind: fleet.KindSource, Local: true},
			"source has no bundle", "source has no bundle",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasRemoteRef(tt.sel); !matchReason(got, tt.wantPull) {
				t.Errorf("hasRemoteRef = %q, want %q", got, tt.wantPull)
			}
			if got := hasLocalBundle(tt.sel); !matchReason(got, tt.wantLocal) {
				t.Errorf("hasLocalBundle = %q, want %q", got, tt.wantLocal)
			}
		})
	}
}

// matchReason compares an Applies result to what the test expects: "" means the
// verb applies, anything else is a substring of the reason it did not.
func matchReason(got, want string) bool {
	if want == "" {
		return got == ""
	}
	return strings.Contains(got, want)
}

// TestPullDirNamesTheDirectoryPullWillCreate covers the shapes a resolved ref
// arrives in. It is what lets the confirmation name a destination rather than
// repeating the source back at the reader.
func TestPullDirNamesTheDirectoryPullWillCreate(t *testing.T) {
	for _, tt := range []struct{ ref, want string }{
		{"oci://ghcr.io/acme/svc:1.0.0", "svc"},
		{"oci://ghcr.io/acme/svc@sha256:aabb", "svc"},
		{"oci://ghcr.io/acme/svc", "svc"},
		{"svc:1.0.0", "svc"},
	} {
		t.Run(tt.ref, func(t *testing.T) {
			if got := pullDir(tt.ref); got != tt.want {
				t.Fatalf("pullDir(%q) = %q, want %q", tt.ref, got, tt.want)
			}
		})
	}
}
