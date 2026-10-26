package delivery

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/catalog"
	"github.com/trianalab/pacto/v4/pkg/contract"
)

// ContractAnnotation is the artifact annotation naming the digest-pinned
// contract a deployable artifact was built from.
const ContractAnnotation = "delivery.pacto.trianalab.io/contract"

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
	// Applied is true once the applier (Flux or Argo CD) confirmed the
	// artifact is applied, and AppliedWhy says why it has not when it has not.
	Applied    bool
	AppliedWhy string
	// Pactos are the Pacto objects for this service in the Stage's namespace.
	Pactos []PactoView
	// Workload looks up the live workload a Pacto evaluated (nil when absent).
	Workload func(kind, name string) *WorkloadState
	// Runs is the workload the Stage says the service runs as
	// (StageSpec.WorkloadOf): a Pacto that evaluated anything else judged a
	// decoy, not the version. Empty checks nothing, for callers with no Stage.
	Runs WorkloadRef
}

// ContractVerdict maps the Pacto contract check onto a verdict, but only when
// the check provably evaluated THIS version: the evaluated contract digest is
// the contract of the applied artifact, the applier applied it, the workload controller
// observed the current workload generation and Pacto evaluated at least that
// generation of that very object. Anything else is Unknown, so a result for an
// old version is never read as the answer for a new one.
func ContractVerdict(in ContractCheck) Verdict {
	return eachPacto(in, GateContract, func(p PactoView) (Result, string) {
		return MapContractStatus(p.Status.ContractStatus), p.Status.ContractStatus
	}, nil)
}

// eachPacto runs judge on every Pacto that provably evaluated the applied
// version and folds the answers into one verdict, worst first. A Pacto that
// did not is Unknown, so a result for an old version never counts.
func eachPacto(in ContractCheck, gate string, judge func(PactoView) (Result, string), cite func(PactoView, map[string]string)) Verdict {
	v := Verdict{Gate: gate, Phase: "verify", Result: Unknown, Citation: map[string]string{"contract": in.ContractRef}}
	want := ContractDigest(in.ContractRef)
	switch {
	case want == "":
		v.Message = "the applied artifact names no digest-pinned contract"
		return v
	case !in.Applied:
		v.Message = "the version is not confirmed applied"
		if in.AppliedWhy != "" {
			v.Message += ": " + in.AppliedWhy
		}
		return v
	case len(in.Pactos) == 0:
		v.Message = "no Pacto checks this service in this namespace"
		return v
	}
	worst := Pass
	var msgs []string
	for _, p := range in.Pactos {
		r, msg := Unknown, staleness(p, want, in.Workload)
		if msg == "" {
			msg = decoy(p.Status.EvaluatedRevision.Workload, in.Runs)
		}
		if msg == "" {
			r, msg = judge(p)
			if cite != nil {
				cite(p, v.Citation)
			}
		}
		msgs = append(msgs, p.Metadata.Name+": "+msg)
		worst = worse(worst, r)
	}
	v.Result = worst
	v.Message = strings.Join(msgs, "; ")
	return v
}

// decoy says why a Pacto that evaluated w judged something other than runs,
// the workload the Stage runs, or "" when it judged that one (or runs is
// empty).
func decoy(w WorkloadView, runs WorkloadRef) string {
	if runs.Name == "" || w.Kind == runs.Kind && w.Name == runs.Name {
		return ""
	}
	got := "no workload"
	if w.Name != "" {
		got = w.Kind + "/" + w.Name
	}
	return "it evaluated " + got + ", not " + runs.Kind + "/" + runs.Name + ", the workload this Stage runs (spec.workload)"
}

