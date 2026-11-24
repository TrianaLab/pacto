package delivery

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/workloadmanager"
)

// The signed placement policy: its own predicate, at the tag PlacementPolicyTag
// of the repository the delivery policy (delivery.publishers.policy) lives
// in, so a promoter that does not know it still reads the publisher policy.
const (
	PredicatePlacement = "delivery.pacto.trianalab.io/placement/v1"
	PlacementPolicyTag = "placement"
)

// PlacementsConfigMap is the ConfigMap in the delivery namespace the promoter
// writes each delivered service's placement rows in, under the key rows.json.
// No reader binding names it: it names services and namespaces.
const PlacementsConfigMap = "pacto-delivery-placements"

// The provenance of a placement: a signed rule or a hand-written Stage
// (declared), an adopted applier already running the service (observed),
// evidence of where it runs (inferred), the namespace named after the
// environment (default), or Pacto cannot tell (unknown, nothing is created).
const (
	PlacementDeclared = "declared"
	PlacementObserved = "observed"
	PlacementInferred = "inferred"
	PlacementDefault  = "default"
	PlacementUnknown  = "unknown"
)

// The sources of a placement.
const (
	PlacementFromStage    = "hand-written"
	PlacementFromPolicy   = "policy"
	PlacementFromPacto    = "pacto"
	PlacementFromInferred = "inferred-from"
	PlacementFromGitOps   = "gitops-source"
	PlacementFromRungName = "environment-name"
)

// PlacementRule is one rule of the signed placement policy: the services
// (globs, as PublisherRule.Services; none is every service) and environments
// (names, or *) it places, and the namespace, a literal or a template of
// {service} and {environment}.
type PlacementRule struct {
	Name         string   `json:"name"`
	Services     []string `json:"services,omitempty"`
	Environments []string `json:"environments"`
	Namespace    string   `json:"namespace"`
	// Clusters are the uids of the members the rule places on; none is
	// every cluster. Only the uid means the same on the hub and the member.
	Clusters []string `json:"clusters,omitempty"`
}

// clusterUIDPattern is the shape of a cluster uid: the kube-system
// namespace uid, a lowercase UUID.
var clusterUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// PlacementPolicy is the signed placement policy (placement/v1): its rules,
// first match wins, and when it was signed.
type PlacementPolicy struct {
	Rules []PlacementRule `json:"rules"`
	// At is when the policy was signed (RFC 3339): a promoter keeps the
	// newest it verified.
	At string `json:"at"`
}

// PlacementStatement is an in-toto Statement v1 carrying a placement policy.
type PlacementStatement struct {
	Type          string          `json:"_type"`
	Subject       []Subject       `json:"subject"`
	PredicateType string          `json:"predicateType"`
	Predicate     PlacementPolicy `json:"predicate"`
}

// OpenPlacementPolicy verifies a placement policy bundle with verify and
// returns the policy. Decoding is strict; a policy with no signed time, or a
// rule with no environment, whose namespace is not a DNS label once
// {service} and {environment} are filled in or that names a cluster by
// anything but its uid, is refused.
func OpenPlacementPolicy(bundle []byte, verify func(bundle []byte) error) (PlacementPolicy, error) {
	var st PlacementStatement
	if _, err := openStatement(bundle, PredicatePlacement, &st); err != nil {
		return PlacementPolicy{}, err
	}
	if err := verify(bundle); err != nil {
		return PlacementPolicy{}, fmt.Errorf("signature: %w", err)
	}
	p := st.Predicate
	if _, err := time.Parse(time.RFC3339, p.At); err != nil {
		return PlacementPolicy{}, errors.New("the placement policy carries no signed time (at, RFC 3339)")
	}
	for i, r := range p.Rules {
		if len(r.Environments) == 0 || !dnsLabel(expandNamespace(r.Namespace, "a", "a")) {
			return PlacementPolicy{}, fmt.Errorf("placement rule %d (%s) needs environments and a namespace that is a DNS label, with only {service} and {environment} filled in", i, r.Name)
		}
		for _, c := range r.Clusters {
			if !clusterUIDPattern.MatchString(c) {
				return PlacementPolicy{}, fmt.Errorf("placement rule %d (%s) names cluster %q: name the member by its uid: a display name can differ between the hub and the member", i, r.Name, c)
			}
		}
	}
	return p, nil
}

func expandNamespace(tmpl, service, environment string) string {
	return strings.NewReplacer("{service}", service, "{environment}", environment).Replace(tmpl)
}

// GitOpsTarget is one GitOps object that deploys a Git repository into a
// namespace: an Argo CD Application (its spec.source.repoURL or any
// spec.sources[].repoURL, and its destination namespace) or a Flux
// Kustomization (its GitRepository's URL and its targetNamespace).
type GitOpsTarget struct {
	Kind, Namespace, Name string
	Repositories          []string
	Destination           string
}

