package contract

import (
	"errors"
	"slices"
	"sort"
	"strings"
)

// Owner is the structured, provider-neutral ownership block for a service.
type Owner struct {
	Team     string         `yaml:"team,omitempty" json:"team,omitempty"`
	DRI      string         `yaml:"dri,omitempty" json:"dri,omitempty"`
	Contacts []OwnerContact `yaml:"contacts,omitempty" json:"contacts,omitempty"`
}

// OwnerContact is a single ownership or escalation contact point.
type OwnerContact struct {
	Type    string `yaml:"type" json:"type"`
	Value   string `yaml:"value" json:"value"`
	Purpose string `yaml:"purpose,omitempty" json:"purpose,omitempty"`
}

// OwnerKind is the namespace a canonical owner identity lives in. A team named
// `alice` and a DRI named `alice` are two owners who happen to be spelled alike,
// so the namespace is part of the identity and never dropped from it.
type OwnerKind string

// The namespaces the contract defines. There are exactly two, because the schema
// declares exactly two identifying fields.
const (
	OwnerKindTeam OwnerKind = "team"
	OwnerKindDRI  OwnerKind = "dri"
)

// ownerKeySep separates the namespace from the value in the wire encoding. It is
// the FIRST occurrence that separates, so the value may contain it too.
const ownerKeySep = ":"

// ErrInvalidOwnerKey is returned by [ParseOwnerKey] for anything that is not a
// canonical owner key. It is deliberately unforgiving: a raw owner name is
// ambiguous between the two namespaces, and guessing one — or matching both —
// would hand a reader somebody else's estate.
var ErrInvalidOwnerKey = errors.New("not a canonical owner key")

// OwnerKey is the canonical identity of an owner: the namespace the name was
// declared in, plus the name. It is what the product routes, groups, ranks and
// links by, and it is NOT a label — [Owner.DisplayString] is the label, and two
// different OwnerKeys may share one.
//
// The zero OwnerKey means "no canonical identity". An owner that declares only
// contacts has one: ownership is declared, but there is nothing to navigate to,
// and inventing an identity out of an email address or a chat channel would put
// a name on the fleet that nobody chose.
type OwnerKey struct {
	Kind  OwnerKind
	Value string
}

// Key returns the canonical identity of this owner. Team wins over DRI, matching
// how a service is spoken about; the second return reports whether there is one
// at all.
//
// A structured owner is one canonical owner however its revisions spell the rest
// of the block: `{team: platform, dri: alice}` and `{team: platform, dri: bob}`
// are both `team:platform`, one page, one ranking row, no dispute.
func (o Owner) Key() (OwnerKey, bool) {
	switch {
	case o.Team != "":
		return OwnerKey{Kind: OwnerKindTeam, Value: o.Team}, true
	case o.DRI != "":
		return OwnerKey{Kind: OwnerKindDRI, Value: o.DRI}, true
	}
	return OwnerKey{}, false
}

// KeyString returns the wire form of this owner's canonical identity, or "" when
// it has none. It is the cheap form of [Owner.Key] for comparison and map keys.
func (o Owner) KeyString() string {
	k, ok := o.Key()
	if !ok {
		return ""
	}
	return k.String()
}

// IsZero reports whether this is the "no canonical identity" key.
func (k OwnerKey) IsZero() bool { return k.Kind == "" || k.Value == "" }

// String is the wire and URL form: `<kind>:<value>`.
//
// The encoding is injective over every value the Owner schema permits, and
// [ParseOwnerKey] round-trips it. Only the FIRST separator delimits, so the two
// namespace prefixes are the only fixed part: a team literally named
// `dri:alice` encodes to `team:dri:alice` and decodes back to a team, and a
// team named `team/payments` needs no escaping at all. The zero key encodes to
// "", which no owner ever matches.
func (k OwnerKey) String() string {
	if k.IsZero() {
		return ""
	}
	return string(k.Kind) + ownerKeySep + k.Value
}

// Label is the disambiguated one-line form for prose and diagnostics, where a
// bare value would leave two same-named owners indistinguishable.
func (k OwnerKey) Label() string {
	if k.IsZero() {
		return ""
	}
	return k.Value + " (" + k.KindLabel() + ")"
}

