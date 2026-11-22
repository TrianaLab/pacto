package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/internal/discover"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// derivedBundle is b pinned at the version Pacto derives for it, with how it
// was derived, or the result of a publish with nothing to write when b holds
// what its baseline published.
func derivedBundle(ctx context.Context, run runner, reg publish.Registry, tools execTools, dir string, b *contract.Bundle, pub *contract.Publish, alt, revision string, in contract.InferredInputs) (*contract.Bundle, *publish.Result, publish.Derived, error) {
	d, err := deriveVersion(ctx, run, reg, tools, dir, b, pub, alt)
	if err != nil {
		return nil, nil, d, err
	}
	repo := publish.Repo(pub.Bundle)
	if d.Unchanged {
		res := unchangedResult(b, repo, revision, d)
		deploy, notes := unchangedDeploy(ctx, reg, tools, pub, d.Version)
		res.Deploy, res.Notes = deploy, append(res.Notes, notes...)
		return nil, res, d, nil
	}
	pinned, err := pinDerived(b, repo, d, in)
	return pinned, nil, d, err
}

// deriveVersion is the version a contract with no service.version publishes
// as, measured from the newest baseline its publisher signed, once the
// checkout is shown to contain that baseline's revision. pub is the
// effective publish section and alt the bundle repository a derived one did
// not pick ("" for a declared one).
func deriveVersion(ctx context.Context, run runner, reg publish.Registry, tools execTools, dir string, b *contract.Bundle, pub *contract.Publish, alt string) (publish.Derived, error) {
	sign := publish.SignOf(pub)
	// The baseline is read only when signed by the publisher, so the
	// signer that names it must load first: no identity, no baseline.
	if _, _, err := tools.signing(ctx, sign); err != nil {
		return publish.Derived{}, fmt.Errorf("deriving service.version needs the publisher identity to verify the baseline: %w", err)
	}
	bv := newBaselineVerifier(ctx, reg, tools, b, pub)
	d, err := publish.DeriveVersion(ctx, reg, publish.DeriveInput{
		Repo: bv.repo, AltRepo: alt, Local: b, Publish: pub, Unchanged: Unchanged, Verify: bv.verify,
	})
	if bv.declared && bv.trust.publicGood {
		d.Notes = append(d.Notes, declarationsPublicGoodNote)
	}
	if err != nil {
		return d, err
	}
	ref, fields := uncarriedFields(b, bv.counted, d)
	switch {
	case len(fields) == 0:
	case !tools.fromCI:
		d.Notes = append(d.Notes, "pacto publish does not carry registry declarations: "+strings.Join(fields, ", ")+" declared in "+ref+
			" are not in this version; run pacto ci --publish or write them in pacto.yaml")
	case d.BaselineDigest != tools.planDigest:
		// The plan carried from another release: publishing would drop
		// what this baseline declares without a word.
		return d, fmt.Errorf("the plan carried registry decisions from %s, the publish measures the version from %s, whose declaration %s holds %s the plan did not carry; run pacto ci --publish again",
			cmp.Or(tools.planTag, "no release"), d.Baseline, ref, strings.Join(fields, ", "))
	}
	note, err := superseded(ctx, run, dir, b.Contract.Service.Name, d)
	if note != "" {
		d.Notes = append(d.Notes, note)
	}
	return d, err
}

// baselineVerifier is the baseline check of one derived version (Decision
// 4): a release counts when the publisher identity signed it or it is a
// counted registry declaration of want. counted is the last release it
// counted as a declaration; declared says a declaration was judged.
type baselineVerifier struct {
	reg      publish.Registry
	tools    execTools
	sign     contract.PublishSign
	trust    *declarationTrust
	repo     string
	want     wantService
	tags     []string
	counted  *registryDeclaration
	declared bool
}

// newBaselineVerifier checks the baselines of b, published with the
// effective publish section pub, under the run's declaration trust.
func newBaselineVerifier(ctx context.Context, reg publish.Registry, tools execTools, b *contract.Bundle, pub *contract.Publish) *baselineVerifier {
	bv := &baselineVerifier{reg: reg, tools: tools, sign: publish.SignOf(pub), trust: tools.decl, repo: publish.Repo(pub.Bundle),
		want: wantService{Name: b.Contract.Service.Name, Domain: b.Contract.Service.Domain}}
	if bv.trust == nil {
		bv.trust = declarationTrustFrom(ctx, nil)
	}
	return bv
}

