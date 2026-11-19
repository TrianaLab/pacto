package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// The target snapshot: what one cluster runs, signed by its delivery recorder
// and tagged cluster-<uid> in <registry>/pacto-targets, so CI reads it with
// registry credentials and no kubeconfig.
const (
	// PredicateTargetSnapshot is the predicate type of a signed target snapshot.
	PredicateTargetSnapshot = "delivery.pacto.trianalab.io/target-snapshot/v1"
	// TargetSnapshotArtifactType is the artifact type of the plain manifest
	// that carries one: it has no subject, so no registry needs Referrers.
	TargetSnapshotArtifactType = "application/vnd.pacto.target-snapshot.v1+json"
	// TargetSnapshotRepository is the fixed installation repository name under
	// the registry, and the only repository a snapshot is pushed to.
	TargetSnapshotRepository = "pacto-targets"
	// TargetSnapshotTagPrefix starts every snapshot tag, so a reader lists
	// cluster-* tags only.
	TargetSnapshotTagPrefix = "cluster-"
	// TargetSnapshotInterval is how often a recorder publishes.
	TargetSnapshotInterval = 5 * time.Minute
	// TargetSnapshotFreshness is the age past which a snapshot reads stale:
	// three missed publishes.
	TargetSnapshotFreshness = 15 * time.Minute
	// MaxTargetSnapshotBytes bounds one signed snapshot, written or read.
	MaxTargetSnapshotBytes = 8 << 20
	// MaxTargetSnapshots bounds how many cluster tags a reader reads per
	// repository.
	MaxTargetSnapshots = 32
	// TargetSnapshotReadTimeout bounds a whole snapshot resolution, so a slow
	// registry never holds a CI gate.
	TargetSnapshotReadTimeout = 20 * time.Second
)

// TargetSnapshot is delivery.pacto.trianalab.io/target-snapshot/v1: the Pacto
// objects a cluster's recorder can read and the workloads no Pacto targets.
type TargetSnapshot struct {
	// Cluster is the cluster UID (the kube-system namespace UID).
	Cluster string `json:"cluster"`
	// AsOf is when the snapshot was taken (RFC 3339).
	AsOf string `json:"asOf"`
	// Completeness is complete or partial, as the publisher saw it.
	Completeness fleet.Completeness `json:"completeness"`
	// Limitations say what the publisher could not read.
	Limitations []fleet.Limitation `json:"limitations,omitempty"`
	// RegistryMode is the registry mode the publisher detected, such as
	// "referrers=write-once-tags listing=tags"; empty when unknown.
	RegistryMode string `json:"registryMode,omitempty"`
	// Publisher is who published, as the publisher claims it. A reader names
	// the signer from its own trust config, never from this field.
	Publisher string `json:"publisher"`
	// Pactos are the Pacto objects, as the stable read contract.
	Pactos []PactoView `json:"pactos"`
	// Workloads are the workloads no Pacto targets, as inferred services.
	Workloads []fleet.RawInferredService `json:"workloads"`
}

// TargetSnapshotStatement is an in-toto Statement v1 carrying a target snapshot.
type TargetSnapshotStatement struct {
	Type          string         `json:"_type"`
	Subject       []Subject      `json:"subject"`
	PredicateType string         `json:"predicateType"`
	Predicate     TargetSnapshot `json:"predicate"`
}

// NewTargetSnapshotStatement builds the statement of s: its subject is the
// cluster's tag, by the sha256 of the snapshot JSON.
func NewTargetSnapshotStatement(s TargetSnapshot) TargetSnapshotStatement {
	body, _ := json.Marshal(s) // plain data; marshalling cannot fail
	return TargetSnapshotStatement{Type: StatementType,
		Subject:       []Subject{{Name: TargetSnapshotRepository + ":" + TargetSnapshotTagPrefix + s.Cluster, Digest: map[string]string{"sha256": sha256Hex(body)}}},
		PredicateType: PredicateTargetSnapshot, Predicate: s}
}

// SignTargetSnapshot signs st and returns the bundle.
func SignTargetSnapshot(ctx context.Context, s Signer, st TargetSnapshotStatement) ([]byte, error) {
	bundle, _, err := signStatement(ctx, s, st)
	return bundle, err
}

// clusterPattern is what a cluster id may hold to sit in a tag.
var clusterPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,100}$`)

// TargetSnapshotTag is the tag of cluster's snapshot, cluster-<uid>. A cluster
// that cannot sit in a tag is an error.
func TargetSnapshotTag(cluster string) (string, error) {
	if !clusterPattern.MatchString(cluster) {
		return "", fmt.Errorf("cluster %q: want 1 to 100 letters, digits or dashes", cluster)
	}
	return TargetSnapshotTagPrefix + cluster, nil
}

// Check refuses a snapshot a reader could not place: a cluster that cannot sit
// in a tag, an asOf that is not RFC 3339, a completeness other than complete
// or partial, or no publisher.
func (s TargetSnapshot) Check() error {
	if _, err := TargetSnapshotTag(s.Cluster); err != nil {
		return err
	}
	if _, err := time.Parse(time.RFC3339, s.AsOf); err != nil {
		return fmt.Errorf("asOf %q: want RFC 3339", s.AsOf)
	}
	if s.Completeness != fleet.CompletenessComplete && s.Completeness != fleet.CompletenessPartial {
		return fmt.Errorf("completeness %q: want %s or %s", s.Completeness, fleet.CompletenessComplete, fleet.CompletenessPartial)
	}
	if s.Publisher == "" {
		return errors.New("publisher is empty")
	}
	return nil
}
