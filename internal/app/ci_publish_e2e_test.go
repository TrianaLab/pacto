package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"

	"github.com/trianalab/pacto/v4/internal/ci"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// e2eEpoch is the commit time of the e2e repository.
const e2eEpoch = "1767225600"

// fixedRun builds one fixed image per folder, whoever runs it and whenever,
// and answers git as a clean checkout at e2eEpoch holding every revision.
type fixedRun struct{}

func (fixedRun) run(_ context.Context, dir string, _ []string, name string, args ...string) ([]byte, error) {
	switch {
	case name == "git" && args[0] == "show":
		return []byte(e2eEpoch + "\n"), nil
	case name == "git":
		return nil, nil
	case name == "docker" && args[1] == "inspect":
		return []byte("Driver: docker-container\n"), nil
	case name == "docker":
		dest, _, _ := strings.Cut(argOf(args, "type=oci,dest="), ",")
		return nil, fixedLayout(dest, filepath.Base(dir))
	case name == "ko":
		return nil, fixedLayout(argOf(args, "--oci-layout-path="), filepath.Base(dir))
	}
	return nil, fmt.Errorf("unexpected program %s", name)
}

// fixedLayout writes an OCI layout holding the one image of folder.
func fixedLayout(dir, folder string) error {
	img := mutate.MediaType(empty.Image, types.OCIManifestSchema1)
	img, err := mutate.AppendLayers(img, static.NewLayer([]byte("image of "+folder), types.OCILayer))
	if err != nil {
		return err
	}
	p, err := layout.Write(dir, empty.Index)
	if err != nil {
		return err
	}
	return p.AppendImage(img)
}

// e2eRepo is a repository of three section-less 2.5 contracts with no
// version: ledger built with ko, payments with a Dockerfile, and reports, a
// Dockerfile and a chart, depending on ledger. A swappable handler serves
// the registry, so a run can start from an empty one at the same address.
type e2eRepo struct {
	root, host string
	handler    atomic.Pointer[http.Handler]
	reg        *hookRegistry
	svc        *Service
}

func newE2ERepo(t *testing.T) *e2eRepo {
	t.Helper()
	privateSigstore(t, ciWorkflow)
	e := &e2eRepo{root: t.TempDir()}
	e.emptyRegistry()
	srv := probedServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { (*e.handler.Load()).ServeHTTP(w, r) }))
	t.Cleanup(srv.Close)
	e.host = strings.TrimPrefix(srv.URL, "http://")
	e.reg = &hookRegistry{Registry: publish.NewClient(anonymous, e.host)}
	e.svc = publishService(&fakeRun{}, e.reg)
	e.svc.sys.run = fixedRun{}.run
	contract := func(name, extra string) string {
		return "pactoVersion: \"2.5\"\nservice:\n  name: " + name + "\n" + extra
	}
	writeFiles(t, e.root, map[string]string{
		"ledger/pacto.yaml":      contract("ledger", ""),
		"ledger/.ko.yaml":        "builds:\n  - main: ./cmd/api\n",
		"ledger/k8s/deploy.yaml": deployment(e.host + "/acme/ledger:1.0"),

		"payments/pacto.yaml":      contract("payments", ""),
		"payments/Dockerfile":      "FROM scratch\n",
		"payments/k8s/deploy.yaml": deployment("r.io/payments:1"),

		"reports/pacto.yaml": contract("reports", "dependencies:\n  - name: ledger\n    ref: oci://"+e.host+"/acme/contracts/ledger\n    compatibility: \"^1\"\n"),
	})
	for p, v := range chartFolder {
		writeFiles(t, e.root, map[string]string{"reports/" + p: strings.ReplaceAll(v, "payments", "reports")})
	}
	return e
}

// emptyRegistry serves a fresh, empty registry at the same address.
func (e *e2eRepo) emptyRegistry() {
	h := registry.New(registry.WithReferrersSupport(true), registry.Logger(log.New(io.Discard, "", 0)))
	e.handler.Store(&h)
}

// e2eRun is one pacto ci --publish run: its printed plan, its JSON and every
// digest it published, the images record included, by "<service> <item>".
type e2eRun struct {
	plan        *ci.Plan
	text, json  string
	digests     map[string]string
	writesAfter int64
}

func (e *e2eRepo) publish(t *testing.T, revision string) e2eRun {
	t.Helper()
	before := e.reg.writes.Load()
	out := e2eRun{digests: map[string]string{}}
	p, err := e.svc.CIPublish(t.Context(), CIPublishOptions{
		CI:       CIOptions{Root: e.root, Registry: e.host + "/acme", Getenv: mapEnv(map[string]string{"SOURCE_DATE_EPOCH": e2eEpoch})},
		Revision: revision,
		Record: func(r ci.Row, res *publish.Result, _ error) {
			if res == nil || len(res.Actions) == 0 {
				return
			}
			for item, d := range res.Digests {
				out.digests[r.Service+" "+item] = d
			}
			bundle := res.Plan.Items[len(res.Plan.Items)-1]
			repo := bundle.Ref[:strings.LastIndex(bundle.Ref, ":")]
			images, err := e.reg.Referrers(t.Context(), repo, res.Digests[publish.BundleItem], publish.ImagesType)
			if err != nil || len(images) != 1 {
				t.Fatalf("%s images record: %v %v", r.Service, images, err)
			}
			out.digests[r.Service+" images"] = images[0]
		}})
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	ci.Text(&b, p)
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	out.plan, out.text, out.json, out.writesAfter = p, b.String(), string(raw), e.reg.writes.Load()-before
	return out
}

