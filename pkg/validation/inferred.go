package validation

import (
	"cmp"
	"fmt"
	"io/fs"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/finding"
)

// inferredRecordStage checks the bundle's pacto.inferred.yaml, when it holds
// one: the record is a pactoVersion 2.5 feature, and it must parse and match
// the files it cites, so a stale or hand-edited record never reads as
// authoritative. A record's surface must name a canonical external identity,
// the one service.external names. A valid record's declaration must agree
// with pacto.yaml (declarationStage).
func inferredRecordStage(result *ValidationResult, c *contract.Contract, fsys fs.FS) {
	rec, err := contract.ReadInferredRecord(fsys)
	if rec == nil && err == nil {
		return
	}
	if InferredRecordNeedsRaise(c.PactoVersion) {
		result.AddError(contract.InferredRecordFile, string(finding.CodeNeedsPacto25),
			contract.InferredRecordFile+` needs pactoVersion "2.5"; set pactoVersion: "2.5", which keeps every older field`)
		return
	}
	if err == nil {
		err = contract.VerifyInferredRecord(fsys, rec)
	}
	if err == nil {
		err = surfaceErr(c, rec.Surface)
	}
	if err != nil {
		result.AddError(contract.InferredRecordFile, string(finding.CodeInferredRecordInvalid), err.Error())
		return
	}
	declarationStage(result, c, rec)
}

// InferredRecordNeedsRaise reports whether a contract at version is too old
// to carry an inferred record: before 2.5. An empty version is
// contract.Current().
func InferredRecordNeedsRaise(version string) bool {
	return contract.Older(cmp.Or(version, contract.Current()), "2.5")
}

// surfaceErr is why surface cannot be in a record beside c, or nil: its
// identity is no canonical external identity, or service.external does not
// name it.
func surfaceErr(c *contract.Contract, surface *contract.InferredSurface) error {
	if surface == nil {
		return nil
	}
	if err := identityErr(surface.Identity); err != nil {
		return fmt.Errorf("surface.identity: %w", err)
	}
	if c.Service.External != surface.Identity {
		return fmt.Errorf("surface.identity is %q, so service.external must be %q, has %q", surface.Identity, surface.Identity, c.Service.External)
	}
	return nil
}
