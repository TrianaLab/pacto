package delivery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// The provenance of an installation ladder rung.
const (
	// RungDeclared: the chart value delivery.environments.ladder names it.
	RungDeclared = "declared"
	// RungInferred: Pacto proposes it and no person confirmed it yet.
	RungInferred = "inferred"
)

// EnvironmentsConfigMap is the ConfigMap in the delivery namespace the
// promoter publishes the installation ladder in, under the key view.json.
// Every authenticated user may read it: it names no service.
const EnvironmentsConfigMap = "pacto-delivery-environments"

// MaxLadderRungs bounds the rungs one proposal holds, and the declared and
// kept rungs one ladder record holds.
const MaxLadderRungs = 16

// LadderRung is one rung of the installation ladder: an environment, the rung
// it takes its versions from (empty for a leaf, fed from the registry), where
// it was found and its provenance (RungDeclared, RungInferred or confirmed by
// a person).
type LadderRung struct {
	Name       string `json:"name"`
	Upstream   string `json:"upstream,omitempty"`
	Source     string `json:"source"`
	Provenance string `json:"provenance"`
}

// LadderInput is what an installation ladder proposal is inferred from: the
// cluster's namespaces, the services known to run in it and the environments
// the cluster advertises (AdvertisedEnvironments).
type LadderInput struct {
	Namespaces []string
	Services   []string
	Advertised []AdvertisedEnvironment
}

// LadderProposal is the inferred rungs Pacto proposes to add to the
// installation ladder, sorted by name. Every one is a leaf.
type LadderProposal []LadderRung

// environmentSynonyms canonicalizes an environment name found in a cluster or
// namespace name, so a cluster named eu-prod hosts production.
var environmentSynonyms = map[string]string{"prod": "production", "stage": "staging", "dev": "development"}

func canonicalEnvironment(name string) string {
	if c, ok := environmentSynonyms[name]; ok {
		return c
	}
	return name
}

// ProposeLadder proposes, as leaves, the rungs the installation ladder does
// not hold yet. known are the rungs it holds, declared or confirmed. A rung
// comes from a namespace suffix -<x> shared by namespaces <service>-<x> of at
// least two distinct known services, then from the environments the cluster
// advertises. Names are canonicalized (prod is production), so a synonym of a
// known rung is never proposed, and a name that is not a DNS label is dropped.
// A leaf never feeds another rung, so a proposal cannot change what reaches a
// declared rung. At most 16 rungs are proposed.
func ProposeLadder(known []string, in LadderInput) LadderProposal {
	taken := map[string]bool{}
	for _, k := range known {
		taken[canonicalEnvironment(k)] = true
	}
	found := map[string]string{}
	add := func(name, source string) {
		name = canonicalEnvironment(name)
		if dnsLabel(name) && !taken[name] && found[name] == "" {
			found[name] = source
		}
	}
	suffixes := namespaceSuffixes(in.Namespaces, in.Services)
	for _, x := range slices.Sorted(maps.Keys(suffixes)) {
		if n := len(suffixes[x]); n >= 2 {
			add(x, fmt.Sprintf("namespaces *-%s, %d found", x, n))
		}
	}
	for _, a := range in.Advertised {
		add(a.Name, a.Source)
	}
	out := LadderProposal{}
	for _, name := range slices.Sorted(maps.Keys(found)) {
		out = append(out, LadderRung{Name: name, Source: found[name], Provenance: RungInferred})
	}
	return out[:min(len(out), MaxLadderRungs)]
}

// namespaceSuffixes maps each suffix x of a namespace <service>-<x> to the
// distinct known services it was found with. The longest known service that
// prefixes a namespace is its service.
func namespaceSuffixes(namespaces, services []string) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, ns := range namespaces {
		svc := ""
		for _, s := range services {
			if len(s) > len(svc) && strings.HasPrefix(ns, s+"-") {
				svc = s
			}
		}
		if svc == "" {
			continue
		}
		x := strings.TrimPrefix(ns, svc+"-")
		if out[x] == nil {
			out[x] = map[string]bool{}
		}
		out[x][svc] = true
	}
	return out
}