// NamespaceOccupant is a workload or Pacto in a namespace and whom it belongs
// to: the service and team of the Pacto that describes it, both empty when no
// Pacto does (an unknown owner).
type NamespaceOccupant struct {
	Workload, Service, Team string
}

// PlacementInput is what ResolvePlacement places one service in one hosted
// environment (Rung) from.
type PlacementInput struct {
	Service ServiceRef
	// Owner is the team of the service's accepted bundle (service.owner.team).
	Owner       string
	Rung        string
	HostedRungs []string
	// ClusterUID is this cluster's uid: a rule naming other clusters is
	// skipped.
	ClusterUID string
	// Policy is the verified placement policy's rules; nil when none.
	Policy []PlacementRule
	Pactos []PactoView
	Stages []StageObject
	// Managers is the manager of the service's workload in each namespace
	// it was read in; a namespace with no such workload is absent.
	Managers map[string]workloadmanager.Manager
	// Occupants are the workloads and Pactos of each namespace read.
	Occupants map[string][]NamespaceOccupant
	// InferredFrom is the inferredFrom of the accepted bundle's declaration.
	InferredFrom string
	// Repository is the source repository of the service's newest accepted
	// publisher verdict; empty when none names one.
	Repository string
	// BundleRepository is the OCI repository of the service's index entry.
	BundleRepository string
	GitOps           []GitOpsTarget
	// Namespaces are the namespaces that exist.
	Namespaces       map[string]bool
	SystemNamespaces []string
	CreateNamespaces bool
	// Taken maps the namespaces earlier environments of the service were
	// placed in to those environments.
	Taken map[string]string
	// Unread names, by the source of a step (PlacementFromPolicy,
	// PlacementFromPacto, PlacementFromGitOps), why what that step reads
	// could not be read: from that step on, any placement reads unknown,
	// since the unread evidence might have placed the service elsewhere.
	Unread map[string]string
}

// Placement is where a service runs in one environment: the namespace, the
// provenance and source, the evidence, why the namespace was refused (then
// nothing is created) and whether a person must confirm it.
type Placement struct {
	Namespace  string `json:"namespace,omitempty"`
	Provenance string `json:"provenance"`
	Source     string `json:"source,omitempty"`
	Evidence   string `json:"evidence,omitempty"`
	Refused    string `json:"refused,omitempty"`
	Confirm    bool   `json:"confirm,omitempty"`
}

// Placed reports whether p names a namespace Pacto may act on.
func (p Placement) Placed() bool {
	return p.Namespace != "" && p.Refused == "" && p.Provenance != PlacementUnknown
}

// PlacementRow is one delivered service's environment as the promoter
// publishes it privately: its source (declared, ladder or
// inferred-confirmed), its placement, the managed Stage Pacto keeps for it
// (namespace/name), or why it has none.
type PlacementRow struct {
	Service     string     `json:"service"`
	Domain      string     `json:"domain,omitempty"`
	Environment string     `json:"environment"`
	Source      string     `json:"source"`
	Placement   *Placement `json:"placement,omitempty"`
	Stage       string     `json:"stage,omitempty"`
	Reason      string     `json:"reason,omitempty"`
}

// placementStep is one step of the placement order: the placement it gives,
// whether it applies, and a note on a candidate it skipped.
type placementStep func(in PlacementInput) (p Placement, ok bool, note string)

// placementSteps is the placement order, first that applies wins, each with
// the source it places from.
var placementSteps = []struct {
	source string
	step   placementStep
}{{PlacementFromStage, placeByStage}, {PlacementFromPolicy, placeByPolicy}, {PlacementFromPacto, placeByPacto},
	{PlacementFromInferred, placeByInferredFrom}, {PlacementFromGitOps, placeByGitOps}, {PlacementFromRungName, placeByName}}

// ResolvePlacement places in.Service in in.Rung: a hand-written Stage, a rule
// of the verified placement policy, a Pacto of the service with the same
// repository and owner, the workload its contract was inferred from, a GitOps
// source of its verified repository, else the namespace named after the
// environment. Every namespace but a hand-written Stage's passes placementRefusal and
// the manager guard: an adopted applier reads observed, any other manager
// reads unknown, so Pacto never adds a second applier. An inferred or default
// placement needs a person. Once a step whose evidence could not be read
// (in.Unread) is reached, nothing but a hand-written Stage places: the
// placement reads unknown with why.
func ResolvePlacement(in PlacementInput) Placement {
	var notes []string
	blocked := ""
	for _, s := range placementSteps {
		p, ok, note := s.step(in)
		if note != "" {
			notes = append(notes, note)
		}
		if why := in.Unread[s.source]; blocked == "" {
			blocked = why
		}
		switch {
		case !ok:
			continue
		case p.Source == PlacementFromStage || p.Provenance == PlacementUnknown:
			return p
		case blocked != "":
			return Placement{Provenance: PlacementUnknown, Evidence: blocked + ", so Pacto cannot tell where " + in.Service.Name + " runs in " + in.Rung}
		}
		p.Evidence = strings.Join(append([]string{p.Evidence}, notes...), "; ")
		return finishPlacement(in, p)
	}
	return Placement{} // unreachable: placeByName always applies
}

