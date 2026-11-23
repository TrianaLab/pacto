package delivery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

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

// maxProposedRungs bounds the rungs one proposal holds.
const maxProposedRungs = 16

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
	return out[:min(len(out), maxProposedRungs)]
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
