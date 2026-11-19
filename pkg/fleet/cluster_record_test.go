package fleet

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

const clusterObject = "news/Deployment/newsletter"

// clusterRecordOf is a record that parses (so only bundle verification
// refuses it) holding fields read from a cluster: it has inputs.cluster and,
// when coverage is set, every source complete.
func clusterRecordOf(t *testing.T, coverage bool, fields ...contract.Inferred) []byte {
	t.Helper()
	sum := sha256.Sum256([]byte(recordManifest))
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat,
		Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "1",
			Sources: []contract.InferredSourceFile{{Path: "deploy/app.yaml", Digest: "sha256:" + hex.EncodeToString(sum[:])}},
			Cluster: &contract.InferredClusterInput{AsOf: "2026-11-14T10:02:00Z", Object: clusterObject, CallWindow: "24h", MaxItems: 10000}},
		Fields: fields}
	if coverage {
		for _, s := range contract.CoverageSources {
			rec.Coverage = append(rec.Coverage, contract.InferredCoverage{Source: s, State: contract.CoverageComplete})
		}
	}
	b := rec.Marshal()
	if _, err := contract.ParseInferredRecord(b); err != nil {
		t.Fatalf("the fixture must parse, so verification is what refuses it: %v", err)
	}
	return b
}

func clusterRuntimeDep() contract.Inferred {
	return contract.Inferred{Field: "dependencies[notification-worker]", Value: "notification-worker",
		Source: "cluster:runtime calls from " + clusterObject, Confidence: contract.ConfidenceLikely,
		AsOf: "2026-11-14T09:58:12Z", Completeness: contract.CoverageComplete}
}

func clusterProposal() contract.Inferred {
	return contract.Inferred{Field: "dependencies[notification-worker].compatibility", Value: "compatibility unknown", Proposed: "^2.3.0",
		Source: "cluster:fleet reported versions 2.3.1", Confidence: contract.ConfidenceLikely,
		AsOf: "2026-11-14T09:58:12Z", Completeness: contract.CoverageComplete, Declare: `compatibility: "^2.3.0"`}
}

// wantClusterRecordDropped: the record projects nothing and the snapshot says
// what it could not read.
func wantClusterRecordDropped(t *testing.T, q *Query) {
	t.Helper()
	s := q.SnapshotForSerialization()
	if n := len(newsletterEdges(s)[ProvenanceInferred]); n != 0 {
		t.Errorf("a bundle record with cluster facts projected %d inferred edges", n)
	}
	if !hasLimitation(s.Limitations, LimitationInferredRecordUnreadable) || s.Completeness != CompletenessPartial {
		t.Errorf("completeness %s, limitations %+v", s.Completeness, s.Limitations)
	}
	sd, rd := newsletterDetail(t, q)
	if !rd.InferredUnreadable || rd.Inferred != nil || !sd.InferredFields.Unreadable || sd.InferredFields.Total != 0 {
		t.Errorf("inferred fields reached the detail: %+v %+v", rd.Inferred, sd.InferredFields)
	}
}

func TestBuildDropsRecordWithClusterSource(t *testing.T) {
	o := validRecordOpts()
	o.record = clusterRecordOf(t, true, clusterRuntimeDep())
	wantClusterRecordDropped(t, recordFleet(t, o))
}

func TestClusterRuntimeDependencyNeverProjects(t *testing.T) {
	o := validRecordOpts()
	o.record = clusterRecordOf(t, false, clusterRuntimeDep(), clusterProposal())
	wantClusterRecordDropped(t, recordFleet(t, o))
}

// A compatibility key, a proposal or not, names no dependency edge, while
// the plain key beside it does (so the check is not vacuous).
func TestCompatibilityProposalIsNotAnEdge(t *testing.T) {
	o := validRecordOpts()
	o.record = nil
	q := recordFleet(t, o)
	snap := q.SnapshotForSerialization()
	rk := RevisionKey(revKeyOf(t, q, "newsletter"))
	rev := *snap.Revisions[rk]
	for _, key := range []string{"dependencies[notification-worker].compatibility", "dependencies[notification-worker].compatibility]",
		"dependencies[notification-worker]]", "dependencies[]"} {
		rev.Record = &contract.InferredRecord{Fields: []contract.Inferred{{Field: key, Value: "compatibility unknown", Proposed: "^2.3.0"}}}
		if edges := revisionInferredEdges(snap, rk, &rev); len(edges) != 0 {
			t.Errorf("%s made edges %+v", key, edges)
		}
	}
	rev.Record = &contract.InferredRecord{Fields: []contract.Inferred{{Field: "dependencies[notification-worker]", Value: "notification-worker"}}}
	if edges := revisionInferredEdges(snap, rk, &rev); len(edges) != 1 {
		t.Errorf("the plain dependency key made %d edges, want 1", len(edges))
	}
}
