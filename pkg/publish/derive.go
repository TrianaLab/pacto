package publish

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/diff"
	"github.com/trianalab/pacto/v4/pkg/semver"
)

// FirstVersion is the version Pacto derives when the bundle repository holds
// none: a service adopted from a running repository is already in production,
// and below 1.0 a breaking change would ask only for a minor.
const FirstVersion = "1.0.0"

// AnnotationCreated is when an artifact was built: the commit time, so two
// publishes of one commit stamp the same value.
const AnnotationCreated = "org.opencontainers.image.created"

// DeriveInput is what DeriveVersion reads: the bundle repository, the other
// repository the layout could have used (empty for none), the local bundle,
// the comparator plan mode uses and the publisher's signature check.
type DeriveInput struct {
	Repo, AltRepo string
	Local         *contract.Bundle
	// Publish is the local effective publish section: where a baseline
	// published from a derived section, which its bundle does not carry,
	// keeps its deploy artifact.
	Publish   *contract.Publish
	Unchanged func(base, local *contract.Bundle) (bool, string)
	// Verify accepts repo@digest, read under tag, when it counts as a
	// baseline: the publisher identity signed it (or it is a counted
	// registry declaration). See VerifiedBaseline.
	Verify func(ctx context.Context, tag, ref string) error
}

// NotCounted is why a bundle that carries a registry declaration does not
// count as a baseline. It unwraps to ErrSignatureInvalid: the tag is skipped,
// never an unread check.
type NotCounted struct{ Reason string }

func (n NotCounted) Error() string { return n.Reason }

func (NotCounted) Unwrap() error { return ErrSignatureInvalid }

// errNoBaseline is VerifiedBaseline finding no final release that counts.
var errNoBaseline = errors.New("no verifiable baseline")

// Derived is the version Pacto derived and where it came from.
type Derived struct {
	Version string
	// Baseline is the verified version the change is measured from, empty
	// for a first publish; BaselineDigest is its bundle digest.
	Baseline, BaselineDigest string
	// BaselineRevision and BaselineCreated are the commit the baseline was
	// published from and its commit time, when known.
	BaselineRevision, BaselineCreated string
	Classification                    string
	Bump                              diff.Bump
	// Unchanged is true when the local bundle holds what the baseline
	// published: Version is then the baseline's and nothing is written.
	Unchanged bool
	Notes     []string
	// BaselineRecord is the inferred record of the baseline DeriveVersion
	// pulled, nil when it has none or it cannot be read.
	BaselineRecord *contract.InferredRecord
	// skipped is the highest final release above the baseline that the
	// publisher did not sign: never written over, so never derived.
	skipped string
}

// DeriveVersion is the version an undeclared service.version publishes as:
// the newest final release the publisher signed, raised by the bump the
// change since it requires, or FirstVersion when the repository holds no
// version. An unsigned higher tag never decides it, but it is never written
// over either. A registry that cannot be read is an error, never a first
// publish.
func DeriveVersion(ctx context.Context, reg Registry, in DeriveInput) (Derived, error) {
	tags, err := reg.Tags(ctx, in.Repo)
	if err != nil {
		return Derived{}, fmt.Errorf("listing the versions in %s: %w", in.Repo, err)
	}
	if len(semver.Filter(tags)) == 0 {
		return firstVersion(ctx, reg, in, tags)
	}
	d, err := VerifiedBaseline(ctx, reg, in.Repo, tags, in.Verify)
	if err != nil {
		return d, err
	}
	base, err := reg.Pull(ctx, in.Repo+"@"+d.BaselineDigest)
	if err != nil {
		return Derived{}, fmt.Errorf("reading the baseline %s: %w", d.Baseline, err)
	}
	d.BaselineRecord, _ = contract.ReadInferredRecord(base.FS) // an unreadable record cites nothing
	d.BaselineRevision, d.BaselineCreated = baselineRevision(ctx, reg, base, in.Publish, d.Baseline)
	if same, _ := in.Unchanged(base, in.Local); same {
		d.Version, d.Unchanged = d.Baseline, true
		return d, nil
	}
	d.Version, d.Classification, d.Bump = nextVersion(ctx, base, in.Local, d)
	return d, satisfiesBases(ctx, reg, in, tags, d)
}