// KindLabel names the namespace the way a reader says it.
func (k OwnerKey) KindLabel() string {
	if k.Kind == OwnerKindDRI {
		return "DRI"
	}
	return "Team"
}

// ParseOwnerKey decodes the wire form. It fails closed: an unknown namespace, a
// missing separator or an empty value is an error rather than a best guess,
// because the raw name `alice` identifies a team and a DRI equally well and
// picking either would be a silent lie about whose estate is on screen.
func ParseOwnerKey(s string) (OwnerKey, error) {
	kind, value, ok := strings.Cut(s, ownerKeySep)
	if !ok || value == "" {
		return OwnerKey{}, ErrInvalidOwnerKey
	}
	switch OwnerKind(kind) {
	case OwnerKindTeam, OwnerKindDRI:
		return OwnerKey{Kind: OwnerKind(kind), Value: value}, nil
	}
	return OwnerKey{}, ErrInvalidOwnerKey
}

// IsEmpty reports whether no ownership information is declared.
func (o Owner) IsEmpty() bool {
	return o.Team == "" && o.DRI == "" && len(o.Contacts) == 0
}

// DisplayString returns the human label: team, else DRI, else "". It is
// presentation only. Two owners in different namespaces can share it, so
// grouping, routing or deduplicating by this value merges owners who are not the
// same owner — use [Owner.Key] for any of that.
func (o Owner) DisplayString() string {
	if o.Team != "" {
		return o.Team
	}
	return o.DRI
}

// IsKey reports whether this owner IS the named canonical owner: an exact
// comparison against the wire form of [Owner.Key]. It is the identity question,
// and [Owner.MatchesFilter] beside it is the search question — with owners
// `team-a` and `team-a-platform` in one fleet, a substring search matches both
// and only one of them is `team-a`. Use this wherever the answer is canonical
// (an owner's estate, a link taken from an owner page, a ranking row's
// destination); use MatchesFilter for what a reader typed.
//
// An owner declaring only contacts has no canonical key and matches none. So
// does a raw, un-namespaced name: `alice` is not `team:alice`, and answering it
// with either namespace — or both — is the collision this key exists to close.
func (o Owner) IsKey(key string) bool {
	k := o.KeyString()
	return k != "" && k == key
}

// Equal reports semantic equality of the whole declaration.
//
// Contacts are compared as a SET, in the full sense: neither position nor
// multiplicity carries meaning. The schema gives list order none — an escalation
// email and a support channel are the same two contact points either way round,
// so re-sorting the YAML must not turn one owner into two and report a service as
// disputed by nobody. Repetition names nothing new either: `[ops]` and
// `[ops, ops]` are one contact point written twice, and a service whose revisions
// spell it each way is owned, not disputed.
//
// The schema permits the repetition rather than rejecting it, because a duplicate
// contact point is harmless noise in an authored file and not worth failing a
// contract over. [Owner.ContactSet] is the normalized form this comparison and any
// presentation of the block both read.
func (o Owner) Equal(other Owner) bool {
	if o.Team != other.Team || o.DRI != other.DRI {
		return false
	}
	a, b := o.ContactSet(), other.ContactSet()
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ContactSet returns the declared contact points as the set they are: ordered
// deterministically by type, value then purpose, with exact duplicates removed.
// Two contact points differing in any of those three fields are two members.
func (o Owner) ContactSet() []OwnerContact {
	out := append([]OwnerContact(nil), o.Contacts...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		if out[i].Value != out[j].Value {
			return out[i].Value < out[j].Value
		}
		return out[i].Purpose < out[j].Purpose
	})
	return slices.Compact(out)
}

// MatchesFilter reports whether the query matches team, DRI, or any contact value.
func (o Owner) MatchesFilter(q string) bool {
	q = strings.ToLower(q)
	if strings.Contains(strings.ToLower(o.Team), q) || strings.Contains(strings.ToLower(o.DRI), q) {
		return true
	}
	for _, c := range o.Contacts {
		if strings.Contains(strings.ToLower(c.Value), q) {
			return true
		}
	}
	return false
}
