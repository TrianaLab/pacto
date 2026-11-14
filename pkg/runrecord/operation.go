package runrecord

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// The legal values of an operation record, from the delivery types.
var (
	operationOutcomes = []string{
		string(delivery.Succeeded), string(delivery.Failed), string(delivery.Errored), string(delivery.Cancelled),
		string(delivery.Expired), string(delivery.Rejected), string(delivery.Superseded), string(delivery.Witnessed),
	}
	verifications = []string{string(delivery.Verified), string(delivery.AcceptedUnknown), string(delivery.Unverified)}
	actorClasses  = []string{string(delivery.ClassController), string(delivery.ClassAgent), string(delivery.ClassHuman), string(delivery.ClassBreakGlass)}
	gatePhases    = []string{"pre", "verify"}
	gateResults   = []string{string(delivery.Pass), string(delivery.Fail), string(delivery.Unknown)}
	uidPattern    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	reasonPattern = regexp.MustCompile(`^[A-Z][A-Za-z]{0,63}$`)
	typePattern   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
)

// OperationInput is a concluded operation as the promoter wrote it.
type OperationInput struct {
	UID, Namespace string
	Spec           delivery.OperationSpec
	Status         delivery.OperationStatus
	// ClusterID is the kube-system namespace UID of the promoter's cluster.
	ClusterID    string
	PactoVersion string
	RecordedAt   time.Time
}

// FromOperation builds the run record of a concluded operation. It copies no
// name, message, note, citation or correlation id, and omits any field it
// cannot trust. It returns false for an operation with no Stage, which has no
// service to record under.
func FromOperation(in OperationInput) (Record, bool) {
	st, p := in.Status, in.Status.Policy
	if p == nil {
		return Record{}, false
	}
	return Record{
		Version: Version, ID: in.UID, Kind: KindOperation, Service: p.Service.Name,
		Outcome: string(st.Outcome), PactoVersion: in.PactoVersion,
		RecordedAt: in.RecordedAt.UTC().Truncate(time.Second), Environment: p.Environment,
		Type: path.Base(in.Spec.Type), Verification: string(st.Verification), Reason: reasonCode(st.Reason),
		ActorClass: string(st.ActorClass), Delegated: in.Spec.OnBehalfOf != nil, HeldFor: heldFor(st.Times), ServiceDomain: p.Service.Domain,
		Stage: &StageRef{Namespace: in.Namespace, Name: in.Spec.Stage, Cluster: in.ClusterID},
		To:    pointerDigest(in.Spec, in.Spec.Params.To), From: pointerDigest(in.Spec, in.Spec.Params.From),
		Contract: contractDigest(st), Gates: gateResultsOf(st.Verdicts),
		StartedAt: st.Times.Admitted, FinishedAt: st.Times.Finished,
	}, true
}

// pointerDigest returns v when the operation's type moves a pointer and v is a
// digest. It reads the type's facet, never a type id.
func pointerDigest(spec delivery.OperationSpec, v string) string {
	t, ok := delivery.TypeByID(spec.Type)
	if !ok || t.Facet != delivery.FacetPointer || !digestPattern.MatchString(v) {
		return ""
	}
	return v
}

// reasonCode returns s only when it is a plain code: a reason can embed an
// approver's name and note.
func reasonCode(s string) string {
	if !reasonPattern.MatchString(s) {
		return ""
	}
	return s
}

func gateResultsOf(vs []delivery.Verdict) []GateResult {
	var out []GateResult
	for _, v := range vs {
		out = append(out, GateResult{Gate: v.Gate, Phase: v.Phase, Result: string(v.Result)})
	}
	return out
}

// contractDigest is the contract the first citing verdict names, in the
// operation's verdicts and then its plan's.
func contractDigest(st delivery.OperationStatus) string {
	vs := st.Verdicts
	if st.Plan != nil {
		vs = append(slices.Clone(vs), st.Plan.Verdicts...)
	}
	for _, v := range vs {
		if d := delivery.ContractDigest(v.Citation["contract"]); digestPattern.MatchString(d) {
			return d
		}
	}
	return ""
}

