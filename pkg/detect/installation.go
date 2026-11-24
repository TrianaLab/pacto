package detect

import (
	"cmp"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// MaxPushes caps the push prefixes an installation advertises.
const MaxPushes = 8

// MaxProfiles caps the profile names an installation advertises.
const MaxProfiles = 3

// MaxObservedOfferings caps the offering slugs an installation advertises:
// the operator maps at most this many source objects per run.
const MaxObservedOfferings = 200

// OfferingsAd is what an installation's inferred-offering producer
// advertises: its producer id, the one repository it pushes offerings to
// (<registry>/pacto-inferred/<producer>/services), the sorted slugs its last
// complete run observed, when that run finished and, when the newest run
// stopped short, why. A reader never reads a slug missing from Observed as
// deleted while Partial is set.
type OfferingsAd struct {
	Producer   string     `json:"producer,omitempty"`
	Repository string     `json:"repository,omitempty"`
	Observed   []string   `json:"observed,omitempty"`
	AsOf       *time.Time `json:"asOf,omitempty"`
	Partial    string     `json:"partial,omitempty"`
}

// OfferingsRepository is the repository r's installation advertises its
// inferred offerings in, when it is that installation's own: the
// contribution repository of its producer under its registry, the producer
// id ending in -<the cluster id r was read in>. Any other repository is
// never read, so two installations sharing a registry read apart and one
// never makes Pacto pull another's.
func (r Result) OfferingsRepository() (string, bool) {
	inst := r.Installation
	if inst == nil || inst.Offerings == nil || inst.Registry == "" || r.Cluster == "" {
		return "", false
	}
	o := inst.Offerings
	p, ok := contract.ContributionRef(o.Repository)
	if !ok || p != o.Producer || !strings.HasSuffix(o.Producer, "-"+r.Cluster) || o.Repository != contract.ContributionRepository(inst.Registry, o.Producer) {
		return "", false
	}
	return o.Repository, true
}

// PushResult is the last target snapshot push the operator attempted: when,
// to which ref and, on failure, why. A failed push is never shown as healthy.
type PushResult struct {
	At    time.Time `json:"at,omitzero"`
	Ref   string    `json:"ref,omitempty"`
	Error string    `json:"error,omitempty"`
}

// Installation is what the operator was configured with, so CI and people
// read it: the installation profile, the registry value, the fixed repositories under it, every prefix
// a cluster-side process may push under, the installation's public snapshot
// key and the last snapshot push. It is declared configuration, never
// inferred, and it sets nothing.
type Installation struct {
	// Profile is the sorted profile names the chart rendered; nil from an
	// operator that does not advertise it.
	Profile  []string `json:"profile,omitempty"`
	Registry string   `json:"registry"`
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
	// ChartVersion is the chart version the operator runs, the one a
	// generated member install pins; empty when it is not advertised. A
	// value with any character outside semver's, a trailing space
	// included, is dropped rather than trimmed.
	ChartVersion string `json:"chartVersion,omitempty"`
	// Offerings is the inferred-offering producer's advertisement; nil when
	// the installation produces no inferred offerings.
	Offerings *OfferingsAd `json:"offerings,omitempty"`
}

// Declared is the Installation block as the capability answer reads it, with
// the cluster it was read in and where: nil without a block.
func (r Result) Declared(source string) *fleet.InstallationDeclared {
	inst := r.Installation
	if inst == nil {
		return nil
	}
	return &fleet.InstallationDeclared{Profile: slices.Clone(inst.Profile), Registry: inst.Registry, ClusterID: r.Cluster, AsOf: r.AsOf,
		Source: "the operator's installation block in " + source}
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

// chartVersion is at most 64 bytes of semver characters.
var chartVersion = regexp.MustCompile(`^[0-9A-Za-z.+-]{0,64}$`)

// sanitizeInstallation cleans every string of inst and caps its prefixes and
// profiles.
func sanitizeInstallation(inst *Installation) {
	if inst == nil {
		return
	}
	for _, s := range []*string{&inst.Registry, &inst.Targets, &inst.Index, &inst.Inferred, &inst.SnapshotKey, &inst.SnapshotKeyID} {
		*s = clean(*s)
	}
	if inst.ChartVersion = clean(inst.ChartVersion); !chartVersion.MatchString(inst.ChartVersion) {
		inst.ChartVersion = ""
	}
	inst.Pushes = cleanAll(inst.Pushes, MaxPushes)
	inst.Profile = cleanAll(inst.Profile, MaxProfiles)
	if p := inst.LastPush; p != nil {
		p.Ref, p.Error = clean(p.Ref), clean(p.Error)
	}
	if o := inst.Offerings; o != nil {
		o.Producer, o.Repository, o.Partial = clean(o.Producer), clean(o.Repository), clean(o.Partial)
		o.Observed = cleanAll(o.Observed, MaxObservedOfferings)
	}
}

// installationRow is the one declared row: the profile, the registry, every
// push prefix, the fixed repositories and the snapshot key. With a registry it
// is partial until a push has succeeded, and a failed push says why, so it
// never reads healthy unconfirmed. Without one it is complete: the operator
// pushes nothing to confirm.
func installationRow(res Result, source string, now time.Time) fleet.Provider {
	inst, asOf := res.Installation, res.AsOf
	if inst.Registry == "" {
		return installationProvider(res, profileEvidence(inst)+"no registry value: the operator pushes nothing", Complete, now)
	}
	evidence := profileEvidence(inst) + "registry " + inst.Registry + "; tags only under the listed prefixes, referrers by digest elsewhere"
	completeness, why := Partial, ""
	switch p := inst.LastPush; {
	case p != nil && p.Error != "":
		why = "last push failed: " + p.Error
	case p == nil || p.Ref == "" || p.At.IsZero():
		why = "push not confirmed yet"
	case asOf.Sub(p.At) > StaleAfter:
		// A publisher that hangs keeps its last success: an old one is no
		// confirmation that pushes still work.
		why = "last push at " + p.At.UTC().Format(time.RFC3339) + " is older than " + StaleAfter.String()
	default:
		completeness = Complete
	}
	var lims []fleet.Limitation
	if why != "" {
		evidence += "; " + why
		lims = []fleet.Limitation{{Code: "INSTALLATION_PUSH_UNCONFIRMED", Source: source, Message: why}}
	}
	// At most four lines, within the MaxDetails every Provider row keeps: the
	// three fixed repositories, every push prefix, the key and the last push.
	var refs []string
	for _, d := range [][2]string{{"target snapshot ", inst.Targets}, {"organization index ", inst.Index}, {"inferred contracts ", inst.Inferred}} {
		if d[1] != "" {
			refs = append(refs, d[0]+d[1])
		}
	}
	var details []string
	if len(refs) > 0 {
		details = append(details, strings.Join(refs, "; "))
	}
	if len(inst.Pushes) > 0 {
		details = append(details, "push prefixes "+strings.Join(inst.Pushes, ", "))
	}
	if inst.SnapshotKeyID != "" {
		details = append(details, "snapshot key "+inst.SnapshotKeyID)
	}
	if p := inst.LastPush; completeness == Complete {
		details = append(details, "last push "+p.Ref+" at "+p.At.UTC().Format(time.RFC3339))
	}
	p := installationProvider(res, evidence, completeness, now)
	p.Details, p.Limitations = details, lims
	return p
}

// profileEvidence is the row's leading "profile <names>; ", empty for a block
// from an operator that does not advertise its profile.
func profileEvidence(inst *Installation) string {
	if len(inst.Profile) == 0 {
		return ""
	}
	return "profile " + strings.Join(inst.Profile, ", ") + "; "
}

// installationProvider is the declared Installation row with no details.
func installationProvider(res Result, evidence, completeness string, now time.Time) fleet.Provider {
	asOf := res.AsOf
	feeds := "target snapshot, organization index and inferred contracts"
	usedFor := feeds + "; set by a person with the registry value"
	switch {
	case len(res.Installation.Profile) > 0 && res.Installation.Registry != "":
		// A profiled row with a registry still says what the registry feeds.
		usedFor = "installation profile, " + feeds + "; set by a person with the profile and registry values"
	case len(res.Installation.Profile) > 0:
		usedFor = "installation profile and registry; set by a person with the profile and registry values"
	}
	return fleet.Provider{
		ID: "installation", Name: "Installation", Evidence: evidence,
		UsedFor:    []string{usedFor},
		Status:     status(completeness, now.Sub(asOf)),
		Provenance: fleet.ProvenanceDeclared, AsOf: &asOf,
		Cluster: cmp.Or(res.ClusterName, res.Cluster), ClusterID: res.Cluster,
	}
}