// planBaselineErr refuses a pacto ci row whose bundle cites a registry
// declaration when the baseline plan mode carried it from (tools.planTag at
// tools.planDigest) is neither signed by the publisher identity nor a counted declaration:
// plan mode reads the newest tag unverified, and a tag pushed without
// either must never choose what CI signs. A row that cites none, or has no
// baseline, passes.
func planBaselineErr(ctx context.Context, reg publish.Registry, tools execTools, b *contract.Bundle, pub *contract.Publish) error {
	tag, digest := tools.planTag, tools.planDigest
	rec, _ := contract.ReadInferredRecord(b.FS) // completion wrote and verified it
	if digest == "" || rec == nil || rec.Inputs.Declaration == nil {
		return nil
	}
	bv := newBaselineVerifier(ctx, reg, tools, b, pub)
	if err := bv.verify(ctx, tag, bv.repo+"@"+digest); err != nil {
		return fmt.Errorf("the plan carried %s from %s:%s, which is neither signed by the publisher identity nor a counted registry declaration: %w",
			rec.Inputs.Declaration.Fields, bv.repo, tag, err)
	}
	return nil
}

// verify accepts repo@digest read under tag when the publisher signed it or
// it counts as a declaration. Neither: the publisher check's error for a
// bundle with no declaration, publish.NotCounted for one with a declaration.
// A check that could not complete stays an error.
func (bv *baselineVerifier) verify(ctx context.Context, tag, ref string) error {
	perr := bv.tools.Verify(ctx, bv.sign, ref)
	if perr == nil || !errors.Is(perr, publish.ErrNoSignature) && !errors.Is(perr, publish.ErrSignatureInvalid) {
		return perr
	}
	if bv.tags == nil {
		tags, err := bv.reg.Tags(ctx, bv.repo)
		if err != nil {
			return fmt.Errorf("listing the versions in %s: %w", bv.repo, err)
		}
		bv.tags = tags
	}
	_, digest, _ := strings.Cut(ref, "@")
	d, why, err := bv.trust.countedDeclaration(ctx, bv.reg, bv.want, bv.repo, bv.tags, tag, digest, bv.verify)
	switch {
	case err != nil:
		return err
	case d != nil:
		bv.declared, bv.counted = true, d
		return nil
	case why == "":
		return perr
	}
	bv.declared = true
	return publish.NotCounted{Reason: why}
}

// uncarriedFields is the registry declaration the baseline of d counts as
// (counted) or cites, and the declared fields b's pacto.yaml does not
// write: a plain pacto publish notes they are not in this version, because
// only pacto ci --publish carries them (Decision 11). No fields when none.
func uncarriedFields(b *contract.Bundle, counted *registryDeclaration, d publish.Derived) (string, []string) {
	var ref string
	var fields []string
	switch {
	case counted != nil && strings.HasSuffix(counted.Ref, "@"+d.BaselineDigest):
		ref = counted.Ref
		for _, c := range declarationCandidates(counted, ownerVerdict{Allowed: true}, time.Time{}) {
			fields = append(fields, c.Field)
		}
	case d.BaselineRecord != nil && d.BaselineRecord.Inputs.Declaration != nil:
		ref, fields = d.BaselineRecord.Inputs.Declaration.Record, strings.Split(d.BaselineRecord.Inputs.Declaration.Fields, ",")
	}
	fields = slices.DeleteFunc(fields, func(f string) bool { return contract.Declares(b.RawYAML, f) })
	slices.Sort(fields)
	return ref, fields
}

// superseded refuses a checkout that does not contain the revision the
// baseline was published from: a newer run already published past it. In a
// shallow clone without that commit, HEAD's commit time is compared with the
// baseline's; with no baseline time, or a baseline naming no revision, a note
// says what could not be checked.
func superseded(ctx context.Context, run runner, dir, svc string, d publish.Derived) (string, error) {
	rev := d.BaselineRevision
	switch {
	case rev == "" && d.Baseline != "":
		return "baseline " + d.Baseline + " names no revision (no deploy artifact annotation, no inferred record): stale-run check not performed", nil
	case rev == "":
		return "", nil // a first publish has nothing to be stale against
	}
	ctx, cancel := context.WithTimeout(ctx, preflightTimeout)
	defer cancel()
	refused := fmt.Errorf("superseded: %s %s was published from %s, which this checkout does not contain", svc, d.Baseline, rev)
	if _, err := run(ctx, dir, nil, "git", "cat-file", "-e", rev+"^{commit}"); err == nil {
		if _, err := run(ctx, dir, nil, "git", "merge-base", "--is-ancestor", rev, "HEAD"); err != nil {
			return "", refused
		}
		return "", nil
	}
	unknown := "baseline revision " + rev + " not in this checkout; fetch more history to check"
	created, err := time.Parse(time.RFC3339, d.BaselineCreated)
	if err != nil {
		return unknown, nil
	}
	out, err := run(ctx, dir, nil, "git", "show", "-s", "--format=%ct", "HEAD")
	sec, perr := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil || perr != nil {
		return unknown, nil
	}
	if time.Unix(sec, 0).Before(created) {
		return "", refused
	}
	return "", nil
}

