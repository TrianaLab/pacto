package delivery

import (
	"cmp"
	"context"
	"fmt"
	"net/url"
	"strings"
)

// PredicatePublishRefusal is a signed publish refusal: pacto publish refused
// a version under the version rule and says so where a supervising person
// looks. Nothing else is pushed for a refused version.
const PredicatePublishRefusal = "delivery.pacto.trianalab.io/publish-refusal/v1"

// PublishRefusal is delivery.pacto.trianalab.io/publish-refusal/v1: one version pacto publish
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
// and id. A bundle that is not one signed delivery.pacto.trianalab.io/publish-refusal/v1
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

// PredicateApplyRefusal is a signed apply refusal: the manifest inspector
// refused a version a Stage was asked to promote, and the recorder says so
// once per (service, Stage, digest) where a supervising person looks.
const PredicateApplyRefusal = "delivery.pacto.trianalab.io/apply-refusal/v1"

// ApplyRefusal is delivery.pacto.trianalab.io/apply-refusal/v1: one version
// the pacto.manifests gate refused on one Stage, before any pointer moved.
type ApplyRefusal struct {
	// Service is the service the Stage delivers.
	Service ServiceRef `json:"service"`
	// Stage and Namespace name the Stage the promote was refused on.
	Stage     string `json:"stage"`
	Namespace string `json:"namespace"`
	// Version is the contract version of Digest, when the plan read it.
	Version string `json:"version,omitempty"`
	// Digest is the refused deploy artifact digest.
	Digest string `json:"digest"`
	// Refused are the inspector's why sentences, at most MaxListedObjects.
	Refused []string `json:"refused"`
	// Message is the verdict's message.
	Message string `json:"message"`
	// Record is the id of the operation record of the refused promote.
	Record string `json:"record"`
	// Operation is the name of the refused Operation.
	Operation string `json:"operation"`
	// At is when the promote was refused (RFC 3339).
	At string `json:"at"`
}

// ApplyRefusalStatement is an in-toto Statement v1 carrying an apply
// refusal. Its subject is the refused deploy artifact.
type ApplyRefusalStatement struct {
	Type          string       `json:"_type"`
	Subject       []Subject    `json:"subject"`
	PredicateType string       `json:"predicateType"`
	Predicate     ApplyRefusal `json:"predicate"`
}

// ApplyRefusalStream is the stream a service's apply refusals are attached
// to: apply-refusals/<domain or ->/<name>, shaped like [PinStream].
func ApplyRefusalStream(s ServiceRef) string {
	return "apply-refusals/" + url.PathEscape(cmp.Or(s.Domain, "-")) + "/" + s.Name
}

// ApplyRefusalOf is the apply refusal op owes, and false when it owes none:
// only a promote to a digest that concluded Rejected with a failed
// pacto.manifests verdict citing what it refused. Record is left to the
// caller, who knows the operation record's id.
func ApplyRefusalOf(svc ServiceRef, op OperationObject) (ApplyRefusal, bool) {
	if op.Spec.Type != TypePromote || op.Status.Outcome != Rejected || op.Spec.Params.To == "" {
		return ApplyRefusal{}, false
	}
	for _, v := range op.Status.Verdicts {
		if v.Gate != GateManifests || v.Result != Fail || v.Citation["refused"] == "" {
			continue
		}
		refused := strings.Split(v.Citation["refused"], "; ")
		r := ApplyRefusal{Service: svc, Stage: op.Spec.Stage, Namespace: op.Metadata.Namespace, Digest: op.Spec.Params.To,
			Refused: refused[:min(len(refused), MaxListedObjects)], Message: v.Message, Operation: op.Metadata.Name, At: op.Status.Times.Finished}
		if op.Status.Plan != nil {
			r.Version = op.Status.Plan.ToVersion
		}
		return r, true
	}
	return ApplyRefusal{}, false
}

// NewApplyRefusalStatement builds the statement of a refusal of the deploy
// artifact with digest ("sha256:<hex>").
func NewApplyRefusalStatement(digest string, r ApplyRefusal) ApplyRefusalStatement {
	return ApplyRefusalStatement{Type: StatementType, Subject: statement(RecordSubjects{To: digest}, RecordPredicate{}).Subject,
		PredicateType: PredicateApplyRefusal, Predicate: r}
}

// SignApplyRefusal signs st and returns the bundle and its id, as SignRecord
// does.
func SignApplyRefusal(ctx context.Context, s Signer, st ApplyRefusalStatement) (bundle []byte, id string, err error) {
	return signStatement(ctx, s, st)
}

// OpenApplyRefusal verifies an apply refusal bundle with verify and returns
// its statement and id. A bundle that is not one signed apply-refusal/v1
// statement is an error.
func OpenApplyRefusal(bundle []byte, verify func(bundle []byte) error) (ApplyRefusalStatement, string, error) {
	var st ApplyRefusalStatement
	payload, err := openStatement(bundle, PredicateApplyRefusal, &st)
	if err != nil {
		return ApplyRefusalStatement{}, "", err
	}
	if err := verify(bundle); err != nil {
		return ApplyRefusalStatement{}, "", fmt.Errorf("signature: %w", err)
	}
	return st, sha256Hex(payload), nil
}