// ProposalDigest is the sha256 of a proposal's canonical JSON (its rungs
// sorted by name), the digest a person's confirmation binds; empty for an
// empty proposal.
func ProposalDigest(p LadderProposal) string {
	if len(p) == 0 {
		return ""
	}
	sorted := slices.SortedFunc(slices.Values(p), func(a, b LadderRung) int { return strings.Compare(a.Name, b.Name) })
	b, _ := json.Marshal(sorted) // ponytail: strings only, Marshal cannot fail
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// EnvironmentsView is the installation-level view the promoter publishes in
// EnvironmentsConfigMap: the declared and confirmed installation ladder, the
// open proposal and its digest, where ladder records live, and how complete
// the answer is. It names no service and no namespace.
type EnvironmentsView struct {
	Declared       []LadderRung   `json:"declared"`
	Confirmed      []LadderRung   `json:"confirmed,omitempty"`
	Proposal       LadderProposal `json:"proposal"`
	ProposalDigest string         `json:"proposalDigest,omitempty"`
	// Record, ConfirmedBy and ConfirmedAt name the signed ladder record that
	// confirmed the ladder, its signer and its signed time.
	Record      string `json:"record,omitempty"`
	ConfirmedBy string `json:"confirmedBy,omitempty"`
	ConfirmedAt string `json:"confirmedAt,omitempty"`
	// Repository and Stream are where a confirmation is pushed.
	Repository string `json:"repository,omitempty"`
	Stream     string `json:"stream,omitempty"`
	// Notes say why a ladder record was not counted or how the chart value
	// overrides the confirmed ladder.
	Notes []string `json:"notes,omitempty"`
	// Dropped are the rungs the counted ladder record left out: a new
	// confirmation carries them, so a dropped rung is never proposed again.
	Dropped []string `json:"dropped,omitempty"`
	// LadderUnread says why the ladder records were read in part: the
	// confirmed ladder and the dropped rungs may be stale, so no confirmation
	// is signed on top of them.
	LadderUnread string `json:"ladderUnread,omitempty"`
	// Idle says why the promoter places nothing, when it does not.
	Idle                string `json:"idle,omitempty"`
	UnplacedCount       int    `json:"unplacedCount"`
	NamespaceCandidates int    `json:"namespaceCandidates"`
	AsOf                string `json:"asOf"`
	// Completeness is partial when something the view is built from could
	// not be read; CompletenessReason says what.
	Completeness       fleet.Completeness `json:"completeness"`
	CompletenessReason string             `json:"completenessReason,omitempty"`
}

// HostedRungs are the installation ladder rungs this cluster hosts: when its
// name advertises environments (AdvertisedEnvironments over the name alone),
// only the rungs they canonicalize to (eu-prod hosts production); otherwise
// every rung.
func HostedRungs(ladder []LadderRung, advertised []AdvertisedEnvironment) []string {
	named := map[string]bool{}
	for _, a := range advertised {
		named[canonicalEnvironment(a.Name)] = true
	}
	var out []string
	for _, r := range ladder {
		if len(named) == 0 || named[canonicalEnvironment(r.Name)] {
			out = append(out, r.Name)
		}
	}
	return out
}

// The source of a desired environment.
const (
	// DesiredFromContract: the contract's environments[] names it.
	DesiredFromContract = "declared"
	// DesiredFromLadder: a declared installation ladder rung, which the
	// contract does not opt out of.
	DesiredFromLadder = "ladder"
	// DesiredFromConfirmed: an inferred rung a person confirmed.
	DesiredFromConfirmed = "inferred-confirmed"
)

// DesiredEnvironment is one environment a service should be delivered to:
// its name, the environment it takes versions from (empty: the registry),
// its source and, for an expired temporary environment, when it started to
// retire (RFC 3339).
type DesiredEnvironment struct {
	Name       string `json:"name"`
	Upstream   string `json:"upstream,omitempty"`
	Source     string `json:"source"`
	RetireFrom string `json:"retireFrom,omitempty"`
}

// DesiredEnvironments are the environments a service whose contract declares
// envs should be delivered to, as of now. A contract that declares none takes
// the whole installation ladder (declared and confirmed rungs). One that
// declares environments names its exact set: a rung it leaves out is opted
// out, and an entry outside the ladder is a leaf fed from the registry. A
// temporary environment whose expires has passed is retiring from then.
func DesiredEnvironments(ladder []LadderRung, envs []contract.Environment, now time.Time) []DesiredEnvironment {
	if len(envs) == 0 {
		out := make([]DesiredEnvironment, 0, len(ladder))
		for _, r := range ladder {
			out = append(out, desiredOf(r))
		}
		return out
	}
	out := make([]DesiredEnvironment, 0, len(envs))
	for _, e := range envs {
		d := DesiredEnvironment{Name: e.Name, Source: DesiredFromContract}
		if i := slices.IndexFunc(ladder, func(r LadderRung) bool { return r.Name == e.Name }); i >= 0 {
			d.Upstream = ladder[i].Upstream
		}
		d.RetireFrom = retireAt(e, now)
		out = append(out, d)
	}
	return out
}

func desiredOf(r LadderRung) DesiredEnvironment {
	d := DesiredEnvironment{Name: r.Name, Upstream: r.Upstream, Source: DesiredFromLadder}
	if r.Provenance == RungConfirmed {
		d.Source = DesiredFromConfirmed
	}
	return d
}

// retireAt is when e started to retire: its expires, once a temporary
// environment's has passed; "" otherwise.
func retireAt(e contract.Environment, now time.Time) string {
	if t, ok := e.ExpiresAt(); ok && e.Lifetime == contract.EnvironmentTemporary && !t.After(now) {
		return t.UTC().Format(time.RFC3339)
	}
	return ""
}

// ManagedLabel, set to ManagedBy, marks a Stage Pacto created from the
// installation ladder. It is for display, the event split and the admission
// safety net only: what Pacto owns is the UID list in its private state, since
// anyone who may edit a Stage may write this label.
const (
	ManagedLabel = "delivery.pacto.trianalab.io/managed-by"
	ManagedBy    = "pacto"
)

// managedPrefix prefixes the labels and annotations of a managed Stage.
const managedPrefix = "delivery.pacto.trianalab.io/"

// ManagedLabels are the labels of the managed Stage of service in
// environment. The service label holds the name alone: a domain is no label
// value.
func ManagedLabels(service ServiceRef, environment string) map[string]string {
	return map[string]string{ManagedLabel: ManagedBy, managedPrefix + "service": service.Name, managedPrefix + "environment": environment}
}

// PlacementAnnotations show, on a managed Stage, where its placement and its
// environment come from (source is a DesiredEnvironment's). Display only:
// Pacto never reads them back.
func PlacementAnnotations(source string, p Placement) map[string]string {
	return map[string]string{
		managedPrefix + "placement-provenance": p.Provenance,
		managedPrefix + "placement-source":     p.Source,
		managedPrefix + "placement-evidence":   p.Evidence,
		managedPrefix + "environment-source":   source,
	}
}

// StagePlacement reads back, for display, where a Stage's environment and
// placement come from: the environment source and placement Pacto wrote on a
// Stage it labelled ManagedLabel, and who confirmed the placement. managed is
// false for any other Stage, which a person wrote. Ownership never rests on
// it: anyone who may edit a Stage may write these, so they are claims, and a
// provenance or source outside the known values reads unknown (empty for the
// source).
func StagePlacement(meta ObjectMeta) (source string, p Placement, confirmedBy string, managed bool) {
	if meta.Labels[ManagedLabel] != ManagedBy {
		return PlacementFromStage, Placement{Namespace: meta.Namespace, Provenance: PlacementDeclared, Source: PlacementFromStage}, "", false
	}
	a := meta.Annotations
	p = Placement{Namespace: meta.Namespace, Provenance: a[managedPrefix+"placement-provenance"],
		Source: a[managedPrefix+"placement-source"], Evidence: a[managedPrefix+"placement-evidence"]}
	if !slices.Contains([]string{PlacementDeclared, PlacementObserved, PlacementInferred, PlacementDefault, PlacementUnknown}, p.Provenance) {
		p.Provenance = PlacementUnknown
	}
	if source = a[managedPrefix+"environment-source"]; !slices.Contains([]string{DesiredFromContract, DesiredFromLadder, DesiredFromConfirmed}, source) {
		source = ""
	}
	return source, p, a[PlacementConfirmedByAnnotation], true
}

// ManagedStageSpec is the spec of the Stage Pacto creates for service in
// environment, applying through applier, the adopted Argo CD or Flux object
// already running workload (nil: the Deployment named after the service). The
// first environment of the installation ladder (upstream nil) takes versions
// from the registry, a later one from its upstream environment's Stage. A
// guarded applier (labelled ApplyRefLabel for this Stage) starts autonomous
// with every promote held for a person; any other starts at observe, which
// never ships.
func ManagedStageSpec(service ServiceRef, environment string, upstream *StageRef, applier ApplyRef, workload *WorkloadRef, guarded bool) StageSpec {
	s := StageSpec{Service: service, Environment: environment, ApplyRef: applier, Workload: workload, StartLevel: LevelObserve}
	if upstream == nil {
		s.Candidates.Registry = &RegistryCandidates{}
	} else {
		s.Candidates.UpstreamStage = upstream
	}
	if guarded {
		s.StartLevel, s.Approval.Always = LevelAutonomous, true
	}
	return s
}

// The annotations Pacto writes on what it creates from the installation
// ladder. Display only: Pacto never reads them back.
const (
	// PlacementConfirmedByAnnotation names, on a managed Stage, the person
	// whose approval of its first promote confirmed its placement.
	PlacementConfirmedByAnnotation = managedPrefix + "placement-confirmed-by"
	// KeepAnnotation, "true" on a namespace Pacto created, says Pacto never
	// deletes it.
	KeepAnnotation = managedPrefix + "keep"
	// RetiringSinceAnnotation shows, on a managed Stage, when its environment
	// stopped being desired (RFC 3339): Pacto retires it RetireAt then.
	RetiringSinceAnnotation = managedPrefix + "retiring-since"
)

// RetireAt is when an environment that stopped being desired at since, or a
// temporary one that expired then, is retired: since plus the grace
// (delivery.environments.retireAfter).
func RetireAt(since time.Time, grace time.Duration) time.Time {
	return since.Add(grace)
}

// NamespaceCandidatesKey is the key of PlacementsConfigMap that lists, as a
// sorted JSON array, the namespaces Pacto would create once a person allows
// it (pacto delivery ladder confirm --create-namespaces). Only readers of
// that ConfigMap see them: a ladder record carries only their digest.
const NamespaceCandidatesKey = "namespaces.json"

// NamespacesDigest is the sha256 of the sorted namespace names' JSON, the
// digest a ladder record binds when its signer lets Pacto create them; empty
// for none.
func NamespacesDigest(namespaces []string) string {
	if len(namespaces) == 0 {
		return ""
	}
	b, _ := json.Marshal(slices.Sorted(slices.Values(namespaces))) // ponytail: strings only, Marshal cannot fail
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// PlacementQuestion is the sentence a held first promote, or the trust
// question of a new signer, starts with while service's placement p waits
// for a person: approving it confirms the placement.
func PlacementQuestion(service string, p Placement) string {
	q := "deploy " + service + " to namespace " + p.Namespace
	if p.Evidence != "" {
		q += " (" + p.Evidence + ")"
	}
	return q + "?"
}

// FluxApplierInput is what FluxApplierObjects builds the Flux objects of one
// service in one environment from: where it runs, the artifact
// publish.deploy names (its oci:// ref and whether it is a chart) and the
// keyless identity of its accepted publisher.
type FluxApplierInput struct {
	Service, Environment, Namespace string
	URL                             string
	Chart                           bool
	Publisher                       PublisherIdentity
}

// deployerName is the account the Flux objects of service apply as.
func deployerName(service string) string { return service + "-deployer" }

// The Flux kinds Pacto creates for a service no applier runs yet.
const (
	fluxSourceAPI    = "source.toolkit.fluxcd.io/v1"
	fluxKustomizeAPI = "kustomize.toolkit.fluxcd.io/v1"
	fluxHelmAPI      = "helm.toolkit.fluxcd.io/v2"
)

// FluxApplierObjects are the Flux objects Pacto creates, under profile idp,
// for a service no applier runs yet in one environment, all named
// <service>-<environment>: the pointer OCIRepository and the Kustomization
// (manifests) or HelmRelease (chart) that applies it. Each is labelled
// ManagedLabel and ApplyRefLabel and created suspended, and the pointer has
// no ref at all, whatever the placement's provenance: nothing applies until
// the promoter's first approved promote writes a digest and unsuspends them.
// The pointer verifies the accepted publisher's keyless signature; the
// applier runs as deployerName in its own namespace, and a Kustomization
// orphans what it applied when it is deleted.
func FluxApplierObjects(in FluxApplierInput) []map[string]any {
	name := in.Service + "-" + in.Environment
	meta := func() map[string]any {
		labels := map[string]any{ManagedLabel: ManagedBy, managedPrefix + "service": in.Service,
			managedPrefix + "environment": in.Environment, ApplyRefLabel: name}
		return map[string]any{"name": name, "namespace": in.Namespace, "labels": labels}
	}
	pointer := map[string]any{"interval": "1m", "url": "oci://" + strings.TrimPrefix(in.URL, "oci://"), "suspend": true,
		"verify": map[string]any{"provider": "cosign", "matchOIDCIdentity": []any{map[string]any{
			"issuer":  "^" + regexp.QuoteMeta(in.Publisher.Issuer) + "$",
			"subject": "^" + regexp.QuoteMeta(in.Publisher.Subject) + "$"}}}}
	source := map[string]any{"kind": "OCIRepository", "name": name}
	applier := map[string]any{"interval": "10m", "suspend": true, "targetNamespace": in.Namespace,
		"serviceAccountName": deployerName(in.Service)}
	kind, api := "Kustomization", fluxKustomizeAPI
	if in.Chart {
		pointer["layerSelector"] = map[string]any{"mediaType": "application/vnd.cncf.helm.chart.content.v1.tar+gzip", "operation": "copy"}
		applier["chartRef"] = source
		kind, api = "HelmRelease", fluxHelmAPI
	} else {
		applier["sourceRef"], applier["path"], applier["prune"], applier["deletionPolicy"] = source, "./", true, "Orphan"
	}
	return []map[string]any{
		{"apiVersion": fluxSourceAPI, "kind": "OCIRepository", "metadata": meta(), "spec": pointer},
		{"apiVersion": api, "kind": kind, "metadata": meta(), "spec": applier},
	}
}

// DeployerObjects are the account the Flux objects of service apply each
// version as in namespace, and what it may touch: a ServiceAccount
// deployerName and a RoleBinding of it to clusterRole, the chart's
// <fullname>-deployer ClusterRole that holds only namespaced workload kinds.
// A RoleBinding grants them in namespace alone: nothing cluster-wide and
// nothing of Pacto delivery, Flux or RBAC. Without it Flux applies as the
// namespace's default account, which multi-tenancy lockdown leaves with no
// rights, or as the controller, cluster-admin in a default install, so anyone
// who can publish a version could have a ClusterRoleBinding applied with no
// person asked.
func DeployerObjects(service, namespace, clusterRole string) []map[string]any {
	name := deployerName(service)
	meta := map[string]any{"name": name, "namespace": namespace,
		"labels": map[string]any{ManagedLabel: ManagedBy, managedPrefix + "service": service}}
	return []map[string]any{
		{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": meta},
		{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "RoleBinding", "metadata": maps.Clone(meta),
			"roleRef":  map[string]any{"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": clusterRole},
			"subjects": []any{map[string]any{"kind": "ServiceAccount", "name": name, "namespace": namespace}}},
	}
}