// nextVersion is the version local publishes as over the baseline base that
// d names: the baseline raised by the bump the change since it requires,
// and above the highest release d skipped. It is the one version rule.
func nextVersion(ctx context.Context, base, local *contract.Bundle, d Derived) (string, string, diff.Bump) {
	class := diff.Compare(ctx, base.Contract, local.Contract, base.FS, local.FS).Classification
	bump := diff.RequiredBump(class, d.Baseline)
	version, _ := diff.Floor(d.Baseline, bump) // the baseline is a semver tag
	if d.skipped != "" && semver.Compare(version, d.skipped) <= 0 {
		version, _ = diff.Floor(d.skipped, diff.BumpPatch)
	}
	return version, class.String(), bump
}

// FollowsBaseline reports whether b, a registry declaration read under tag
// in repo, follows the repository's history: the newest final release
// below tag that verify accepts is the baseline its record names
// (declaration.baseline.bundle) and tag is the version nextVersion derives
// over it; with no final release below tag, tag is first and the record
// names no baseline. why says why it does not follow; a walk or pull that
// could not complete is an error.
func FollowsBaseline(ctx context.Context, reg Registry, repo string, tags []string, tag string, b *contract.Bundle,
	verify func(ctx context.Context, tag, ref string) error, first string) (bool, string, error) {
	named := ""
	if rec, _ := contract.ReadInferredRecord(b.FS); rec != nil && rec.Declaration != nil && rec.Declaration.Baseline != nil {
		named = rec.Declaration.Baseline.Bundle
	}
	below := slices.DeleteFunc(semver.Filter(tags), func(t string) bool { return prerelease(t) || semver.Compare(t, tag) >= 0 })
	if len(below) == 0 {
		switch {
		case named != "":
			return false, "declares baseline " + named + ", and " + repo + " holds no final release below " + tag, nil
		case tag != first:
			return false, "version " + tag + " does not follow the history: a first declaration is " + first, nil
		}
		return true, "", nil
	}
	d, err := VerifiedBaseline(ctx, reg, repo, below, verify)
	switch {
	case errors.Is(err, errNoBaseline):
		return false, "no final release below " + tag + " counts as its baseline", nil
	case err != nil:
		return false, "", err
	case named != repo+"@"+d.BaselineDigest:
		return false, "declares baseline " + cmp.Or(named, "none") + ", and the newest counted release below " + tag + " is " + d.Baseline, nil
	}
	base, err := reg.Pull(ctx, named)
	if err != nil {
		return false, "", fmt.Errorf("reading the baseline %s: %w", d.Baseline, err)
	}
	c := *b.Contract
	c.Service.Version = base.Contract.Service.Version // the version is the outcome, never a change
	if next, _, _ := nextVersion(ctx, base, &contract.Bundle{Contract: &c, RawYAML: b.RawYAML, FS: b.FS}, d); next != tag {
		return false, "version " + tag + " does not follow " + d.Baseline + ": the next version is " + next, nil
	}
	return true, "", nil
}

// firstVersion is FirstVersion, unless the other layout repository holds
// versions: the bundle repository moved, and starting again at 1.0.0 would
// fork the service's history. A repository with only candidates gets its
// candidate checked.
func firstVersion(ctx context.Context, reg Registry, in DeriveInput, tags []string) (Derived, error) {
	if in.AltRepo != "" {
		alt, err := reg.Tags(ctx, in.AltRepo)
		if err != nil {
			return Derived{}, fmt.Errorf("listing the versions in %s: %w", in.AltRepo, err)
		}
		if len(semver.Filter(alt)) > 0 {
			return Derived{}, fmt.Errorf("bundle repository moved: %s holds versions; declare publish.bundle as it", in.AltRepo)
		}
	}
	d := Derived{Version: FirstVersion}
	return d, satisfiesBases(ctx, reg, in, tags, d)
}

