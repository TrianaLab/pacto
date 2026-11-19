package workloadmanager

// Rule values beyond the Manager names: the two label rules and the fallback.
const (
	PartOf   = "part-of"
	Instance = "instance"
	Workload = "workload"
)

// NoGroupingFrom is where the fallback group was read from.
const NoGroupingFrom = "no grouping label or annotation"

const (
	argoInstanceKey = "argocd.argoproj.io/instance"
	partOfLabel     = "app.kubernetes.io/part-of"

	// maxQuoted caps each value an ambiguity sentence or a From quotes, so
	// the sentence stays inside pkg/fleet's bound.
	maxQuoted = 120
)

// Grouping is how the cluster already groups a workload: the rule that
// matched, the owning object and where Pacto read it. Ambiguous is set only
// with Rule == Workload and names the workload's own values, never another
// object.
type Grouping struct{ Rule, Namespace, Name, From, Ambiguous string }

// Group is the group of o, the first level with a claim winning: Argo CD
// Application, Flux Kustomization or HelmRelease, Helm release,
// app.kubernetes.io/part-of, app.kubernetes.io/instance, else the workload
// itself. Two claims of one level naming different owners leave the workload
// ungrouped with the reason; a lower level never decides then. Unlike Detect
// it answers how the cluster groups o, not who applies it, so Argo CD
// tracking over Helm annotations is Argo CD here and ambiguous there.
//
// Helm annotations under a Flux HelmRelease's labels are the same chain; the
// Flux level wins before the Helm one is read.
func Group(o Object) Grouping {
	for _, level := range [][]Grouping{
		argoClaims(o), fluxClaims(o), helmClaims(o),
		labelGroup(o, partOfLabel, PartOf), labelGroup(o, instanceLabel, Instance),
	} {
		if g, ok := pick(level, o); ok {
			return g
		}
	}
	return workloadGroup(o)
}

// argoClaims are the Argo CD claims on o: the tracking id and the
// argocd.argoproj.io/instance label, or, only when neither claims, the
// app.kubernetes.io/instance label under an argocd field manager. A tracking
// id of another object or a malformed one is no claim.
func argoClaims(o Object) []Grouping {
	var out []Grouping
	if m, _ := argoTracking(o); m.Name == ArgoCD && m.OwnerName != "" {
		out = append(out, Grouping{Rule: ArgoCD, Namespace: m.OwnerNamespace, Name: m.OwnerName, From: "annotation " + argoTrackingID})
	}
	if ns, name := argoApp(o.Labels[argoInstanceKey]); name != "" {
		out = append(out, Grouping{Rule: ArgoCD, Namespace: ns, Name: name, From: "label " + argoInstanceKey})
	}
	if len(out) > 0 {
		return out
	}
	if m, ok := argoInstance(o); ok && m.OwnerName != "" {
		out = append(out, Grouping{Rule: ArgoCD, Namespace: m.OwnerNamespace, Name: m.OwnerName,
			From: "label " + instanceLabel + " and field manager " + quoted(sanitize(manager(o, isArgo)))})
	}
	return out
}

func fluxClaims(o Object) []Grouping {
	var out []Grouping
	for _, f := range []struct {
		fp   fingerprint
		from string
	}{
		{fluxKustomization, "labels " + kustomizeName + " and namespace"},
		{fluxHelmRelease, "labels " + helmReleaseName + " and namespace"},
	} {
		if m, ok := f.fp(o); ok && m.OwnerName != "" {
			out = append(out, Grouping{Rule: m.Name, Namespace: m.OwnerNamespace, Name: m.OwnerName, From: f.from})
		}
	}
	return out
}

func helmClaims(o Object) []Grouping {
	if m, ok := helmRelease(o); ok && m.OwnerName != "" {
		return []Grouping{{Rule: Helm, Namespace: m.OwnerNamespace, Name: m.OwnerName, From: "annotations " + helmName + " and release-namespace"}}
	}
	return nil
}

// labelGroup is the claim of label key on o, in o's namespace.
func labelGroup(o Object, key, rule string) []Grouping {
	if v := o.Labels[key]; v != "" {
		return []Grouping{{Rule: rule, Namespace: o.Namespace, Name: sanitize(v), From: "label " + key}}
	}
	return nil
}

// pick decides one level: no claim reads false; claims naming one owner are
// that owner with every From; two owners leave o ungrouped.
func pick(level []Grouping, o Object) (Grouping, bool) {
	if len(level) == 0 {
		return Grouping{}, false
	}
	g := level[0]
	for _, c := range level[1:] {
		if c.Rule != g.Rule || c.Namespace != g.Namespace || c.Name != g.Name {
			w := workloadGroup(o)
			w.Ambiguous = claim(g) + " and " + claim(c)
			return w, true
		}
		g.From += " and " + c.From
	}
	return g, true
}

// claim words one claim of an ambiguity sentence: where it was read and the
// owner it names.
func claim(g Grouping) string {
	owner := g.Name
	if g.Namespace != "" {
		owner = g.Namespace + "/" + owner
	}
	return g.From + " names " + quoted(owner)
}

// quoted is v cut at maxQuoted runes with a trailing "..."; sanitized values
// are ASCII, so bytes are runes.
func quoted(v string) string {
	if len(v) > maxQuoted {
		return v[:maxQuoted] + "..."
	}
	return v
}

func workloadGroup(o Object) Grouping {
	return Grouping{Rule: Workload, Namespace: o.Namespace, Name: o.Kind + "/" + o.Name, From: NoGroupingFrom}
}
