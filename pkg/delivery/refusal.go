package delivery

import (
	"context"
	"fmt"
)

// PredicatePublishRefusal is a signed publish refusal: pacto publish refused
// a version under the version rule and says so where a supervising person
// looks. Nothing else is pushed for a refused version.
const PredicatePublishRefusal = "pacto.dev/publish-refusal/v1"

// PublishRefusal is pacto.dev/publish-refusal/v1: one version pacto publish
// refused because the contract change needs a bigger version bump.
type PublishRefusal struct {
	// Service is the service the contract declares.
	Service string `json:"service"`
	// Version is the version publish refused.
	Version string `json:"version"`
	// Baseline is the published version the contract was compared with.
	Baseline string `json:"baseline"`
	// Classification is the pkg/diff classification of the change.
	Classification string `json:"classification"`
	// RequiredBump is the smallest bump the change needs: major, minor or patch.
	RequiredBump string `json:"requiredBump"`
	// Message is the refusal as publish printed it.
	Message string `json:"message"`
	// At is when publish refused it (RFC 3339).
	At string `json:"at"`
}

// RefusalStatement is an in-toto Statement v1 carrying a publish refusal.
// Its subject is the refused contract bundle, by the digest publish computed
// for it; that bundle was never pushed.
type RefusalStatement struct {
	Type          string         `json:"_type"`
	Subject       []Subject      `json:"subject"`
	PredicateType string         `json:"predicateType"`
	Predicate     PublishRefusal `json:"predicate"`
}

// NewRefusalStatement builds the statement of a refusal of the contract
// bundle with digest contract ("sha256:<hex>").
func NewRefusalStatement(contract string, r PublishRefusal) RefusalStatement {
	return RefusalStatement{Type: StatementType, Subject: statement(RecordSubjects{Contract: contract}, RecordPredicate{}).Subject,
		PredicateType: PredicatePublishRefusal, Predicate: r}
}

// SignRefusal signs st and returns the bundle and its id, as SignRecord does.
func SignRefusal(ctx context.Context, s Signer, st RefusalStatement) (bundle []byte, id string, err error) {
	return signStatement(ctx, s, st)
}

// OpenRefusal verifies a refusal bundle with verify and returns its statement
// and id. A bundle that is not one signed pacto.dev/publish-refusal/v1
// statement is an error.
func OpenRefusal(bundle []byte, verify func(bundle []byte) error) (RefusalStatement, string, error) {
	var st RefusalStatement
	payload, err := openStatement(bundle, PredicatePublishRefusal, &st)
	if err != nil {
		return RefusalStatement{}, "", err
	}
	if err := verify(bundle); err != nil {
		return RefusalStatement{}, "", fmt.Errorf("signature: %w", err)
	}
	return st, sha256Hex(payload), nil
}
