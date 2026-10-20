package delivery

import (
	"fmt"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/catalog"
	"github.com/trianalab/pacto/v4/pkg/contract"
)

// ContractAnnotation is the artifact annotation naming the digest-pinned
// contract a deployable artifact was built from.
const ContractAnnotation = "pacto.dev/contract"

// ParseDigest validates an OCI manifest digest as a Pacto ContentID. A tag is
// refused: delivery moves digests only.
func ParseDigest(d string) (catalog.ContentID, error) {
	return catalog.NewContentID(catalog.SchemeOCI, d)
}

// RevisionDigest extracts the manifest digest from a Flux artifact revision
// ("v1@sha256:..." or "sha256:..."). It is the candidate and the applied
// version; status.artifact.digest is the tarball digest and is never used.
func RevisionDigest(revision string) string {
	if i := strings.LastIndex(revision, "@"); i >= 0 {
		revision = revision[i+1:]
	}
	if _, err := ParseDigest(revision); err != nil {
		return ""
	}
	return revision
}

// ContractDigest returns the digest half of a digest-pinned contract reference,
// or "" when the reference is not pinned.
func ContractDigest(ref string) string {
	_, d := contract.SplitOCIDigest(contract.TrimOCIScheme(ref))
	if _, err := ParseDigest(d); err != nil {
		return ""
	}
	return d
}

// WorkloadState is the live workload a contract check must have seen.
type WorkloadState struct {
	UID                string
	Generation         int64
	ObservedGeneration int64
}

// ContractCheck is what the pacto.contract verify gate reads.
type ContractCheck struct {
	// ContractRef is the contract the applied artifact names.
	ContractRef string
	// Applied is true once Flux confirmed the artifact is applied.
	Applied bool
	// Pactos are the Pacto objects for this service in the Stage's namespace.
	Pactos []PactoView
	// Workload looks up the live workload a Pacto evaluated (nil when absent).
	Workload func(kind, name string) *WorkloadState
}

// ContractVerdict maps the Pacto contract check onto a verdict, but only when
// the check provably evaluated THIS version: the evaluated contract digest is
// the contract of the applied artifact, Flux applied it, the workload controller
// observed the current workload generation and Pacto evaluated at least that
// generation of that very object. Anything else is Unknown, so a result for an
// old version is never read as the answer for a new one.
func ContractVerdict(in ContractCheck) Verdict {
	v := Verdict{Gate: GateContract, Phase: "verify", Result: Unknown, Citation: map[string]string{"contract": in.ContractRef}}
	want := ContractDigest(in.ContractRef)
	switch {
	case want == "":
		v.Message = "the applied artifact names no digest-pinned contract"
		return v
	case !in.Applied:
		v.Message = "Flux has not confirmed the version is applied"
		return v
	case len(in.Pactos) == 0:
		v.Message = "no Pacto checks this service in this namespace"
		return v
	}
	worst := Pass
	var msgs []string
	for _, p := range in.Pactos {
		r, msg := pactoVerdict(p, want, in.Workload)
		msgs = append(msgs, p.Metadata.Name+": "+msg)
		worst = worse(worst, r)
	}
	v.Result = worst
	v.Message = strings.Join(msgs, "; ")
	return v
}

func pactoVerdict(p PactoView, want string, workload func(kind, name string) *WorkloadState) (Result, string) {
	e := p.Status.EvaluatedRevision
	if e == nil || e.ContractDigest != want {
		got := ""
		if e != nil {
			got = e.ContractDigest
		}
		return Unknown, fmt.Sprintf("last evaluated contract %s, not %s", short(got), short(want))
	}
	if e.Workload.Name != "" {
		w := workload(e.Workload.Kind, e.Workload.Name)
		switch {
		case w == nil:
			return Unknown, "the evaluated workload " + e.Workload.Kind + "/" + e.Workload.Name + " is gone"
		case w.UID != e.Workload.UID:
			return Unknown, "the workload was replaced after it was evaluated"
		case w.ObservedGeneration != w.Generation:
			return Unknown, fmt.Sprintf("the workload controller has not observed generation %d yet", w.Generation)
		case e.Workload.Generation < w.Generation:
			return Unknown, fmt.Sprintf("Pacto evaluated generation %d, the workload is at %d", e.Workload.Generation, w.Generation)
		}
	}
	return MapContractStatus(p.Status.ContractStatus), p.Status.ContractStatus
}

// MapContractStatus is the closed mapping from Pacto's contract status to a
// verdict. Anything unrecognised is Unknown, never Pass.
func MapContractStatus(s string) Result {
	switch s {
	case "Compliant", "Warning":
		return Pass
	case "NonCompliant", "Invalid":
		return Fail
	default:
		return Unknown
	}
}

// worse orders Fail over Unknown over Pass.
func worse(a, b Result) Result {
	rank := map[Result]int{Pass: 0, Unknown: 1, Fail: 2}
	if rank[b] > rank[a] {
		return b
	}
	return a
}