// finishPlacement applies placementRefusal, the manager guard and the missing
// namespace check to p, and says whether a person must confirm it.
func finishPlacement(in PlacementInput, p Placement) Placement {
	if p.Refused = placementRefusal(in, p); p.Refused != "" {
		return p
	}
	switch m := in.Managers[p.Namespace]; m.Name {
	case "", workloadmanager.None:
	case workloadmanager.ArgoCD, workloadmanager.FluxKustomization, workloadmanager.FluxHelmRelease:
		if p.Provenance != PlacementDeclared {
			p.Provenance = PlacementObserved
		}
		p.Evidence += "; applied by " + workloadmanager.Describe(m)
	default:
		p.Provenance, p.Evidence = PlacementUnknown, "owned by "+workloadmanager.Describe(m)+": Pacto does not add another applier"
		return p
	}
	if !in.Namespaces[p.Namespace] && !in.CreateNamespaces {
		p.Refused = "namespace " + p.Namespace + " does not exist: create it or set delivery.environments.createNamespaces=true"
		return p
	}
	// A Pacto's service and team come from the bundle it references, which
	// anyone may reference from their own namespace: what it shows is evidence
	// a person confirms, even on an adopted applier.
	p.Confirm = p.Provenance == PlacementInferred || p.Provenance == PlacementDefault || p.Source == PlacementFromPacto
	return p
}

// placementRefusal says why p's namespace is refused: a system namespace, one an
// earlier environment of the service took, or one holding a workload or Pacto
// of an unknown owner or, unless a signed rule declared it, of another team.
// "" when it is not.
func placementRefusal(in PlacementInput, p Placement) string {
	ns := p.Namespace
	switch {
	case strings.HasPrefix(ns, "kube-") || slices.Contains(in.SystemNamespaces, ns):
		return ns + " is a system namespace"
	case in.Taken[ns] != "":
		return "namespace " + ns + " already holds the " + in.Taken[ns] + " environment of " + in.Service.Name
	}
	for _, o := range in.Occupants[ns] {
		switch {
		case o.Team == "":
			return "namespace " + ns + " holds workloads of an unknown owner"
		case o.Team == in.Owner || p.Provenance == PlacementDeclared:
		default:
			return "namespace " + ns + " belongs to " + o.Service + " (" + o.Team + ")"
		}
	}
	return ""
}

// rungEnvironment reports whether namespace ns is in.Rung's: a Pacto there
// carries the environment label, the namespace is named after the
// environment or ends in -<environment>, or the cluster hosts only that
// environment.
func rungEnvironment(in PlacementInput, ns string) bool {
	if len(in.HostedRungs) == 1 || ns == in.Rung || strings.HasSuffix(ns, "-"+in.Rung) {
		return true
	}
	return slices.ContainsFunc(in.Pactos, func(p PactoView) bool {
		return p.Metadata.Namespace == ns && p.Metadata.Labels[EnvironmentLabel] == in.Rung
	})
}

func unresolved(ns, rung, what string) string {
	return what + " namespace " + ns + " is not tied to " + rung + ": label its Pacto " + EnvironmentLabel + "=" + rung
}

func placeByStage(in PlacementInput) (Placement, bool, string) {
	for _, s := range in.Stages {
		if OfService(s, in.Service.Name, in.Service.Domain) && s.Spec.Environment == in.Rung {
			return Placement{Namespace: s.Metadata.Namespace, Provenance: PlacementDeclared, Source: PlacementFromStage,
				Evidence: "Stage " + s.Metadata.Namespace + "/" + s.Metadata.Name}, true, ""
		}
	}
	return Placement{}, false, ""
}

func placeByPolicy(in PlacementInput) (Placement, bool, string) {
	for _, r := range in.Policy {
		if (len(r.Clusters) == 0 || slices.Contains(r.Clusters, in.ClusterUID)) && servesService(r.Services, in.Service.Name) && (slices.Contains(r.Environments, in.Rung) || slices.Contains(r.Environments, "*")) {
			return Placement{Namespace: expandNamespace(r.Namespace, in.Service.Name, in.Rung), Provenance: PlacementDeclared,
				Source: PlacementFromPolicy, Evidence: "placement rule " + r.Name}, true, ""
		}
	}
	return Placement{}, false, ""
}

