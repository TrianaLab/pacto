package evidenceoci

import (
	"bytes"
	"encoding/json"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/trianalab/pacto/v4/internal/testutil"
)

func manifestAnnotations(t *testing.T, art Artifact) map[string]string {
	t.Helper()
	var m ocispec.Manifest
	if err := json.Unmarshal(art.Manifest, &m); err != nil {
		t.Fatal(err)
	}
	return m.Annotations
}

func TestCommitStampsAcceptingCluster(t *testing.T) {
	s, subj := oneSubjectStore(t)
	mustCommit(t, s.WithCluster("uid-a"), storeRecord(subj, "remote-eu", "env-1", 1, "orders", acceptedAt))
	res := s.List(t.Context())
	if len(res.Records) != 1 || res.Records[0].Cluster != "uid-a" {
		t.Fatalf("records = %+v; want one accepted in uid-a", res.Records)
	}
	// The annotation sits beside the payload: the payload a cluster-unaware
	// server writes and strictly decodes stays byte-identical.
	plain := mustBuild(t, testRecord(subj), subj)
	rec := testRecord(subj)
	rec.Cluster = "uid-a"
	stamped := mustBuild(t, rec, subj)
	if !bytes.Equal(plain.Payload, stamped.Payload) {
		t.Fatal("the cluster changed the payload")
	}
	if got := manifestAnnotations(t, stamped)[acceptedClusterAnnotation]; got != "uid-a" {
		t.Fatalf("annotation = %q, want uid-a", got)
	}
}

func TestCommitWithoutClusterWritesNoAnnotation(t *testing.T) {
	s, subj := oneSubjectStore(t)
	mustCommit(t, s, storeRecord(subj, "remote-eu", "env-1", 1, "orders", acceptedAt))
	if res := s.List(t.Context()); len(res.Records) != 1 || res.Records[0].Cluster != "" {
		t.Fatalf("records = %+v; want one with no cluster", res.Records)
	}
	art := mustBuild(t, testRecord(subj), subj)
	if a := manifestAnnotations(t, art); a != nil || art.ManifestDesc.Annotations != nil {
		t.Fatalf("annotations = %v; a server without a cluster writes none", a)
	}
}

// scanOne publishes rec and returns what a scan reads back.
func scanOne(t *testing.T, cluster string) (string, int) {
	t.Helper()
	host := startRegistry(t, testutil.ReferrersOptions{})
	subj, desc := seedContract(t, host, RepositoryOptions{})
	rec := recordN(subj, 1)
	rec.Cluster = cluster
	art, err := BuildArtifact(rec, subj, desc)
	if err != nil {
		t.Fatal(err)
	}
	repo := openRepo(t, host, RepositoryOptions{})
	publish(t, repo, art)
	got := mustScan(t, repo, subj, desc)
	if len(got.Found) != 1 {
		t.Fatalf("found %d records", len(got.Found))
	}
	return got.Found[0].Cluster, got.Invalid
}

func TestScanReadsAcceptingCluster(t *testing.T) {
	if cluster, invalid := scanOne(t, "9f1c4e2a-0b1d-4c2e-8f3a-1b2c3d4e5f60"); cluster != "9f1c4e2a-0b1d-4c2e-8f3a-1b2c3d4e5f60" || invalid != 0 {
		t.Fatalf("cluster = %q, invalid = %d", cluster, invalid)
	}
}

func TestScanDropsInvalidClusterAnnotation(t *testing.T) {
	if cluster, invalid := scanOne(t, "-not a cluster"); cluster != "" || invalid != 0 {
		t.Fatalf("cluster = %q, invalid = %d; want the record kept without a cluster", cluster, invalid)
	}
}
