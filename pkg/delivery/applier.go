package delivery

import (
	"cmp"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/workloadmanager"
)

// The states of ApplierStatus.
const (
	// ApplierAdopted: every workload the Stage delivers carries the marks of
	// the Stage's own applier.
	ApplierAdopted = "Adopted"
	// ApplierUnmanaged: there is no workload to check yet (no Pacto for the
	// service in the Stage namespace, or its workload is not created).
	ApplierUnmanaged = "Unmanaged"
	// ApplierConflict: another manager or another object owns a workload.
	ApplierConflict = "Conflict"
	// ApplierUnknown: Pacto cannot tell who manages a workload.
	ApplierUnknown = "Unknown"
)

// ApplierStatus is who manages the workloads a Stage delivers, as the
// promoter observed it, against the Stage's spec.applyRef (declared). Every
// owner and evidence string comes from workloadmanager, which sanitizes what
// the workload's labels and annotations say.
type ApplierStatus struct {
	// Manager is the detected manager. Both Flux kinds read flux; Owner names
	// the kind.
	// +optional
	// +kubebuilder:validation:Enum=argocd;flux;helm;external;unsupported;ambiguous;none
	Manager string `json:"manager,omitempty" enum:"argocd,flux,helm,external,unsupported,ambiguous,none" doc:"The detected manager; both Flux kinds read flux and owner names the kind; external is a tool the installation named in its fingerprints. Empty when no workload could be read"`
	// Owner is the object that manages the workloads ("Application
	// payments/payments").
	// +optional
	Owner string `json:"owner,omitempty" doc:"The object that manages the workloads, such as Application payments/payments"`
	// Evidence is what on the workload names the manager.
	// +optional
	Evidence string `json:"evidence,omitempty" doc:"What on the workload names its manager, such as tracking annotation"`
	// Display is the one line every surface prints for the manager.
	// +optional
	Display string `json:"display,omitempty" doc:"The one line every surface prints for the manager"`
	// Workloads are the workloads checked ("Deployment payments").
	// +optional
	Workloads []string `json:"workloads,omitempty" doc:"The workloads checked, such as Deployment payments"`
	// +kubebuilder:validation:Enum=Adopted;Unmanaged;Conflict;Unknown
	State string `json:"state" enum:"Adopted,Unmanaged,Conflict,Unknown" doc:"Adopted when the Stage's applier manages every workload; Unmanaged when there is no workload yet; Conflict when another manager or object owns one; Unknown when Pacto cannot tell. Only Adopted and Unmanaged let a promote through"`
	// +optional
	Message string `json:"message,omitempty"`
	// Since is when this result was first seen (RFC 3339).
	// +optional
	Since string `json:"since,omitempty" doc:"When this result was first seen (RFC 3339)"`
	// AsOf is the last observation (RFC 3339).
	// +optional
	AsOf string `json:"asOf,omitempty" doc:"The last observation (RFC 3339)"`
	// StaleAt is when asOf turns stale (RFC 3339): twice the promoter's
	// resync after asOf, since it rewrites asOf at least once per resync.
	// +optional
	StaleAt string `json:"staleAt,omitempty" doc:"When asOf turns stale (RFC 3339): twice the promoter's resync after asOf. A later observation means the promoter stopped checking"`
	// Values is the values file or Kustomize overlay the applier selects, as
	// observed on the applyRef object.
	// +optional
	Values string `json:"values,omitempty" doc:"The values file or Kustomize overlay the applier selects, as observed on the applyRef object, such as values-production.yaml or overlays/production; the Mediated condition says whether Pacto accepts it; empty when it selects none"`
	// +kubebuilder:validation:Enum=complete;partial
	Completeness string `json:"completeness" enum:"complete,partial" doc:"partial when a workload or the Pacto list could not be read"`
}

// ApplierTarget is one workload the Stage delivers and its detected manager.
type ApplierTarget struct {
	Workload string
	Manager  workloadmanager.Manager
}

// NoApplierMessage is what a Stage whose applier API the cluster does not
// serve reads, on its applier status and its Ready condition.
const NoApplierMessage = "no applier: install Flux or Argo CD; in profile idp, set idp.flux.install=true on the Pacto release"

