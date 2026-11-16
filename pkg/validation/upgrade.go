package validation

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"slices"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// The reasons a stage skips because the contract declares nothing for it to
// check. Every other skip means a stage could not verify what it checks.
const (
	skipNoConfigRefs   = "no configuration references"
	skipNoEnvironments = "no environments declared"
)

// ErrUnverified is the error of a run that cannot judge a pactoVersion raise:
// it already fails or a stage could not verify what it checks.
var ErrUnverified = errors.New("fix the errors this run reports before Pacto can tell whether raising pactoVersion keeps its meaning")

// meaningStages are the stages runLayers can skip. Every rule that depends on
// pactoVersion lives in them; a run such as `pacto validate` adds stages of
// its own (lock, readiness) that hold none.
var meaningStages = []string{"cross-field", "policy", "configuration-refs", "environments"}

// UpgradeFindings is a validation run's errors and warnings as a
// [contract.UpgradeCheck] returns them. The run cannot tell what a contract
// means, and the error is [ErrUnverified], when it has any error or a meaning
// stage skipped for a reason other than having nothing to check.
func UpgradeFindings(stages []Stage) ([]contract.ValidationError, []contract.ValidationWarning, error) {
	r := Merged(stages)
	if !r.IsValid() {
		return r.Errors, r.Warnings, ErrUnverified
	}
	for _, st := range stages {
		if st.Status == StageSkip && slices.Contains(meaningStages, st.Name) && st.Reason != skipNoConfigRefs && st.Reason != skipNoEnvironments {
			return r.Errors, r.Warnings, ErrUnverified
		}
	}
	return r.Errors, r.Warnings, nil
}

// UpgradeCheck is the standard [contract.UpgradeCheck]: the layers `pacto
// validate` runs, over bundleFS with resolver at now. A document that does not
// parse is one PARSE_ERROR error and [ErrUnverified]: a raise that breaks parsing is
// kept with that finding, and an original that does not parse is never raised.
func UpgradeCheck(ctx context.Context, bundleFS fs.FS, resolver BundleResolver, now time.Time) contract.UpgradeCheck {
	return func(raw []byte) ([]contract.ValidationError, []contract.ValidationWarning, error) {
		c, err := contract.Parse(bytes.NewReader(raw))
		if err != nil {
			return []contract.ValidationError{{Code: "PARSE_ERROR", Message: err.Error()}}, nil, ErrUnverified
		}
		return UpgradeFindings(ValidateStagesWithResolverAt(ctx, c, raw, bundleFS, resolver, now))
	}
}
