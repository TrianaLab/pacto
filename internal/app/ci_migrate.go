package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// The states of a CIMigration.
const (
	migrationCurrent  = "current"  // written at the current pactoVersion, or none written
	migrationMigrated = "migrated" // raised in memory to the current pactoVersion
	migrationKept     = "kept"     // behind, and published as written: Reason says why
)

// CIMigration is what pacto ci did with one contract's pactoVersion: From is
// the version pacto.yaml declares, To the version it publishes as.
type CIMigration struct {
	From, To, State, Reason string
}

// migrateForCI is b, read from the folder dir below the repository root,
// with a pactoVersion behind the current one raised in memory: only the
// version token of pacto.yaml changes, the raised contract is completed the
// way every local read completes it, and the inferred record names the
// migration. The raised bundle is validated as a whole, record included; a
// raise that adds a finding there or in Upgrade's own check, or that cannot
// be judged, returns b unchanged and says why. An unknown result never
// raises. resolver is the plan's fenced resolver, in and cat the inputs and
// catalog the ci read used. The folder is never written.
//
// ponytail: copyFSWithReplace copies each behind service into memory; an
// overlay like WithInferredRecord if a large repository's plan memory matters.
// ponytail: validates the original again (once in the ci validation stage,
// once in Upgrade, once here); pass the first run's findings in if ci time
// matters.
func (s *Service) migrateForCI(ctx context.Context, b *contract.Bundle, dir, root string, resolver validation.BundleResolver, in contract.InferredInputs, cat *externalcatalog.Catalog) (*contract.Bundle, CIMigration) {
	// RawYAML is pacto.yaml as written: pacto ci applies no overrides.
	up, err := contract.Upgrade(b.RawYAML, contract.Current(), validation.UpgradeCheck(ctx, b.FS, resolver, s.env().now()))
	kept := CIMigration{From: up.From, To: up.From, State: migrationKept}
	switch {
	case err != nil:
		// Upgrade read no version (a block-scalar pactoVersion Parse accepts):
		// the parsed one says whether anything was behind.
		v := b.Contract.PactoVersion
		if _, behind := contract.Behind(v); !behind {
			return b, CIMigration{From: v, To: v, State: migrationCurrent}
		}
		kept.From, kept.To, kept.Reason = v, v, err.Error()
		return b, kept
	case up.Kept != nil:
		kept.Reason = findingAt(up.Kept.Code, up.Kept.Path) + " from " + contract.Current()
		return b, kept
	case up.To == up.From:
		return b, CIMigration{From: up.From, To: up.From, State: migrationCurrent}
	}
	c, _, _ := contract.ParseDocument(up.Raw) // ponytail: Upgrade's check parsed these bytes
	fsys, err := copyFSWithReplace(b.FS, DefaultContractPath, up.Raw)
	nb := &contract.Bundle{Contract: c, RawYAML: up.Raw, FS: fsys}
	if err == nil {
		// Complete restores the inferred service.name the re-parse dropped;
		// completion ran for nothing at load, below the record's version.
		err = completeLocal(nb, dir, dirBase(dir), root, in, cat)
	}
	if err != nil {
		kept.Reason = err.Error() + " after the raise"
		return b, kept
	}
	rec, _ := contract.ReadInferredRecord(nb.FS) // ponytail: completion just wrote it
	rec = rec.WithMigration(contract.InferredMigration{From: up.From, To: up.To, Source: DefaultContractPath, By: "pacto ci " + s.buildVersion()}, in)
	nb.FS = contract.WithInferredRecord(nb.FS, rec.Marshal())
	before := validation.ValidateWithResolver(ctx, b.Contract, b.RawYAML, b.FS, resolver)
	if added := addedFinding(before, validation.ValidateWithResolver(ctx, nb.Contract, nb.RawYAML, nb.FS, resolver)); added != "" {
		kept.Reason = added + " after completion"
		return b, kept
	}
	return nb, CIMigration{From: up.From, To: up.To, State: migrationMigrated}
}

// addedFinding is the first finding of after (errors, then warnings) whose
// severity, code and path occur more often than in before, as "<code> at
// <path>"; "" when there is none.
func addedFinding(before, after validation.ValidationResult) string {
	seen := map[string]int{}
	for _, e := range before.Errors {
		seen[fmt.Sprint(true, e.Code, e.Path)]++
	}
	for _, w := range before.Warnings {
		seen[fmt.Sprint(false, w.Code, w.Path)]++
	}
	type found struct {
		err        bool
		code, path string
	}
	var all []found
	for _, e := range after.Errors {
		all = append(all, found{true, e.Code, e.Path})
	}
	for _, w := range after.Warnings {
		all = append(all, found{false, w.Code, w.Path})
	}
	for _, f := range all {
		k := fmt.Sprint(f.err, f.code, f.path)
		if seen[k] == 0 {
			return findingAt(f.code, f.path)
		}
		seen[k]--
	}
	return ""
}

// findingAt is "<code> at <path>", or the code alone with no path.
func findingAt(code, path string) string {
	return strings.TrimSuffix(code+" at "+path, " at ")
}