// ApplierInput is what AdoptApplier reads: the Stage's namespace, service
// and applyRef, each target, the workloads a Pacto reports that do not exist
// yet, what could not be read, and the Kustomizations the promoter read
// (their sourceRef says which OCIRepository they apply).
type ApplierInput struct {
	Namespace      string
	Service        string
	Ref            ApplyRef
	Targets        []ApplierTarget
	Missing        []string
	Unread         []string
	Kustomizations []KustomizationView
	Now            time.Time
	// NoAPI says the cluster does not serve the applyRef's API: no Flux or
	// Argo CD runs.
	NoAPI bool
	// Values is the values file or overlay the applier selects
	// (SelectedValues), refused or not; empty when it selects none or could
	// not be read.
	Values string
}

// verdict of one target.
type targetVerdict struct {
	state, message string
}

// AdoptApplier compares who manages each target with the Stage's applyRef.
// Agreement rests on positive evidence: every supported applier marks what it
// applies, so a workload without its applier's marks reads Unknown, never
// Adopted. A confirmed contradiction reads Conflict before anything Pacto
// could not see.
func AdoptApplier(in ApplierInput) ApplierStatus {
	now := in.Now.UTC().Format(time.RFC3339)
	st := ApplierStatus{Since: now, AsOf: now, Completeness: "complete"}
	if len(in.Unread) > 0 {
		st.Completeness = "partial"
	}
	if in.NoAPI {
		st.State, st.Manager, st.Message = ApplierUnknown, workloadmanager.None, NoApplierMessage
		return st
	}
	st.Values = in.Values
	if len(in.Targets) == 0 {
		if len(in.Unread) > 0 {
			st.State, st.Message = ApplierUnknown, "Pacto could not read "+strings.Join(in.Unread, "; ")
			return st
		}
		st.Manager, st.Display, st.State = workloadmanager.None, workloadmanager.Describe(workloadmanager.Manager{}), ApplierUnmanaged
		st.Message = "no workload to check yet: no Pacto for service " + in.Service + " in " + in.Namespace + " reports one"
		if len(in.Missing) > 0 {
			st.Message = "no workload to check yet: " + strings.Join(in.Missing, ", ") + ", which a Pacto reports, does not exist yet"
		}
		return st
	}
	pick, verdict := 0, targetVerdict{state: ApplierAdopted}
	for i, t := range in.Targets {
		st.Workloads = append(st.Workloads, t.Workload)
		v := judgeTarget(in, t)
		if rank(v.state) > rank(verdict.state) {
			pick, verdict = i, v
		}
	}
	if verdict.state == ApplierAdopted && len(in.Unread) > 0 {
		verdict = targetVerdict{ApplierUnknown, "Pacto could not read " + strings.Join(in.Unread, "; ")}
	}
	m := in.Targets[pick].Manager
	st.Manager, st.Owner, st.Evidence, st.Display = managerEnum(m.Name), ownerOf(m), m.Evidence, workloadmanager.Describe(m)
	if cmp.Or(m.Name, workloadmanager.None) == workloadmanager.None {
		// Describe(None) reads "unmanaged", which a workload without marks
		// does not prove.
		st.Display = "no manager marks"
	}
	st.State, st.Message = verdict.state, verdict.message
	if st.State == ApplierAdopted {
		st.Message = st.Display + " applies " + strings.Join(st.Workloads, ", ")
	}
	return st
}

// rank orders target verdicts: a contradiction outweighs an unknown, which
// outweighs agreement.
func rank(state string) int {
	return slices.Index([]string{ApplierAdopted, ApplierUnknown, ApplierConflict}, state)
}

func judgeTarget(in ApplierInput, t ApplierTarget) targetVerdict {
	m := t.Manager
	switch m.Name {
	case workloadmanager.None, "":
		return targetVerdict{ApplierUnknown, "the Stage's applier leaves no mark on " + t.Workload + ", so Pacto cannot tell who manages it"}
	case workloadmanager.Unsupported:
		return targetVerdict{ApplierUnknown, workloadmanager.Describe(m) + ", manages " + t.Workload + ": Pacto adopts only Argo CD and Flux"}
	case workloadmanager.Ambiguous:
		return targetVerdict{ApplierUnknown, t.Workload + " carries marks of more than one manager: " + m.Evidence}
	case workloadmanager.External:
		// Gate-only: never Adopted, so the promoter writes no pointer.
		return targetVerdict{ApplierUnknown, "external: " + m.Product + " manages " + t.Workload + " (" + m.Evidence + "): Pacto runs the gates and writes no pointer"}
	case workloadmanager.Helm:
		return conflictWith(in, t)
	}
	if m.OwnerName == "" {
		return targetVerdict{ApplierUnknown, "only " + m.Evidence + " names the manager of " + t.Workload + ", so Pacto cannot tell which " + m.OwnerKind + " applies it"}
	}
	switch m.Name {
	case workloadmanager.ArgoCD:
		return adoptArgo(in, t)
	case workloadmanager.FluxKustomization:
		return adoptFluxKustomization(in, t)
	case workloadmanager.FluxHelmRelease:
		return adoptHelmRelease(in, t)
	}
	return targetVerdict{ApplierUnknown, workloadmanager.Describe(m) + " manages " + t.Workload}
}

