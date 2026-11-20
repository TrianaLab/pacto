package detect

import (
	"cmp"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// MaxPushes caps the push prefixes an installation advertises.
const MaxPushes = 8

// PushResult is the last target snapshot push the operator attempted: when,
// to which ref and, on failure, why. A failed push is never shown as healthy.
type PushResult struct {
	At    time.Time `json:"at,omitzero"`
	Ref   string    `json:"ref,omitempty"`
	Error string    `json:"error,omitempty"`
}

// Installation is what the operator was configured with, so CI and people
// read it: the registry value, the fixed repositories under it, every prefix
// a cluster-side process may push under, the installation's public snapshot
// key and the last snapshot push. It is declared configuration, never
// inferred, and it sets nothing.
type Installation struct {
	Registry string `json:"registry"`
	// Targets, Index and Inferred are <registry>/pacto-targets, pacto-index
	// and pacto-inferred.
	Targets  string `json:"targets,omitempty"`
	Index    string `json:"index,omitempty"`
	Inferred string `json:"inferred,omitempty"`
	// SnapshotKey is the base64 of the installation public key's DER;
	// SnapshotKeyID is its signing hint.
	SnapshotKey   string `json:"snapshotKey,omitempty"`
	SnapshotKeyID string `json:"snapshotKeyID,omitempty"`
	// Pushes is every prefix a cluster-side process may push under.
	Pushes   []string    `json:"pushes,omitempty"`
	LastPush *PushResult `json:"lastPush,omitempty"`
}

// errSnapshotKey refuses an advertised key that is not base64.
var errSnapshotKey = errors.New("detection installation snapshotKey is not base64")

// checkInstallation refuses an installation a reader would cut or could not
// use: more than MaxPushes prefixes or a snapshot key that is not base64.
func checkInstallation(inst *Installation) error {
	if inst == nil {
		return nil
	}
	if len(inst.Pushes) > MaxPushes {
		return fmt.Errorf("detection installation names %d push prefixes, more than %d", len(inst.Pushes), MaxPushes)
	}
	if _, err := base64.StdEncoding.DecodeString(inst.SnapshotKey); err != nil {
		return errSnapshotKey
	}
	return nil
}

// sanitizeInstallation cleans every string of inst and caps its prefixes.
func sanitizeInstallation(inst *Installation) {
	if inst == nil {
		return
	}
	for _, s := range []*string{&inst.Registry, &inst.Targets, &inst.Index, &inst.Inferred, &inst.SnapshotKey, &inst.SnapshotKeyID} {
		*s = clean(*s)
	}
	inst.Pushes = cleanAll(inst.Pushes, MaxPushes)
	if p := inst.LastPush; p != nil {
		p.Ref, p.Error = clean(p.Ref), clean(p.Error)
	}
}

// installationRow is the one declared row: the registry, every push prefix,
// the fixed repositories and the snapshot key. It is partial until a push has
// succeeded, and a failed push says why, so it never reads healthy unconfirmed.
func installationRow(res Result, source string, now time.Time) fleet.Provider {
	inst, asOf := res.Installation, res.AsOf
	evidence := "registry " + inst.Registry + "; pushes only under the listed prefixes"
	completeness, why := Partial, ""
	switch p := inst.LastPush; {
	case p == nil:
		why = "push not confirmed yet"
	case p.Error != "":
		why = "last push failed: " + p.Error
	default:
		completeness = Complete
	}
	var lims []fleet.Limitation
	if why != "" {
		evidence += "; " + why
		lims = []fleet.Limitation{{Code: "INSTALLATION_PUSH_UNCONFIRMED", Source: source, Message: why}}
	}
	var details []string
	for _, d := range [][2]string{{"target snapshot ", inst.Targets}, {"organization index ", inst.Index}, {"inferred contracts ", inst.Inferred}} {
		if d[1] != "" {
			details = append(details, d[0]+d[1])
		}
	}
	for _, p := range inst.Pushes {
		details = append(details, "push prefix "+p)
	}
	if inst.SnapshotKeyID != "" {
		details = append(details, "snapshot key "+inst.SnapshotKeyID)
	}
	if p := inst.LastPush; p != nil && p.Error == "" {
		details = append(details, "last push "+p.Ref+" at "+p.At.UTC().Format(time.RFC3339))
	}
	return fleet.Provider{
		ID: "installation", Name: "Installation", Evidence: evidence,
		UsedFor:    []string{"target snapshot, organization index and inferred contracts; set by a person with the registry value"},
		Status:     status(completeness, now.Sub(asOf)),
		Provenance: fleet.ProvenanceDeclared, AsOf: &asOf, Details: details, Limitations: lims,
		Cluster: cmp.Or(res.ClusterName, res.Cluster), ClusterID: res.Cluster,
	}
}