// heldFor is how long the operation waited on a person: from its hold to the
// apply an approval allowed, else to its conclusion.
// ponytail: Times.Held is the last hold, so a twice-asked operation reports its last wait; keep a running total in status if that matters.
func heldFor(t delivery.Times) string {
	held, err := time.Parse(time.RFC3339, t.Held)
	if err != nil {
		return ""
	}
	if applied, err := time.Parse(time.RFC3339, t.Applied); err == nil && applied.After(held) {
		return applied.Sub(held).String()
	}
	finished, err := time.Parse(time.RFC3339, t.Finished)
	if err != nil || finished.Before(held) {
		return ""
	}
	return finished.Sub(held).String()
}

func validateOperationEnums(r Record) error {
	switch {
	case !uidPattern.MatchString(r.ID):
		return fmt.Errorf("id %q is not a Kubernetes UID", r.ID)
	case !typePattern.MatchString(r.Type):
		return fmt.Errorf("type %q is not an operation type name", r.Type)
	case !slices.Contains(operationOutcomes, r.Outcome):
		return fmt.Errorf("unknown outcome %q", r.Outcome)
	case r.Verification != "" && !slices.Contains(verifications, r.Verification):
		return fmt.Errorf("unknown verification %q", r.Verification)
	case r.ActorClass != "" && !slices.Contains(actorClasses, r.ActorClass):
		return fmt.Errorf("unknown actorClass %q", r.ActorClass)
	case r.Reason != "" && !reasonPattern.MatchString(r.Reason):
		return errors.New("reason is not a plain code")
	}
	for _, g := range r.Gates {
		if g.Gate == "" || !slices.Contains(gatePhases, g.Phase) || !slices.Contains(gateResults, g.Result) {
			return fmt.Errorf("gates holds an invalid result %+v", g)
		}
	}
	return nil
}

func validateStage(r Record) error {
	s := r.Stage
	if s == nil {
		return errors.New("stage is missing")
	}
	for _, f := range []struct{ name, v string }{{"namespace", s.Namespace}, {"name", s.Name}, {"cluster", s.Cluster}} {
		if f.v == "" {
			return fmt.Errorf("stage.%s is empty", f.name)
		}
	}
	return nil
}

func validateOperationValues(r Record) error {
	for _, f := range []struct{ name, v string }{{"to", r.To}, {"from", r.From}, {"contract", r.Contract}} {
		if f.v != "" && !digestPattern.MatchString(f.v) {
			return fmt.Errorf("%s %q is not sha256:<64 hex>", f.name, f.v)
		}
	}
	if d, err := time.ParseDuration(r.HeldFor); r.HeldFor != "" && (err != nil || d < 0) {
		return fmt.Errorf("heldFor %q is not a non-negative duration", r.HeldFor)
	}
	if _, err := time.Parse(time.RFC3339, r.FinishedAt); err != nil {
		return fmt.Errorf("finishedAt %q is not an RFC 3339 time", r.FinishedAt)
	}
	if _, err := time.Parse(time.RFC3339, r.StartedAt); r.StartedAt != "" && err != nil {
		return fmt.Errorf("startedAt %q is not an RFC 3339 time", r.StartedAt)
	}
	return nil
}

// field is a record field and whether it is set.
type field struct {
	name string
	set  bool
}

// present names the first field of fs that is set.
func present(fs []field) error {
	for _, f := range fs {
		if f.set {
			return fmt.Errorf("%s is not allowed on this kind", f.name)
		}
	}
	return nil
}

func validateNoCIFields(r Record) error {
	return present([]field{
		{"oldRevision", r.OldRevision != ""}, {"newRevision", r.NewRevision != ""},
		{"bundleDigest", r.BundleDigest != ""}, {"deployDigest", r.DeployDigest != ""},
		{"classification", r.Classification != ""}, {"gate", r.Gate != nil}, {"consumers", r.Consumers != nil},
		{"completeness", r.Completeness != ""}, {"asOf", r.AsOf != nil},
		{"repository", r.Repository != ""}, {"ciRunUrl", r.CIRunURL != ""},
	})
}

func validateNoOperationFields(r Record) error {
	return present([]field{
		{"type", r.Type != ""}, {"verification", r.Verification != ""}, {"reason", r.Reason != ""},
		{"actorClass", r.ActorClass != ""}, {"delegated", r.Delegated}, {"heldFor", r.HeldFor != ""}, {"serviceDomain", r.ServiceDomain != ""},
		{"stage", r.Stage != nil}, {"to", r.To != ""}, {"from", r.From != ""}, {"contract", r.Contract != ""},
		{"gates", r.Gates != nil}, {"startedAt", r.StartedAt != ""}, {"finishedAt", r.FinishedAt != ""},
	})
}