func refKind(r ApplyRef) string { return cmp.Or(r.Kind, ApplierFlux) }

func appliesThrough(in ApplierInput) string {
	return refKind(in.Ref) + " " + in.Namespace + "/" + in.Ref.Name
}

// conflictWith is t's manager owning t while the Stage applies through
// another object.
func conflictWith(in ApplierInput, t ApplierTarget) targetVerdict {
	return targetVerdict{ApplierConflict, t.Manager.Product + " " + ownerOf(t.Manager) + " owns " + t.Workload +
		", and the Stage applies through " + appliesThrough(in) + ": Pacto never writes a pointer another manager overwrites"}
}

func adoptArgo(in ApplierInput, t ApplierTarget) targetVerdict {
	m := t.Manager
	switch {
	case refKind(in.Ref) != ApplierArgoCD:
		return conflictWith(in, t)
	case m.OwnerNamespace == "":
		return targetVerdict{ApplierConflict, "Application " + m.OwnerName + " in the Argo CD namespace owns " + t.Workload +
			"; the Stage applies through " + in.Namespace + "/" + in.Ref.Name}
	case m.OwnerNamespace != in.Namespace || m.OwnerName != in.Ref.Name:
		return conflictWith(in, t)
	}
	return targetVerdict{state: ApplierAdopted}
}

func adoptFluxKustomization(in ApplierInput, t ApplierTarget) targetVerdict {
	m := t.Manager
	if refKind(in.Ref) != ApplierFlux {
		return conflictWith(in, t)
	}
	i := slices.IndexFunc(in.Kustomizations, func(k KustomizationView) bool {
		return k.Metadata.Namespace == m.OwnerNamespace && k.Metadata.Name == m.OwnerName && mayAdopt(in, k)
	})
	if i < 0 {
		why := ""
		if len(in.Unread) > 0 {
			why = " (" + strings.Join(in.Unread, "; ") + ")"
		}
		if m.OwnerNamespace != in.Namespace {
			// The workload's labels, which anyone who edits it controls, name
			// a Kustomization in another namespace. One fixed verdict, whether
			// it exists or where it points, so the Stage status never tells a
			// reader of this namespace about an object they cannot read.
			return targetVerdict{ApplierUnknown, "Pacto cannot confirm that Kustomization " + m.OwnerNamespace + "/" + m.OwnerName +
				", which the labels of " + t.Workload + " name, applies from " + appliesThrough(in) +
				": it reads a Kustomization outside " + in.Namespace + " only when it applies from that OCIRepository" + why}
		}
		return targetVerdict{ApplierUnknown, "Pacto could not read Kustomization " + m.OwnerNamespace + "/" + m.OwnerName + ", which manages " + t.Workload + why}
	}
	src := in.Kustomizations[i].Spec.SourceRef
	srcNS := cmp.Or(src.Namespace, m.OwnerNamespace)
	if src.Kind != ApplierFlux || srcNS != in.Namespace || src.Name != in.Ref.Name {
		return targetVerdict{ApplierConflict, "Kustomization " + m.OwnerNamespace + "/" + m.OwnerName + " applies " + t.Workload + " and applies from " +
			src.Kind + " " + srcNS + "/" + src.Name + ", and the Stage applies through " + appliesThrough(in) +
			": Pacto never writes a pointer another manager overwrites"}
	}
	return targetVerdict{state: ApplierAdopted}
}

