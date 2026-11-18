package validation

import (
	"cmp"
	"io/fs"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/finding"
)

// inferredRecordStage checks the bundle's pacto.inferred.yaml, when it holds
// one: the record is a pactoVersion 2.5 feature, and it must parse and match
// the files it cites, so a stale or hand-edited record never reads as
// authoritative.
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
	if err != nil {
		result.AddError(contract.InferredRecordFile, string(finding.CodeInferredRecordInvalid), err.Error())
	}
}

// InferredRecordNeedsRaise reports whether a contract at version is too old
// to carry an inferred record: before 2.5. An empty version is
// contract.Current().
func InferredRecordNeedsRaise(version string) bool {
	return olderThan(cmp.Or(version, contract.Current()), "2.5")
}
