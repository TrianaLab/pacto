package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
)


// bumpVersion rewrites the contract in dir to version and workload.
func bumpVersion(t *testing.T, dir, version, workload string) {
	t.Helper()
	p := filepath.Join(dir, "pacto.yaml")
	raw, _ := os.ReadFile(p)
	s := strings.Replace(string(raw), "version: 1.0.0", "version: "+version, 1)
	s = strings.Replace(s, "workload: service", "workload: "+workload, 1)
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestPublishKeepsTheVersionRefusal: a version the version rule refuses is
// kept, signed by the publish key, in the contract bundle repository, where
// the dashboard reads it; a refusal that cannot be kept is said beside it.
func TestPublishKeepsTheVersionRefusal(t *testing.T) {
	srv := httptest.NewServer(testutil.NewReferrersRegistry(testutil.ReferrersOptions{}))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	reg := publish.NewClient(authn.DefaultKeychain, host)
	ctx := context.Background()
	dir := writePublishBundle(t, host, "workload: service\n")
	pub := signingKey(t, dir)
	svc := publishService(&fakeRun{}, reg)
	svc.sys.now = func() time.Time { return time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC) }
	if _, err := svc.Publish(ctx, PublishOptions{Path: dir, Revision: "r1"}); err != nil {
		t.Fatal(err)
	}
	bumpVersion(t, dir, "1.0.1", "job")
	res, err := svc.Publish(ctx, PublishOptions{Path: dir, Revision: "r2"})
	if !errors.Is(err, publish.ErrNotReady) || res.Refusal != "the refusal is kept, signed, in "+host+"/pacto/payments" {
		t.Fatalf("refused publish: %v %q", err, res.Refusal)
	}
	repo, _ := ocireferrer.Open(host+"/pacto/payments", ocireferrer.Options{})
	verify := func(b []byte) error { _, err := signing.Policy{Key: pub}.Verify(b, nil); return err }
	got, err := deliveryrecord.ListRefusals(ctx, repo, "payments", verify)
	if err != nil || len(got.Refusals) != 1 {
		t.Fatalf("refusals: %+v %v", got, err)
	}
	if r := got.Refusals[0].Statement.Predicate; r.Version != "1.0.1" || r.Baseline != "1.0.0" || r.RequiredBump != "major" ||
		r.Classification != "BREAKING" || r.At != "2026-10-22T10:00:00Z" || !strings.Contains(r.Message, "major") {
		t.Fatalf("refusal: %+v", r)
	}
	// A repository that cannot be opened is said, and publish still refuses.
	svc.sys.refusalRepo = func(string) (*remote.Repository, error) { return nil, errors.New("no repo") }
	if res, err = svc.Publish(ctx, PublishOptions{Path: dir, Revision: "r2"}); !errors.Is(err, publish.ErrNotReady) ||
		!strings.HasSuffix(res.Refusal, ": no repo") {
		t.Fatalf("unopened repository: %v %q", err, res.Refusal)
	}
}

// TestKeepRefusalFailures: each step that can fail is said, never hidden.
func TestKeepRefusalFailures(t *testing.T) {
	refused := publish.Plan{Service: "payments", Version: "1.0.1", Baseline: "1.0.0", RequiredBump: "major",
		Checks: []publish.Check{{Name: "version", Message: "needs a major bump"}}}
	env := (&Service{}).env()
	tools := newExecTools(t.TempDir(), nil, nil)
	b := &contract.Bundle{Contract: &contract.Contract{Publish: &contract.Publish{Bundle: "oci://reg/pacto/payments"}}}
	if got := keepRefusal(t.Context(), env, tools, b, publish.Plan{}); got != "" {
		t.Fatalf("no version refusal: %q", got)
	}
	if got := keepRefusal(t.Context(), env, tools, b, refused); !strings.Contains(got, "publish.sign is not configured") {
		t.Fatalf("unsigned: %q", got)
	}
	b.Contract.Publish.Sign = &contract.PublishSign{Key: "missing.key"}
	notKept := func(why string) {
		t.Helper()
		if got := keepRefusal(t.Context(), env, tools, b, refused); !strings.HasPrefix(got, "the refusal was not kept in reg/pacto/payments: ") {
			t.Fatalf("%s: %q", why, got)
		}
	}
	notKept("a bundle that cannot be packed")
	b.FS = fstest.MapFS{}
	notKept("no key")
	dir := t.TempDir()
	signingKey(t, dir)
	tools = newExecTools(dir, nil, nil)
	b.Contract.Publish.Sign = &contract.PublishSign{Key: "cosign.key"}
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadRequest) }))
	defer down.Close()
	old := rekorURL
	rekorURL = down.URL
	defer func() { rekorURL = old }()
	notKept("a transparency log that is down")
}