// mayAdopt reports whether the Stage may read k: k is in the Stage namespace,
// or it applies from the Stage's own OCIRepository, the set Mediated reads.
func mayAdopt(in ApplierInput, k KustomizationView) bool {
	src := k.Spec.SourceRef
	return k.Metadata.Namespace == in.Namespace ||
		src.Kind == ApplierFlux && cmp.Or(src.Namespace, k.Metadata.Namespace) == in.Namespace && src.Name == in.Ref.Name
}

func adoptHelmRelease(in ApplierInput, t ApplierTarget) targetVerdict {
	m := t.Manager
	if refKind(in.Ref) != ApplierHelmRelease || m.OwnerNamespace != in.Namespace || m.OwnerName != in.Ref.Name {
		return conflictWith(in, t)
	}
	return targetVerdict{state: ApplierAdopted}
}

// managerEnum maps a workloadmanager name onto the CRD's manager enum.
func managerEnum(name string) string {
	switch name {
	case workloadmanager.FluxKustomization, workloadmanager.FluxHelmRelease:
		return "flux"
	case workloadmanager.ArgoCD, workloadmanager.Helm, workloadmanager.External, workloadmanager.Unsupported, workloadmanager.Ambiguous, workloadmanager.None:
		return name
	case "":
		return workloadmanager.None
	}
	return workloadmanager.Unsupported
}

// ownerOf names the owning object: kind and, when known, namespace and name.
func ownerOf(m workloadmanager.Manager) string {
	name := m.OwnerName
	if name != "" && m.OwnerNamespace != "" && m.Name != workloadmanager.Helm {
		name = m.OwnerNamespace + "/" + name
	}
	return strings.TrimSpace(m.OwnerKind + " " + name)
}

// ApplierStaleAfter is twice the promoter's default resync, the stale
// threshold of a status that carries no staleAt (one an older promoter or the
// demo wrote).
const ApplierStaleAfter = 2 * time.Minute

// Stale reports whether now is past staleAt, or, without one, longer than
// ApplierStaleAfter after asOf. An unreadable time is not stale: the surfaces
// print it as it is.
func (a *ApplierStatus) Stale(now time.Time) bool {
	if t, err := time.Parse(time.RFC3339, a.StaleAt); err == nil {
		return now.After(t)
	}
	t, err := time.Parse(time.RFC3339, a.AsOf)
	return err == nil && now.Sub(t) > ApplierStaleAfter
}

// SameApplier reports whether a and b say the same thing: every field but
// Since, AsOf and StaleAt.
func SameApplier(a, b *ApplierStatus) bool {
	if a == nil || b == nil {
		return a == b
	}
	x, y := *a, *b
	x.Since, x.AsOf, x.StaleAt, y.Since, y.AsOf, y.StaleAt = "", "", "", "", "", ""
	return reflect.DeepEqual(x, y)
}

// applierGate judges pacto.applier: Pass when the Stage's applier manages the
// workloads (or there are none yet), Fail on a confirmed contradiction and
// Unknown when Pacto cannot tell. A verdict other than Pass is also a plan
// conflict, since nobody can accept a pointer another manager may overwrite.
// It reports whether the promote may go on.
func applierGate(in PlanInput, p *Plan) bool {
	v, ok := applierVerdict(in)
	if !ok {
		return true
	}
	p.Verdicts = append(p.Verdicts, v)
	switch v.Result {
	case Fail:
		p.Conflicts = append(p.Conflicts, "ApplierConflict: "+v.Message)
	case Unknown:
		p.Conflicts = append(p.Conflicts, "ApplierUnknown: "+v.Message)
	}
	return v.Result == Pass
}

// applierVerdict is the pacto.applier verdict; false when there is nothing to
// judge: no status.applier (a Stage an older promoter wrote).
func applierVerdict(in PlanInput) (Verdict, bool) {
	st := in.Status.Applier
	if st == nil {
		return Verdict{}, false
	}
	v := Verdict{Gate: GateApplier, Phase: "pre", Message: st.Message, Citation: map[string]string{
		"declared": refKind(in.Stage.ApplyRef) + " " + in.Stage.ApplyRef.Name,
		"observed": cmp.Or(st.Display, st.Manager), "state": st.State}}
	switch st.State {
	case ApplierAdopted, ApplierUnmanaged:
		v.Result = Pass
	case ApplierConflict:
		v.Result = Fail
	default:
		v.Result = Unknown
	}
	return v, true
}
