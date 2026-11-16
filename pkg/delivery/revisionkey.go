package delivery

import (
	"slices"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// HistoryAnswer is a Stage's history with the derived revision key of each
// artifact digest it names, as the CLI and MCP answer it.
type HistoryAnswer struct {
	*History
	// Revisions is keyed by artifact digest.
	Revisions map[string]string `json:"revisions,omitempty" doc:"The fleet revision key of each artifact digest named here, derived from the contract's digest and name, not checked against a fleet"`
}

// HistoryWalk is Histories under the name HistoriesAnswer embeds it by, so
// the answer's Histories still names the Stages' histories.
type HistoryWalk = Histories

// HistoriesAnswer is a walk's histories with the derived revision key of
// each artifact digest they name.
type HistoriesAnswer struct {
	*HistoryWalk
	Revisions map[string]string `json:"revisions,omitempty" doc:"The fleet revision key of each artifact digest named here, derived from the contract's digest and name, not checked against a fleet"`
}

// BlameAnswer is a blame with the derived revision key of its digest.
type BlameAnswer struct {
	*Blame
	Revision string `json:"revision,omitempty" doc:"The fleet revision key of the digest, derived from the contract's digest and name, not checked against a fleet"`
}

// RevisionKeyOf is the fleet revision key of the artifact d describes, as a
// registry fleet keys it: the service the contract's repository scopes (its
// path minus the last segment) and the bundle's name, at the contract's
// digest. It is derived, not checked against a fleet, and "" when the
// contract was not read, is not digest-pinned or names another service than
// st's: a key is never guessed.
func RevisionKeyOf(st StageObject, d Described) fleet.RevisionKey {
	digest := ContractDigest(d.Contract)
	if d.Err != "" || d.Service == "" || digest == "" || d.Service != st.Spec.Service.Name {
		return ""
	}
	return fleet.NewRevisionKey(fleet.NewServiceKeyDomain(contract.OCIDomain(d.Contract), d.Service), digest)
}

// HistoryDigests are the artifact digests h names, once each: what the Stage
// runs, then every line's from, to and pointer. An autonomy change's from and
// to are level names and are left out.
func HistoryDigests(h History) []string {
	var out []string
	add := func(ds ...string) {
		for _, d := range ds {
			if _, err := ParseDigest(d); err == nil && !slices.Contains(out, d) {
				out = append(out, d)
			}
		}
	}
	add(h.Stage.Pointer)
	for _, e := range h.Entries {
		add(e.From, e.To)
		if e.Pointer != nil {
			add(e.Pointer.Before, e.Pointer.After)
		}
	}
	return out
}