// VerifiedBaseline is the newest final release of repo among tags whose
// bundle verify accepts, with a note for each higher one it skipped: one
// with no signature or none that verifies, or a registry declaration that
// does not count (NotCounted, whose reason the note gives). A check that
// could not complete is an error.
func VerifiedBaseline(ctx context.Context, reg Registry, repo string, tags []string, verify func(ctx context.Context, tag, ref string) error) (Derived, error) {
	var d Derived
	for _, t := range semver.Filter(tags) {
		if prerelease(t) {
			continue
		}
		r, err := reg.Head(ctx, repo+":"+t)
		if err != nil {
			return Derived{}, fmt.Errorf("reading %s:%s: %w", repo, t, err)
		}
		if r == nil { // the tag went away since the listing
			continue
		}
		var nc NotCounted
		switch err := verify(ctx, t, repo+"@"+r.Digest); {
		case err == nil:
		case errors.As(err, &nc):
			d.Notes = append(d.Notes, "ignored "+t+": "+nc.Reason)
			d.skipped = cmp.Or(d.skipped, t)
			continue
		case errors.Is(err, ErrNoSignature) || errors.Is(err, ErrSignatureInvalid):
			d.Notes = append(d.Notes, "ignored "+t+": no signature by the publisher identity")
			d.skipped = cmp.Or(d.skipped, t)
			continue
		default: // unread is not unsigned: it decides no version
			return Derived{}, fmt.Errorf("cannot verify %s:%s: %w", repo, t, err)
		}
		d.Baseline, d.BaselineDigest = t, r.Digest
		return d, nil
	}
	return Derived{}, fmt.Errorf("%w in %s: no final release there is signed by the publisher identity or is a counted registry declaration; declare service.version to publish", errNoBaseline, repo)
}

// satisfiesBases runs the version rule against every version versionCheck
// will judge d.Version by, so it never refuses a version Pacto derived: a
// candidate line, or an unsigned tag above the baseline, needs a declared
// version instead.
func satisfiesBases(ctx context.Context, reg Registry, in DeriveInput, tags []string, d Derived) error {
	for _, base := range Bases(tags, d.Version) {
		if base == d.Baseline {
			continue
		}
		p := Plan{Version: d.Version}
		if ch := bumpCheck(ctx, reg, in.Local, &p, in.Repo, base); !ch.Passed {
			what, fix := "candidate "+base, "declare service.version to publish a candidate line"
			if !prerelease(base) {
				what, fix = base, "declare service.version to publish"
			}
			return fmt.Errorf("%s holds %s above %s, and the derived %s does not satisfy it (%s): %s",
				in.Repo, what, cmp.Or(d.Baseline, "no final release"), d.Version, ch.Message, fix)
		}
	}
	return nil
}

// baselineRevision is the commit the baseline was published from and its
// commit time: the annotations of its deploy artifact, else the commit its
// inferred record names. A baseline with no publish section of its own was
// published from a derived one, which local names the same way.
func baselineRevision(ctx context.Context, reg Registry, base *contract.Bundle, local *contract.Publish, version string) (string, string) {
	pub := base.Contract.Publish
	if pub == nil {
		pub = local
	}
	if pub != nil {
		for _, a := range pub.Artifacts {
			if a.Name != pub.Deploy {
				continue
			}
			if r, err := reg.Head(ctx, Repo(a.Ref)+":"+version); err == nil && r != nil && r.Annotations[AnnotationRevision] != "" {
				return r.Annotations[AnnotationRevision], r.Annotations[AnnotationCreated]
			}
		}
	}
	if rec, err := contract.ReadInferredRecord(base.FS); err == nil && rec != nil {
		return rec.Inputs.Commit, ""
	}
	return "", ""
}
