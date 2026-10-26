// Package publish decides and carries out `pacto publish`: what a contract's
// publish section declares, as the version the contract states, never over an
// existing tag. It builds nothing and runs nothing itself: builders, signing
// and SBOMs come through Tools, and the registry through Registry. Every step
// re-reads the registry, so a publish that stopped halfway finishes when run
// again from the same revision, and running it twice changes nothing the
// second time.
//
// Experimental: not covered by semantic versioning. Delivery is Alpha, and
// this API may change in a minor release.
package publish

import (
	"context"
	"fmt"
	"strings"
	"time"

	mver "github.com/Masterminds/semver/v3"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/diff"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/semver"
)

// State is what the registry holds for one artifact's tag.
type State string

// The five states. Only absent is ever written.
const (
	// Absent: the registry answered that the tag does not exist.
	Absent State = "absent"
	// Identical: the tag holds exactly the digest Pacto computed.
	Identical State = "identical"
	// Adopt: the tag's provenance says this version from this revision, so an
	// earlier publish pushed it. Apply reuses its digest only when it is
	// signed by the configured identity, and refuses it as a conflict
	// otherwise: provenance annotations are not signed.
	Adopt State = "adopt"
	// Conflict: the tag holds something else. Nothing is overwritten.
	Conflict State = "conflict"
	// Unreadable: the registry could not be read. Unreadable is never absent.
	Unreadable State = "unreadable"
)

// Provenance annotations Pacto stamps on every manifest it pushes.
const (
	AnnotationVersion  = "org.opencontainers.image.version"
	AnnotationRevision = "org.opencontainers.image.revision"
)

// BundleItem is the name the contract bundle has among the plan's items.
const BundleItem = "bundle"

