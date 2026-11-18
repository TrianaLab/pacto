package discover

import (
	"bytes"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func TestCleanURL(t *testing.T) {
	for raw, want := range map[string]string{
		"https://u:p@charts.example/x?token=1#f": "https://charts.example/x",
		"oauth2:tok@host/x?ref=1":                "host/x",
		"github.com/org/repo//x?ref=v1":          "github.com/org/repo//x",
		"http://localhost:8080/healthz?":         "http://localhost:8080/healthz",
	} {
		if got := cleanURL(raw); got != want {
			t.Errorf("cleanURL(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestDiscoveryNeverRecordsURLCredentials(t *testing.T) {
	dir := writeFiles(t, map[string]string{"Dockerfile": "FROM x\nHEALTHCHECK CMD curl -f http://admin:s3cr3t@localhost:8080/healthz?token=tok3n#frag\n"})
	r := readFixture(t, dir)
	f := field(t, r.Fields, "capabilities[health]")
	for _, secret := range []string{"admin", "s3cr3t", "tok3n", "frag"} {
		if strings.Contains(f.Value+f.Declare, secret) {
			t.Errorf("%q recorded: %+v", secret, f)
		}
	}
	rec, err := CompleteIn(helmCredsDir, helmCredsDir, nil, contract.InferredInputs{Pacto: "4.6.0"}, nil)
	if err != nil || rec == nil {
		t.Fatalf("record = %+v, %v", rec, err)
	}
	if deps := rec.Inputs.ChartDependencies; len(deps) != 1 || deps[0].Repository != "https://charts.example.com/stable" {
		t.Errorf("chart dependencies = %+v", deps)
	}
	if out := rec.Marshal(); bytes.Contains(out, []byte("tok3n")) || bytes.Contains(out, []byte("user")) {
		t.Errorf("the record holds a credential:\n%s", out)
	}
	// A remote Kustomize resource is named without its userinfo or query.
	rec, err = CompleteIn(kustomizeDir, kustomizeDir, nil, contract.InferredInputs{Pacto: "4.6.0"}, nil)
	if err != nil || rec == nil {
		t.Fatalf("record = %+v, %v", rec, err)
	}
	if out := rec.Marshal(); bytes.Contains(out, []byte("tok3n")) || bytes.Contains(out, []byte("oauth2")) || bytes.Contains(out, []byte("ref=x")) {
		t.Errorf("the record holds a credential:\n%s", out)
	}
}
