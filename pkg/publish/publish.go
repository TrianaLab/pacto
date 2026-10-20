// Package publish decides and carries out `pacto publish`: what a contract's
// publish section declares, as the version the contract states, never over an
// existing tag. It builds nothing and runs nothing itself: builders, signing
// and SBOMs come through Tools, and the registry through Registry. Every step
// re-reads the registry, so a publish that stopped halfway finishes when run
// again, and running it twice changes nothing the second time.
package publish

import (
	"context"
	"fmt"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
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
	// Adopt: the tag holds what an earlier publish of this version from this
	// revision pushed; its digest is reused.
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
		return Adopt, "an earlier publish of this version from this revision pushed it"
	}
	return Conflict, fmt.Sprintf("the tag holds %s, which this publish did not compute (version %q, revision %q); it is never overwritten",
		remote.Digest, remote.Annotations[AnnotationVersion], remote.Annotations[AnnotationRevision])
}

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
	p.Checks = append(p.Checks, signCheck(pub), revisionCheck(in.Revision), versionCheck(ctx, reg, in.Bundle, &p))
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
	for _, ch := range p.Checks {
		p.Ready = p.Ready && ch.Passed
	}
	for _, it := range p.Items {
		p.Ready = p.Ready && it.State != Conflict && it.State != Unreadable
		if it.State == Absent {
			p.Missing++
		}
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
	return Check{Name: "sign", Passed: true, Message: "signed with cosign (" + signMethod(pub.Sign) + ")"}
}

func signMethod(s *contract.PublishSign) string {
	switch {
	case s.Key != "":
		return "key " + s.Key
	case s.KMS != "":
		return "kms " + s.KMS
	}
	return "keyless"
}

func revisionCheck(rev string) Check {
	if rev == "" {
		return Check{Name: "revision", Message: "no source revision: run inside a git checkout or pass --revision, so a publish that stops halfway can finish"}
	}
	return Check{Name: "revision", Passed: true, Message: "source revision " + rev}
}

// versionCheck is the version rule: the change from the highest published
// version below this one decides the smallest bump allowed.
func versionCheck(ctx context.Context, reg Registry, b *contract.Bundle, p *Plan) Check {
	ch := Check{Name: "version"}
	repo := Repo(b.Contract.Publish.Bundle)
	tags, err := reg.Tags(ctx, repo)
	if err != nil {
		ch.Message = "the published versions could not be listed: " + err.Error()
		return ch
	}
	for _, t := range semver.Filter(tags) { // newest first
		if semver.Compare(t, p.Version) < 0 {
			p.Baseline = t
			break
		}
	}
	if p.Baseline == "" {
		ch.Passed, ch.Message = true, "no earlier version is published, so any version is accepted"
		return ch
	}
	base, err := reg.Pull(ctx, repo+":"+p.Baseline)
	if err != nil {
		ch.Message = "the published " + p.Baseline + " could not be read: " + err.Error()
		return ch
	}
	d := diff.Compare(ctx, base.Contract, b.Contract, base.FS, b.FS)
	p.Classification, p.RequiredBump = d.Classification.String(), diff.RequiredBump(d.Classification, p.Baseline)
	if err := diff.CheckBump(p.Baseline, p.Version, p.RequiredBump); err != nil {
		ch.Message = d.Classification.String() + " since " + p.Baseline + ": " + err.Error()
		return ch
	}
	ch.Passed, ch.Message = true, fmt.Sprintf("%s since %s needs a %s bump", d.Classification, p.Baseline, p.RequiredBump)
	if d.Classification == diff.PotentialBreaking {
		ch.Message += "; Pacto could not tell whether it breaks a consumer, so a minor is enough"
	}
	return ch
}

// envName is the environment variable suffix of an artifact name.
func envName(name string) string { return strings.ToUpper(strings.ReplaceAll(name, "-", "_")) }

// contractRef is the digest-pinned contract reference the deploy artifact carries.
func contractRef(bundleRepo, digest string) string { return "oci://" + bundleRepo + "@" + digest }
