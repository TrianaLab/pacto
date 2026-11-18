package app

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
)

// orgMQCatalog classifies the corporate message queue by its host.
const orgMQCatalog = "apiVersion: catalog.pacto.trianalab.io/v1\nentries:\n  - identity: corp/mq\n    class: legacy\n    label: Corporate MQ\n    hosts: [mq.corp.example]\n"

// unreachableCatalogRef is pinned by digest, and no registry serves it.
const unreachableCatalogRef = "oci://127.0.0.1:1/acme/external-catalog@sha256:0000000000000000000000000000000000000000000000000000000000000000"

// mqFolder is the completion fixture under a 2.5 contract with a values file
// that names the corporate message queue.
func mqFolder(t *testing.T) string {
	t.Helper()
	dir := completionFolder(t, `pactoVersion: "2.5"`+"\n")
	writeFiles(t, dir, map[string]string{"chart/Chart.yaml": "apiVersion: v2\nname: app\nversion: 1.0.0\n",
		"chart/values.yaml": "MQ_URL: amqp://mq.corp.example:5672\n"})
	return dir
}

func TestCatalogInputsNameUsedCatalogs(t *testing.T) {
	reg := provenanceTestRegistry(t)
	ref := pushOrgCatalog(t, reg, orgMQCatalog)
	_, digest, _ := strings.Cut(ref, "@")
	def := "default " + externalcatalog.DefaultDigest()

	r, err := (&Service{ExternalCatalogRef: ref}).Init(t.Context(), InitOptions{Dir: mqFolder(t), Plan: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Inferred.Inputs.Catalog; !slices.Equal(got, []string{def, "organization " + digest}) {
		t.Fatalf("inputs = %v", got)
	}
	i := slices.IndexFunc(r.Inferred.Fields, func(f contract.Inferred) bool { return f.External != nil && f.External.Identity == "corp/mq" })
	if i < 0 || r.Inferred.Fields[i].External.Catalog != externalcatalog.OriginOrganization {
		t.Fatalf("corp/mq = %+v", r.Inferred.Fields)
	}

	r, err = (&Service{ExternalCatalogRef: unreachableCatalogRef}).Init(t.Context(), InitOptions{Dir: mqFolder(t), Plan: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Inferred.Inputs.Catalog; !slices.Equal(got, []string{def}) {
		t.Fatalf("unreachable: inputs = %v", got)
	}
	if !slices.ContainsFunc(r.Unknown, func(u string) bool { return strings.Contains(u, "could not be read") }) {
		t.Fatalf("unknown = %v", r.Unknown)
	}
}

func TestPackUsesExternalCatalog(t *testing.T) {
	reg := provenanceTestRegistry(t)
	ref := pushOrgCatalog(t, reg, orgMQCatalog)
	_, digest, _ := strings.Cut(ref, "@")
	rec, err := contract.ParseInferredRecord([]byte(packedWith(t, &Service{ExternalCatalogRef: ref}, mqFolder(t))[contract.InferredRecordFile]))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(rec.Inputs.Catalog, "organization "+digest) {
		t.Fatalf("inputs = %v", rec.Inputs.Catalog)
	}
	// An unreachable catalog warns and packs with the default alone.
	rec, err = contract.ParseInferredRecord([]byte(packedWith(t, &Service{ExternalCatalogRef: unreachableCatalogRef}, mqFolder(t))[contract.InferredRecordFile]))
	if err != nil || !slices.Equal(rec.Inputs.Catalog, []string{"default " + externalcatalog.DefaultDigest()}) {
		t.Fatalf("unreachable: %v %v", err, rec)
	}
}

// Every local completion refuses an organization catalog that is not pinned
// by digest, before it reads the folder.
func TestCompletionRefusesCatalogTag(t *testing.T) {
	dir := mqFolder(t)
	old, err := readLocalBundle(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The old revision comes from a registry, so only the new one completes.
	store := &mockBundleStore{PullFn: func(context.Context, string) (*contract.Bundle, error) { return old, nil }}
	s := &Service{ExternalCatalogRef: "oci://example.com/cat:latest", BundleStore: store}
	ctx := context.Background()
	calls := map[string]func() error{
		"init": func() error { _, err := s.Init(ctx, InitOptions{Dir: dir, Plan: true}); return err },
		"pack": func() error {
			_, err := s.Pack(ctx, PackOptions{Path: dir, Output: t.TempDir() + "/b.tar.gz"})
			return err
		},
		"push": func() error {
			_, err := s.Push(ctx, PushOptions{Path: dir, Ref: "oci://r.example/app:1.0.0"})
			return err
		},
		"publish": func() error { _, err := s.Publish(ctx, PublishOptions{Path: dir, Plan: true}); return err },
		"explain": func() error { _, err := s.Explain(ctx, ExplainOptions{Path: dir}); return err },
		"impact": func() error {
			_, err := s.Impact(ctx, ImpactOptions{OldPath: "oci://r.example/app:1.0.0", NewPath: dir, NewDirName: "app"})
			return err
		},
	}
	for name, call := range calls {
		if err := call(); err == nil || !strings.Contains(err.Error(), "pin it by digest") {
			t.Errorf("%s: %v", name, err)
		}
	}
}