// staleness says why a Pacto's result is not about the applied version, or ""
// when it is: the evaluated contract digest is the applied one and the
// evaluated workload generation is current.
func staleness(p PactoView, want string, workload func(kind, name string) *WorkloadState) string {
	e := p.Status.EvaluatedRevision
	if e == nil || e.ContractDigest != want {
		got := ""
		if e != nil {
			got = e.ContractDigest
		}
		return fmt.Sprintf("last evaluated contract %s, not %s", short(got), short(want))
	}
	if e.Workload.Name == "" {
		return ""
	}
	switch w := workload(e.Workload.Kind, e.Workload.Name); {
	case w == nil:
		return "the evaluated workload " + e.Workload.Kind + "/" + e.Workload.Name + " is gone"
	case w.UID != e.Workload.UID:
		return "the workload was replaced after it was evaluated"
	case w.ObservedGeneration != w.Generation:
		return fmt.Sprintf("the workload controller has not observed generation %d yet", w.Generation)
	case e.Workload.Generation < w.Generation:
		return fmt.Sprintf("Pacto evaluated generation %d, the workload is at %d", e.Workload.Generation, w.Generation)
	}
	return ""
}

// claimCodes are the findings that contradict what a version declares about
// its calls and ports. They count as claims only when the node agent's kernel
// evidence backs them: an undeclared call (a Pacto-managed service the
// contract never names) or a declared port nothing listens on.
var claimCodes = []string{"DEPENDENCY_UNDECLARED", "INTERFACE_ABSENT"}

// RuntimeVerdict is the pacto.runtime claim gate: what the running version
// does against what its contract declares, as the node agent saw it, read
// from the Pacto evaluation of exactly this version. A contradiction the
// kernel saw is Fail even when coverage is partial, because it was seen. With
// no contradiction it passes only under complete kernel coverage: anything
// less cannot rule a contradiction out, so it is Unknown.
func RuntimeVerdict(in ContractCheck) Verdict {
	return eachPacto(in, GateRuntime, func(p PactoView) (Result, string) {
		if seen := contradictions(p); len(seen) > 0 {
			return Fail, "calls or listens contrary to its contract: " + strings.Join(seen, "; ")
		}
		switch cov := p.Status.EvaluatedRevision.KernelCoverage; cov {
		case "Complete":
			return Pass, "the node agent saw no call to a Pacto-managed service the contract does not name and no declared port nothing listens on"
		case "":
			return Unknown, "the node agent is not observing this workload (kernel evidence is off)"
		default:
			return Unknown, "kernel coverage is " + cov + ", so a contradiction cannot be ruled out"
		}
	}, func(p PactoView, c map[string]string) {
		c[p.Metadata.Name+".evaluatedContract"] = p.Status.EvaluatedRevision.ContractDigest
		c[p.Metadata.Name+".kernelCoverage"] = p.Status.EvaluatedRevision.KernelCoverage
		c[p.Metadata.Name+".evaluatedAt"] = p.Status.LastReconciledAt
		if seen := contradictions(p); len(seen) > 0 {
			c[p.Metadata.Name+".findings"] = strings.Join(seen, "; ")
		}
	})
}

// RuntimeSoak holds a pacto.runtime Pass as Unknown until the version has run
// for the soak param (a Go duration, default 1m). Complete coverage with
// nothing seen right after a rollout means nothing has happened yet, not
// that nothing is wrong. A Fail stands at once: it was seen.
func RuntimeSoak(v Verdict, in GateInput, params map[string]string) Verdict {
	soak := time.Minute
	if s, ok := params["soak"]; ok {
		d, err := time.ParseDuration(s)
		if err != nil || d < 0 {
			return Verdict{Gate: v.Gate, Phase: v.Phase, Result: Unknown, Message: fmt.Sprintf("soak %q is not a duration of 0 or more", s)}
		}
		soak = d
	}
	v.Citation["soak"] = soak.String()
	if ran := in.Window.To.Sub(in.Window.From); v.Result == Pass && ran < soak {
		v.Result = Unknown
		v.Message = fmt.Sprintf("soaking: judged after %s, %s so far (%s)", soak, ran.Truncate(time.Second), v.Message)
	}
	return v
}

// contradictions lists the claim findings the kernel backs, with the evidence.
func contradictions(p PactoView) []string {
	var out []string
	for _, f := range p.Status.Findings {
		if !slices.Contains(claimCodes, f.Code) {
			continue
		}
		for _, e := range f.EvidenceRefs {
			if e.Collector == "ebpf" || strings.HasSuffix(e.Collector, "+ebpf") {
				out = append(out, fmt.Sprintf("%s %s (%s at %s)", f.Code, f.Subject, e.Collector, e.DetectedAt))
				break
			}
		}
	}
	return out
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