// TestCIPublishByteIdentical: two runs over the same commit, each into an
// empty registry at the same address, publish the same digest for every
// bundle, image, chart, deploy artifact and images record, and print the
// same plan byte for byte.
func TestCIPublishByteIdentical(t *testing.T) {
	e := newE2ERepo(t)
	first := e.publish(t, "r1")
	e.emptyRegistry()
	second := e.publish(t, "r1")
	if first.plan.Exit != 0 || !slices.Equal(services(first.plan), []string{"ledger", "payments", "reports"}) {
		t.Fatalf("first run:\n%s", first.text)
	}
	for _, r := range first.plan.Rows {
		if r.State != ci.StatePublished || r.To != "1.0.0" || len(r.Images) != 1 || slices.ContainsFunc(r.Notes, func(n string) bool { return strings.HasPrefix(n, "not reproducible") }) {
			t.Fatalf("row %+v", r)
		}
	}
	keys := slices.Sorted(maps.Keys(first.digests))
	for _, want := range []string{"ledger bundle", "ledger image", "ledger images", "ledger manifests", "reports chart", "payments image"} {
		if !slices.Contains(keys, want) {
			t.Fatalf("no %q digest among %v", want, keys)
		}
	}
	if !maps.Equal(first.digests, second.digests) {
		t.Fatalf("digests differ:\n%v\n%v", first.digests, second.digests)
	}
	if first.text != second.text || first.json != second.json {
		t.Fatalf("outputs differ:\n%s\n%s\n%s\n%s", first.text, second.text, first.json, second.json)
	}
}

// TestCIPublishSecondRunWritesNothing: a second run over the same registry
// finds every service unchanged and writes nothing.
func TestCIPublishSecondRunWritesNothing(t *testing.T) {
	e := newE2ERepo(t)
	if first := e.publish(t, "r1"); first.plan.Exit != 0 || first.writesAfter == 0 {
		t.Fatalf("first run:\n%s", first.text)
	}
	second := e.publish(t, "r2")
	for _, r := range second.plan.Rows {
		if r.State != ci.StateUnchanged {
			t.Fatalf("row %+v", r)
		}
	}
	if second.writesAfter != 0 || second.plan.Exit != 0 {
		t.Fatalf("second run wrote %d times:\n%s", second.writesAfter, second.text)
	}
}

// TestCIPublishThenDeriveNext: a change after the first run publishes the
// next version of that service only, derived from the verified baseline.
func TestCIPublishThenDeriveNext(t *testing.T) {
	e := newE2ERepo(t)
	e.publish(t, "r1")
	writeFiles(t, e.root, map[string]string{"ledger/notes.md": "# ledger\n"})
	next := e.publish(t, "r2")
	if r := pubRow(t, next.plan, "ledger"); r.State != ci.StatePublished || r.Baseline != "1.0.0" || r.To != "1.0.1" {
		t.Fatalf("ledger %+v", r)
	}
	for _, s := range []string{"payments", "reports"} {
		if r := pubRow(t, next.plan, s); r.State != ci.StateUnchanged {
			t.Fatalf("%s %+v", s, r)
		}
	}
}

// TestCIPublishCarriesRegistryDeclarationEndToEnd runs pacto declare
// invoices --accept ledger as ana, then pacto ci --publish over a two-line
// invoices pacto.yaml, and pulls what it published.
func TestCIPublishCarriesRegistryDeclarationEndToEnd(t *testing.T) {
	e := newDeclCIEnv(t)
	de := newDeclareEnv(t)
	de.host, de.reg, de.svc.sys.publishRegistry = e.host, e.inner, func() publish.Registry { return e.inner }
	e.ss, e.de.ss = de.ss, de.ss
	de.login(personToken("ana@acme.com"))
	declared := de.declare(t, de.req("ledger"))
	e.folder(t, "", ledgerManifest)
	r := e.publishCI(t, "r1")
	raw, rec := e.published(t, r)
	if r.State != ci.StatePublished || !strings.Contains(raw, "  - name: \"ledger\"\n") || !strings.Contains(raw, `compatibility: "^2.3.0"`) ||
		rec.Inputs.Declaration == nil || rec.Inputs.Declaration.Record != declared.Published || rec.Inputs.Declaration.Fields != "dependencies[ledger]" {
		t.Fatalf("%+v\n%s\n%s", r, raw, rec.Marshal())
	}
	_, digest, _ := strings.Cut(declared.Published, "@")
	rev := digest[:len("sha256:")+12]
	var text bytes.Buffer
	ci.Text(&text, &ci.Plan{Rows: []ci.Row{r}})
	for _, l := range []string{
		"invoices: dependencies.ledger: ^2.3.0 (oci://reg.io/acme/contracts/ledger), accepted in registry (rev " + rev + ")",
		"invoices: service.version: " + r.To + ", derived from " + declared.Tag + " (rev " + rev + ")",
	} {
		if !strings.Contains(text.String(), l+"\n") {
			t.Errorf("no %q in\n%s", l, text.String())
		}
	}
	out, _ := json.Marshal(r)
	if !strings.Contains(string(out), `"carried":[{"field":"dependencies[ledger]","value":"^2.3.0 (oci://reg.io/acme/contracts/ledger)","from":"registry","kind":"declared","record":"`+declared.Published+`"`) {
		t.Fatalf("%s", out)
	}
}