// Item is one thing publish writes and what the registry holds for it.
type Item struct {
	Name string `json:"name"`
	Type string `json:"type"`
	// Ref is repo:tag.
	Ref    string `json:"ref"`
	State  State  `json:"state"`
	Digest string `json:"digest,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// Check is one of Pacto's own checks. A failed check refuses the publish.
type Check struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Message string `json:"message"`
}

// Plan is what publishing this version would do, decided without writing.
type Plan struct {
	Service  string `json:"service"`
	Version  string `json:"version"`
	Revision string `json:"revision"`
	// Baseline is the highest published version below Version.
	Baseline       string    `json:"baseline,omitempty"`
	Classification string    `json:"classification,omitempty"`
	RequiredBump   diff.Bump `json:"requiredBump,omitempty"`
	Checks         []Check   `json:"checks"`
	// Items are the artifacts in declaration order, then the bundle.
	Items []Item `json:"items"`
	// Ready is true when every check passed and nothing conflicts or is unreadable.
	Ready bool `json:"ready"`
	// Missing counts the absent items: what a publish would write.
	Missing int `json:"missing"`
}

// Input is a validated local bundle and where it was built from.
type Input struct {
	Bundle *contract.Bundle
	// Revision is the source revision (a git commit), stamped on every manifest.
	Revision string
	// Checks are checks the caller ran (validation, local references, the lock).
	Checks []Check
	// Preflight, when set, returns more checks once the items are decided, so
	// a check that only matters for what will be written (a builder, a
	// signer, a clean checkout) can pass when nothing will be.
	Preflight func(Plan) []Check
}

// Repo turns a publish ref (oci://host/path) into a repository reference.
func Repo(ref string) string { return contract.TrimOCIScheme(ref) }

// Decide is the registry decision for one tag: unreadable when err is not nil,
// absent when nothing is there, identical when it holds digest, adopt when its
// provenance says this version from this revision, conflict otherwise.
func Decide(remote *Remote, err error, digest, version, revision string) (State, string) {
	switch {
	case err != nil:
		return Unreadable, "the registry could not be read: " + err.Error()
	case remote == nil:
		return Absent, ""
	case digest != "" && remote.Digest == digest:
		return Identical, ""
	case revision != "" && remote.Annotations[AnnotationRevision] == revision && remote.Annotations[AnnotationVersion] == version:
		return Adopt, "its provenance says an earlier publish of this version from this revision pushed it"
	}
	why := "the tag holds " + remote.Digest + ", which this publish did not compute"
	var prov []string // only what the manifest says: a bundle carries no provenance
	if v := remote.Annotations[AnnotationVersion]; v != "" {
		prov = append(prov, "version "+v)
	}
	if r := remote.Annotations[AnnotationRevision]; r != "" {
		prov = append(prov, "revision "+r)
	}
	if len(prov) > 0 {
		why += " (" + strings.Join(prov, ", ") + ")"
	}
	return Conflict, why + "; it is never overwritten" + NextVersion
}

// NextVersion is what to do about a tag publish will not write over.
const NextVersion = ": publish a higher service.version"

// MakePlan runs Pacto's checks and decides every item against the registry.
// It writes nothing.
func MakePlan(ctx context.Context, reg Registry, in Input) Plan {
	c := in.Bundle.Contract
	p := Plan{Service: c.Service.Name, Version: c.Service.Version, Revision: in.Revision, Checks: append([]Check{}, in.Checks...)}
	pub := c.Publish
	if pub == nil {
		p.Checks = append(p.Checks, Check{Name: "publish", Message: "the contract declares no publish section (pactoVersion 2.1)"})
		return p
	}
	p.Checks = append(p.Checks, signCheck(pub), refsCheck(pub), sbomCheck(pub), revisionCheck(in.Revision), versionCheck(ctx, reg, in.Bundle, &p))
	for _, a := range pub.Artifacts {
		ref := Repo(a.Ref) + ":" + p.Version
		r, err := reg.Head(ctx, ref)
		st, why := Decide(r, err, "", p.Version, in.Revision)
		p.Items = append(p.Items, item(a.Name, a.Type, ref, st, why, r))
	}
	ref := Repo(pub.Bundle) + ":" + p.Version
	digest, err := oci.BundleDigest(in.Bundle)
	r, headErr := reg.Head(ctx, ref)
	st, why := Decide(r, headErr, digest, p.Version, "")
	if err != nil {
		st, why = Unreadable, "the bundle could not be packed: "+err.Error()
	}
	bundle := item(BundleItem, BundleItem, ref, st, why, r)
	if st == Absent {
		bundle.Digest = digest
	}
	p.Items = append(p.Items, bundle)

	p.Ready = true
	for _, it := range p.Items {
		p.Ready = p.Ready && it.State != Conflict && it.State != Unreadable
		if it.State == Absent {
			p.Missing++
		}
	}
	if in.Preflight != nil {
		p.Checks = append(p.Checks, in.Preflight(p)...)
	}
	for _, ch := range p.Checks {
		p.Ready = p.Ready && ch.Passed
	}
	return p
}

func item(name, typ, ref string, st State, why string, r *Remote) Item {
	it := Item{Name: name, Type: typ, Ref: ref, State: st, Reason: why}
	if r != nil {
		it.Digest = r.Digest
	}
	return it
}

func signCheck(pub *contract.Publish) Check {
	if pub.Sign == nil {
		return Check{Name: "sign", Message: "publish.sign is not configured; everything published is signed, so publish refuses"}
	}
	if s := pub.Sign; s.Key == "" && s.KMS == "" && s.TransparencyLog != nil && !*s.TransparencyLog {
		return Check{Name: "sign", Message: "keyless signing needs the transparency log: a short-lived certificate verifies only at the time the log saw it"}
	}
	return Check{Name: "sign", Passed: true, Message: "signed with Sigstore (" + signMethod(pub.Sign) + ")"}
}

func signMethod(s *contract.PublishSign) string {
	switch {
	case s.Key != "":
		return "key " + s.Key
	case s.KMS != "":
		return "kms " + s.KMS
	}
	return "keyless as " + s.Identity
}

// refsCheck refuses two items in one repository: they share the version tag,
// so the second would find the first's digest there and never be written.
func refsCheck(pub *contract.Publish) Check {
	seen := map[string]string{Repo(pub.Bundle): BundleItem}
	for _, a := range pub.Artifacts {
		repo := Repo(a.Ref)
		if other, ok := seen[repo]; ok {
			return Check{Name: "refs", Message: fmt.Sprintf("%s and %s are both published to %s, where they would share the version tag; give each its own repository", other, a.Name, repo)}
		}
		seen[repo] = a.Name
	}
	return Check{Name: "refs", Passed: true, Message: "every item has its own repository"}
}

// sbomCheck refuses an SBOM on an image the dockerfile builder builds: docker
// buildx writes no SBOM file, so whatever sits at the path would be attached
// and signed as this build's.
func sbomCheck(pub *contract.Publish) Check {
	for _, a := range pub.Artifacts {
		if a.SBOM != "" && a.Build.Dockerfile != nil {
			return Check{Name: "sbom", Message: a.Name + " is built from a Dockerfile, which writes no SBOM file, so " + a.SBOM +
				" would not be this build's; build it with a command that writes the SBOM, or drop sbom"}
		}
	}
	return Check{Name: "sbom", Passed: true, Message: "every SBOM is written by the build of its image"}
}

func revisionCheck(rev string) Check {
	if rev == "" {
		return Check{Name: "revision", Message: "no source revision: run inside a git checkout or pass --revision, so a publish that stops halfway can finish"}
	}
	return Check{Name: "revision", Passed: true, Message: "source revision " + rev}
}

// versionCheck is the version rule: the change from the highest published
// version below this one decides the smallest bump allowed. When that version
// is a release candidate, the change from the highest final release below this
// one must satisfy the rule too: a candidate's fixes are exempt only from the
// bump the candidate line itself already carries.
func versionCheck(ctx context.Context, reg Registry, b *contract.Bundle, p *Plan) Check {
	ch := Check{Name: "version"}
	repo := Repo(b.Contract.Publish.Bundle)
	tags, err := reg.Tags(ctx, repo)
	if err != nil {
		ch.Message = "the published versions could not be listed: " + err.Error()
		return ch
	}
	var bases []string                      // the final release first, then the candidate
	for _, t := range semver.Filter(tags) { // newest first
		if semver.Compare(t, p.Version) >= 0 {
			continue
		}
		if len(bases) == 0 || !prerelease(t) {
			bases = append([]string{t}, bases...)
		}
		if !prerelease(t) {
			break
		}
	}
	if len(bases) == 0 {
		ch.Passed, ch.Message = true, "no earlier version is published, so any version is accepted"
		return ch
	}
	for _, base := range bases {
		if ch = bumpCheck(ctx, reg, b, p, repo, base); !ch.Passed {
			return ch
		}
	}
	return ch
}

// bumpCheck is the version rule against one published version, which it
// records as the plan's baseline.
func bumpCheck(ctx context.Context, reg Registry, b *contract.Bundle, p *Plan, repo, baseline string) Check {
	ch := Check{Name: "version"}
	p.Baseline = baseline
	base, err := reg.Pull(ctx, repo+":"+baseline)
	if err != nil {
		p.Classification, p.RequiredBump = "", "" // no comparison with this baseline was made
		ch.Message = "the published " + baseline + " could not be read: " + err.Error()
		return ch
	}
	d := diff.Compare(ctx, base.Contract, b.Contract, base.FS, b.FS)
	p.Classification, p.RequiredBump = d.Classification.String(), diff.RequiredBump(d.Classification, baseline)
	if err := diff.CheckBump(baseline, p.Version, p.RequiredBump); err != nil {
		ch.Message = d.Classification.String() + " since " + baseline + ": " + err.Error()
		return ch
	}
	ch.Passed, ch.Message = true, fmt.Sprintf("%s since %s needs a %s bump", d.Classification, baseline, p.RequiredBump)
	if d.Classification == diff.PotentialBreaking {
		ch.Message += "; Pacto could not tell whether it breaks a consumer, so a minor is enough"
	}
	return ch
}

// VersionRefusal is the version rule's refusal of p, kept as a signed record
// where a supervising person looks: false when the rule did not refuse it
// after comparing with a published baseline.
func VersionRefusal(p Plan, at time.Time) (delivery.PublishRefusal, bool) {
	for _, ch := range p.Checks {
		if ch.Name == "version" && !ch.Passed && p.RequiredBump != "" {
			return delivery.PublishRefusal{Service: p.Service, Version: p.Version, Baseline: p.Baseline, Classification: p.Classification,
				RequiredBump: string(p.RequiredBump), Message: ch.Message, At: at.UTC().Format(time.RFC3339)}, true
		}
	}
	return delivery.PublishRefusal{}, false
}

// prerelease reports whether the semver tag t is a release candidate.
func prerelease(t string) bool {
	v, err := mver.NewVersion(t)
	return err == nil && v.Prerelease() != ""
}

// envName is the environment variable suffix of an artifact name.
func envName(name string) string { return strings.ToUpper(strings.ReplaceAll(name, "-", "_")) }

// contractRef is the digest-pinned contract reference the deploy artifact carries.
func contractRef(bundleRepo, digest string) string { return "oci://" + bundleRepo + "@" + digest }