// placeByPacto places the service where a Pacto of it runs in this
// environment, when the Pacto's contract is in the service's own repository
// and owned by its team: a squatting Pacto that points at the service's
// bundle from another repository or team never counts. Two such namespaces
// read unknown.
func placeByPacto(in PlacementInput) (Placement, bool, string) {
	found := map[string]bool{}
	var notes []string
	for _, p := range in.Pactos {
		c, ns := p.Status.Contract, p.Metadata.Namespace
		if c == nil || c.ServiceName != in.Service.Name || c.Owner == nil || c.Owner.Team != in.Owner {
			continue
		}
		if repo, _, _ := strings.Cut(strings.TrimPrefix(c.ResolvedRef, "oci://"), "@"); repo != in.BundleRepository {
			continue
		}
		if !rungEnvironment(in, ns) {
			notes = append(notes, unresolved(ns, in.Rung, "the Pacto"))
			continue
		}
		found[ns] = true
	}
	switch nss := slices.Sorted(maps.Keys(found)); len(nss) {
	case 0:
		return Placement{}, false, strings.Join(notes, "; ")
	case 1:
		return Placement{Namespace: nss[0], Provenance: PlacementInferred, Source: PlacementFromPacto,
			Evidence: "a Pacto of " + in.Service.Name + " runs in " + nss[0]}, true, ""
	default:
		return Placement{Provenance: PlacementUnknown,
			Evidence: in.Service.Name + " runs in " + fmt.Sprint(len(nss)) + " namespaces of " + in.Rung + ": " + strings.Join(nss, ", ")}, true, ""
	}
}

func placeByInferredFrom(in PlacementInput) (Placement, bool, string) {
	_, ns, kind, name, ok := fleet.ParseK8sCaller(in.InferredFrom)
	switch {
	case !ok:
		return Placement{}, false, ""
	case !rungEnvironment(in, ns):
		return Placement{}, false, unresolved(ns, in.Rung, "the inferred-from")
	}
	return Placement{Namespace: ns, Provenance: PlacementInferred, Source: PlacementFromInferred,
		Evidence: "the published contract was inferred from " + kind + " " + ns + "/" + name}, true, ""
}

// placeByGitOps places the service where a GitOps object deploys its
// verified source repository; it does not apply without one. A repository
// deployed into two namespaces of the environment (a monorepo of several
// services) reads unknown.
func placeByGitOps(in PlacementInput) (Placement, bool, string) {
	if in.Repository == "" {
		return Placement{}, false, ""
	}
	want := repoKey(in.Repository)
	var notes []string
	found := map[string]string{}
	for _, g := range in.GitOps {
		if g.Destination == "" || !slices.ContainsFunc(g.Repositories, func(r string) bool { return repoKey(r) == want }) {
			continue
		}
		if !rungEnvironment(in, g.Destination) {
			notes = append(notes, unresolved(g.Destination, in.Rung, "the GitOps destination"))
			continue
		}
		if found[g.Destination] == "" {
			found[g.Destination] = g.Kind + " " + g.Namespace + "/" + g.Name + " deploys " + want
		}
	}
	switch nss := slices.Sorted(maps.Keys(found)); len(nss) {
	case 0:
		return Placement{}, false, strings.Join(notes, "; ")
	case 1:
		return Placement{Namespace: nss[0], Provenance: PlacementInferred, Source: PlacementFromGitOps, Evidence: found[nss[0]]}, true, ""
	default:
		return Placement{Provenance: PlacementUnknown,
			Evidence: want + " is deployed into " + fmt.Sprint(len(nss)) + " namespaces of " + in.Rung + ": " + strings.Join(nss, ", ")}, true, ""
	}
}

func placeByName(in PlacementInput) (Placement, bool, string) {
	return Placement{Namespace: in.Rung, Provenance: PlacementDefault, Source: PlacementFromRungName,
		Evidence: "each environment is a namespace of the same name"}, true, ""
}

// repoKey is a Git repository URL as host/path, lower-cased, with no scheme,
// user, port-free scp colon or trailing .git, so the URL a GitOps object
// names compares with the one a certificate attests.
func repoKey(u string) string {
	u = strings.ToLower(strings.TrimSpace(u))
	_, rest, scheme := strings.Cut(u, "://")
	if scheme {
		u = rest
	}
	if at := strings.LastIndex(u, "@"); at >= 0 && (strings.IndexByte(u, '/') < 0 || at < strings.IndexByte(u, '/')) {
		u = u[at+1:]
	}
	if !scheme {
		u = strings.Replace(u, ":", "/", 1)
	}
	return strings.TrimSuffix(strings.TrimRight(u, "/"), ".git")
}
