package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/evidenceingest"
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
	// TargetSnapshotInterval is how often the operator publishes.
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
	// Sources say, per section, when it was read and how completely: kind
	// kubernetes for the Pactos, kubernetes-workloads for the workloads and
	// evidence-http for Evidence. With none, Completeness and Limitations
	// cover the Pactos and the workloads.
	Sources []SnapshotSource `json:"sources,omitempty"`
	// Evidence is the body the installation's Evidence Server answered on its
	// targets endpoint, verbatim: the targets it accepted from any producer.
	// A reader maps it with evidenceingest.TargetsCollection, as it maps the
	// body of an --evidence-url source.
	Evidence json.RawMessage `json:"evidence,omitempty"`
	// OwnerPolicy is the installation's owner policy, from the chart's
	// declare block: who may set a service owner. Absent when the chart
	// sets none.
	OwnerPolicy *OwnerPolicy `json:"ownerPolicy,omitempty"`
	// Join says how a hub may approve this cluster as a member: its display
	// name, the environments it seems to host and the key that signs the
	// snapshot. Absent when the publisher fills none.
	Join *SnapshotJoin `json:"join,omitempty"`
}

// SnapshotSource is one section of a target snapshot: its kind, when it was
// read, how completely and what could not be read.
type SnapshotSource struct {
	Kind         string             `json:"kind"`
	AsOf         string             `json:"asOf"`
	Completeness fleet.Completeness `json:"completeness"`
	Limitations  []fleet.Limitation `json:"limitations,omitempty"`
}

// snapshotSectionKinds are the section kinds a target snapshot may carry.
var snapshotSectionKinds = []string{fleet.SourceKindKubernetes, fleet.SourceKindKubernetesWorkloads, fleet.SourceKindEvidenceHTTP}

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
// or partial, no publisher, a section of an unknown or repeated kind or with
// the same faults, an evidence section over evidenceingest's body bound, an
// owner policy OwnerPolicy.Check refuses or a join block checkJoin refuses.
func (s TargetSnapshot) Check() error {
	if _, err := TargetSnapshotTag(s.Cluster); err != nil {
		return err
	}
	if err := checkRead(s.AsOf, s.Completeness); err != nil {
		return err
	}
	if s.Publisher == "" {
		return errors.New("publisher is empty")
	}
	seen := map[string]bool{}
	for _, sec := range s.Sources {
		if !slices.Contains(snapshotSectionKinds, sec.Kind) || seen[sec.Kind] {
			return fmt.Errorf("source kind %q: want one each of %s", sec.Kind, strings.Join(snapshotSectionKinds, ", "))
		}
		seen[sec.Kind] = true
		if err := checkRead(sec.AsOf, sec.Completeness); err != nil {
			return fmt.Errorf("source %s: %w", sec.Kind, err)
		}
	}
	if len(s.Evidence) > evidenceingest.MaxTargetsBodyBytes {
		return fmt.Errorf("evidence is %d bytes, over the %d byte bound", len(s.Evidence), evidenceingest.MaxTargetsBodyBytes)
	}
	if s.OwnerPolicy != nil {
		if err := s.OwnerPolicy.Check(); err != nil {
			return fmt.Errorf("owner policy: %w", err)
		}
	}
	if s.Join != nil {
		if err := checkJoin(*s.Join); err != nil {
			return fmt.Errorf("join: %w", err)
		}
	}
	return nil
}

// checkRead refuses an asOf that is not RFC 3339 and a completeness other
// than complete or partial.
func checkRead(asOf string, c fleet.Completeness) error {
	if _, err := time.Parse(time.RFC3339, asOf); err != nil {
		return fmt.Errorf("asOf %q: want RFC 3339", asOf)
	}
	if c != fleet.CompletenessComplete && c != fleet.CompletenessPartial {
		return fmt.Errorf("completeness %q: want %s or %s", c, fleet.CompletenessComplete, fleet.CompletenessPartial)
	}
	return nil
}

// TargetSnapshotStream is the stream a snapshot of cluster is verified under,
// <uid>/pacto-targets/snapshot, shaped like a record stream
// (<cluster>/<namespace>/<stage>) so a trust entry's streams scope it. Every
// cluster's operator can write every cluster-* tag, so a snapshot counts as
// verified only under a key scoped to its cluster: one whose streams hold a
// pattern that starts with the literal UID.
func TargetSnapshotStream(cluster string) string {
	return cluster + "/" + TargetSnapshotRepository + "/snapshot"
}

// TargetSnapshotVerdict says how a snapshot verified: Verified when a record
// key covering its stream signed it, Scoped when that key is scoped to the
// snapshot's cluster, and Key the trust entry that verified it.
type TargetSnapshotVerdict struct {
	Verified, Scoped bool
	Key              TrustedKey
}

// OpenTargetSnapshot decodes a target snapshot bundle and verifies it with
// trust; open turns one key into the check of a bundle (pkg/signing does). A
// nil trust reads it unverified. Otherwise the record keys scoped to the
// snapshot's cluster are tried first, then the other record keys that cover
// its stream; when none verifies, the error says why. A bundle that is not
// one signed target-snapshot/v1 statement, or a snapshot Check refuses, is an
// error.
func OpenTargetSnapshot(bundle []byte, trust *TrustConfig, open func(TrustedKey) (func([]byte) error, error)) (TargetSnapshotStatement, TargetSnapshotVerdict, error) {
	var st TargetSnapshotStatement
	if _, err := openStatement(bundle, PredicateTargetSnapshot, &st); err != nil {
		return TargetSnapshotStatement{}, TargetSnapshotVerdict{}, err
	}
	if err := st.Predicate.Check(); err != nil {
		return TargetSnapshotStatement{}, TargetSnapshotVerdict{}, err
	}
	if trust == nil {
		return st, TargetSnapshotVerdict{}, nil
	}
	cluster := st.Predicate.Cluster
	stream := TargetSnapshotStream(cluster)
	var scoped, unscoped []TrustedKey
	for _, k := range trust.Keys {
		switch {
		case k.Role != "" || !covers(k.Streams, stream):
		case scopedTo(k.Streams, cluster, stream):
			scoped = append(scoped, k)
		default:
			unscoped = append(unscoped, k)
		}
	}
	err := fmt.Errorf("no trusted key may sign the target snapshot of cluster %q", cluster)
	for i, keys := range [][]TrustedKey{scoped, unscoped} {
		for _, k := range keys {
			verify, verr := TrustConfig{Keys: []TrustedKey{k}}.Verifier(open)
			if verr == nil {
				verr = verify(stream, 0, bundle)
			}
			if verr == nil {
				return st, TargetSnapshotVerdict{Verified: true, Scoped: i == 0, Key: k}, nil
			}
			err = verr
		}
	}
	return st, TargetSnapshotVerdict{}, fmt.Errorf("signature: %w", err)
}

// scopedTo reports whether one of patterns starts with the literal cluster
// UID and covers stream.
func scopedTo(patterns []string, cluster, stream string) bool {
	for _, p := range patterns {
		if first, _, _ := strings.Cut(p, "/"); first == cluster && covers([]string{p}, stream) {
			return true
		}
	}
	return false
}
