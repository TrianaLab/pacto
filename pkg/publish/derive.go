package publish

import (
	"cmp"
	"context"
	"fmt"

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
	Publish *contract.Publish
	Unchanged     func(base, local *contract.Bundle) (bool, string)
	// Verify accepts repo@digest when the publisher identity signed it.
	Verify func(ctx context.Context, ref string) error
}

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
	d, err := verifiedBaseline(ctx, reg, in, tags)
	if err != nil {
		return d, err
	}
	base, err := reg.Pull(ctx, in.Repo+"@"+d.BaselineDigest)
	if err != nil {
		return Derived{}, fmt.Errorf("reading the baseline %s: %w", d.Baseline, err)
	}
	d.BaselineRevision, d.BaselineCreated = baselineRevision(ctx, reg, base, in.Publish, d.Baseline)
	if same, _ := in.Unchanged(base, in.Local); same {
		d.Version, d.Unchanged = d.Baseline, true
		return d, nil
	}
	class := diff.Compare(ctx, base.Contract, in.Local.Contract, base.FS, in.Local.FS).Classification
	d.Classification, d.Bump = class.String(), diff.RequiredBump(class, d.Baseline)
	d.Version, _ = diff.Floor(d.Baseline, d.Bump) // the baseline is a semver tag
	if d.skipped != "" && semver.Compare(d.Version, d.skipped) <= 0 {
		d.Version, _ = diff.Floor(d.skipped, diff.BumpPatch)
	}
	return d, satisfiesBases(ctx, reg, in, tags, d)
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

// verifiedBaseline is the newest final release whose bundle Verify accepts,
// with a note for each higher one it skipped.
func verifiedBaseline(ctx context.Context, reg Registry, in DeriveInput, tags []string) (Derived, error) {
	var d Derived
	for _, t := range semver.Filter(tags) {
		if prerelease(t) {
			continue
		}
		r, err := reg.Head(ctx, in.Repo+":"+t)
		if err != nil {
			return Derived{}, fmt.Errorf("reading %s:%s: %w", in.Repo, t, err)
		}
		if r == nil { // the tag went away since the listing
			continue
		}
		if in.Verify(ctx, in.Repo+"@"+r.Digest) != nil {
			d.Notes = append(d.Notes, "ignored "+t+": no signature by the publisher identity")
			d.skipped = cmp.Or(d.skipped, t)
			continue
		}
		d.Baseline, d.BaselineDigest = t, r.Digest
		return d, nil
	}
	return Derived{}, fmt.Errorf("no verifiable baseline in %s: no final release there is signed by the publisher identity; declare service.version to publish", in.Repo)
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
