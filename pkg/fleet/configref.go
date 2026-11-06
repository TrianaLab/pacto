package fleet

import (
	"context"
	"errors"
	"fmt"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// snapshotResolver resolves configuration refs from the snapshot alone: the
// fleet never fetches. A base is the key of the revision that declared the ref,
// so a configuration that is itself a ref resolves through the refs of the
// revision that declares it, and the cycle chain works across hops.
type snapshotResolver struct {
	snap *FleetSnapshot
	root RevisionKey
}

var _ validation.OriginBundleResolver = snapshotResolver{}

func (r snapshotResolver) RootBase() string { return string(r.root) }

func (r snapshotResolver) ResolveBundle(ctx context.Context, ref string) (*contract.Bundle, error) {
	b, _, err := r.ResolveBundleFrom(ctx, r.RootBase(), ref)
	return b, err
}

// ResolveBundleFrom finds the configuration ref the revision named by base
// declares as ref and returns the bundle of the revision it resolves to, the
// one its relationship edge names, with that revision's key as the next base.
func (r snapshotResolver) ResolveBundleFrom(_ context.Context, base, ref string) (*contract.Bundle, string, error) {
	from := r.snap.Revisions[RevisionKey(base)]
	if from == nil || from.Contract == nil {
		return nil, "", errors.New("the revision that declares it is not in this snapshot")
	}
	for _, d := range from.Contract.ReferenceRefs() {
		if d.Kind != contract.ReferenceKindConfig || d.Ref != ref {
			continue
		}
		locked, note := lockReference(from, d)
		to, reason := resolveRefRevision(r.snap, from, d, locked, note)
		switch {
		case to == nil:
			return nil, "", errors.New(reason)
		case to.docConflict != "":
			return nil, "", errors.New(to.docConflict)
		}
		return to.bundle, string(to.Key), nil
	}
	return nil, "", fmt.Errorf("%s@%s declares no configuration ref %q", from.Service, from.Version, ref)
}

// revalidateConfigRefs validates again each readable revision that declares a
// configuration ref, with the ref resolved from the snapshot in lenient mode,
// so its values are checked against the provider's schema. A ref the snapshot
// cannot place stays unverified: the stage is skipped and never passes.
func revalidateConfigRefs(snap *FleetSnapshot) {
	for _, rev := range snap.Revisions {
		if !declaresConfigRef(rev.Contract) {
			continue
		}
		yml, err := rev.bundle.Raw()
		if err != nil {
			continue
		}
		stages := validation.ValidateDocumentStagesWith(context.Background(), rev.Contract, contract.DecodeDocument(yml), rev.bundle.FS,
			validation.ConfigRefOptions{Resolver: snapshotResolver{snap: snap, root: rev.Key}, Now: snap.GeneratedAt})
		setStages(rev, stages)
	}
}

// declaresConfigRef reports whether c names a configuration schema by ref.
func declaresConfigRef(c *contract.Contract) bool {
	for _, cfg := range c.Configurations {
		if cfg.Ref != "" {
			return true
		}
	}
	return false
}

// setStages records a validation run on rev.
func setStages(rev *ContractRevision, stages []validation.Stage) {
	res := validation.Merged(stages)
	rev.Valid = res.IsValid()
	rev.Validation = res.Findings()
	rev.Stages = StageSummaries(stages)
	rev.Environments = EnvironmentResults(rev.Contract.Environments, stages)
}
