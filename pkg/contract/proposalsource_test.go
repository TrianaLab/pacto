package contract

import (
	"strings"
	"testing"
	"testing/fstest"
)

// proposalRecord parses and verifies a record holding f in a bundle that
// lists no file f cites.
func proposalRecord(f Inferred) error {
	rec, err := ParseInferredRecord(recordWith(f, nil, ""))
	if err != nil {
		return err
	}
	return VerifyInferredRecord(fstest.MapFS{"openapi/v1.yaml": &fstest.MapFile{Data: []byte("openapi: 3.0.0\n")},
		"deploy/app.yaml": &fstest.MapFile{Data: []byte("kind: Deployment\n")}}, rec)
}

var (
	indexRef      = Inferred{Field: "dependencies[ledger].ref", Value: "oci://ghcr.io/acme/ledger", Source: "index:ghcr.io/acme/pacto-index:0123456789abcdef-2.4.0", Confidence: ConfidenceLikely}
	snapshotRange = Inferred{Field: "dependencies[ledger].compatibility", Value: "compatibility unknown", Proposed: "^2.3.0",
		Source: "snapshot:ghcr.io/acme/pacto-target-snapshots@" + digestOf("s"), Confidence: ConfidenceLikely}
	commitExpiry = Inferred{Field: "breaks[0].expires", Value: "2026-12-14", Source: "commit:0123456789ab +30d", Confidence: ConfidenceLikely}
)

// TestVerifyAcceptsProposalSources: what pacto ci proposes from names no
// bundle file, so a bundle carries it and verifies.
func TestVerifyAcceptsProposalSources(t *testing.T) {
	for _, f := range []Inferred{indexRef, snapshotRange, commitExpiry} {
		if err := proposalRecord(f); err != nil {
			t.Errorf("%s: %v", f.Source, err)
		}
	}
}

func TestVerifyRefusesMalformedSnapshotSource(t *testing.T) {
	for _, src := range []string{"snapshot:", "snapshot:ghcr.io/acme/snap:latest", "snapshot:ghcr.io/acme/snap@sha256:abc", "snapshot:../x@" + digestOf("s")} {
		f := snapshotRange
		f.Source = src
		if err := proposalRecord(f); err == nil {
			t.Errorf("%q accepted", src)
		}
	}
}

func TestParseIndexSource(t *testing.T) {
	ok := indexRef
	ok.Source = "index:127.0.0.1:5000/acme/pacto-index:0123456789abcdef-1.0.0_build"
	if err := proposalRecord(ok); err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{"index:", "index:ghcr.io/acme/pacto-index", "index:Acme:tag", "index:ghcr.io/acme/pacto-index:-bad"} {
		f := indexRef
		f.Source = src
		if err := proposalRecord(f); err == nil {
			t.Errorf("%q accepted", src)
		}
	}
}

func TestParseCommitSource(t *testing.T) {
	for _, src := range []string{"commit:", "commit:0123456789ab", "commit:0123456789 +30d", "commit:0123456789AB +30d", "commit:0123456789ab +7d"} {
		f := commitExpiry
		f.Source = src
		if err := proposalRecord(f); err == nil {
			t.Errorf("%q accepted", src)
		}
	}
}

// TestProposalSourcesKeepToTheirField: each proposal source sits only on the
// field family it proposes.
func TestProposalSourcesKeepToTheirField(t *testing.T) {
	cases := map[string]struct {
		f    Inferred
		want string
	}{
		"index on a range":     {Inferred{Field: "dependencies[ledger].compatibility", Value: "x", Source: indexRef.Source, Confidence: ConfidenceLikely}, "index: source"},
		"snapshot unproposed":  {Inferred{Field: snapshotRange.Field, Value: "x", Source: snapshotRange.Source, Confidence: ConfidenceLikely}, "snapshot: source"},
		"snapshot on a ref":    {Inferred{Field: "dependencies[ledger].ref", Value: "x", Source: snapshotRange.Source, Confidence: ConfidenceLikely}, "snapshot: source"},
		"commit on a version":  {Inferred{Field: "service.version", Value: "2026-12-14", Source: commitExpiry.Source, Confidence: ConfidenceLikely}, "go together"},
		"expiry from a file":   {Inferred{Field: "breaks[0].expires", Value: "2026-12-14", Source: "deploy/app.yaml", Confidence: ConfidenceLikely}, "go together"},
		"expiry is not a date": {Inferred{Field: "breaks[0].expires", Value: "soon", Source: commitExpiry.Source, Confidence: ConfidenceLikely}, "date"},
		"proposal from a file": {Inferred{Field: snapshotRange.Field, Value: "x", Proposed: "^1.0.0", Source: "deploy/app.yaml", Confidence: ConfidenceLikely}, "snapshot: source"},
	}
	for name, tc := range cases {
		if err := proposalRecord(tc.f); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
}