// unchangedResult is the publish of a folder that holds what its baseline
// published: nothing is written, and the baseline is what stands.
func unchangedResult(b *contract.Bundle, repo, revision string, d publish.Derived) *publish.Result {
	ref := repo + ":" + d.Version
	return &publish.Result{Plan: publish.Plan{Service: b.Contract.Service.Name, Version: d.Version, Revision: revision, Baseline: d.Baseline,
		Checks: []publish.Check{{Name: "version", Passed: true, Message: "unchanged since " + d.Baseline + ": nothing to publish"}},
		Items:  []publish.Item{{Name: publish.BundleItem, Type: publish.BundleItem, Ref: ref, State: publish.Identical, Digest: d.BaselineDigest}},
		Ready:  true},
		Actions: []string{}, Digests: map[string]string{publish.BundleItem: d.BaselineDigest}, Notes: d.Notes}
}

// unchangedDeploy is the deploy artifact version published, repo@digest,
// once its signature verifies, so --propose can still propose it. pub naming
// a deploy artifact that cannot be read or verified gives a note instead.
func unchangedDeploy(ctx context.Context, reg publish.Registry, tools execTools, pub *contract.Publish, version string) (string, []string) {
	for _, a := range pub.Artifacts {
		if a.Name != pub.Deploy {
			continue
		}
		ref := publish.Repo(a.Ref) + ":" + version
		r, err := reg.Head(ctx, ref)
		if err == nil && r == nil {
			err = errors.New("not found")
		}
		if err == nil {
			err = tools.Verify(ctx, publish.SignOf(pub), publish.Repo(a.Ref)+"@"+r.Digest)
		}
		if err != nil {
			return "", []string{"no deploy artifact to propose: " + ref + ": " + err.Error()}
		}
		return publish.Repo(a.Ref) + "@" + r.Digest, nil
	}
	return "", nil
}

// pinDerived is b with the derived version declared in its pacto.yaml and
// recorded in its inferred record, with the baseline it came from in the
// bundle repository repo, so every reader sees the version is Pacto's. in
// are the record inputs when the folder gave no record.
func pinDerived(b *contract.Bundle, repo string, d publish.Derived, in contract.InferredInputs) (*contract.Bundle, error) {
	raw, err := contract.PinServiceVersion(b.RawYAML, d.Version)
	if err != nil {
		return nil, err
	}
	c, _, err := contract.ParseDocument(raw)
	if err == nil {
		err = contract.RequireDeclared(c, raw)
	}
	if err != nil || c.Service.Version != d.Version {
		return nil, fmt.Errorf("pinning service.version %s into pacto.yaml: %v", d.Version, err)
	}
	rec, err := recordOf(b.FS, in)
	if err != nil {
		return nil, err
	}
	rec.Fields = append(rec.Fields, versionField(repo, d))
	fsys, err := copyFSWithReplace(b.FS, DefaultContractPath, raw)
	if err != nil {
		return nil, fmt.Errorf("failed to build the pinned bundle FS: %w", err)
	}
	return &contract.Bundle{Contract: c, RawYAML: raw, FS: contract.WithInferredRecord(fsys, rec.Marshal())}, nil
}

// recordOf is the inferred record in fsys, or an empty one with inputs in
// when the folder gave none.
func recordOf(fsys fs.FS, in contract.InferredInputs) (*contract.InferredRecord, error) {
	rec, err := contract.ReadInferredRecord(fsys)
	if err != nil || rec != nil {
		return rec, err
	}
	in.Discovery = discover.Version
	return &contract.InferredRecord{Format: contract.InferredRecordFormat, Inputs: in}, nil
}

// versionField is the record field of a derived service.version.
func versionField(repo string, d publish.Derived) contract.Inferred {
	f := contract.Inferred{Field: "service.version", Confidence: contract.ConfidenceLikely, Declare: "service:\n  version: " + d.Version}
	if d.Baseline == "" {
		f.Value, f.Source = d.Version+" (no versions in "+repo+")", "registry:"+repo
		return f
	}
	f.Value = fmt.Sprintf("%s (%s + %s, %s)", d.Version, d.Baseline, d.Bump, d.Classification)
	f.Source = "registry:" + repo + "@" + d.BaselineDigest
	return f
}
