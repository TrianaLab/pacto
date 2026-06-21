package contract

import "strings"

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

// IsEmpty reports whether no ownership information is declared.
func (o Owner) IsEmpty() bool {
	return o.Team == "" && o.DRI == "" && len(o.Contacts) == 0
}

// DisplayString returns the canonical human label: team, else DRI, else "".
func (o Owner) DisplayString() string {
	if o.Team != "" {
		return o.Team
	}
	return o.DRI
}

// Equal reports semantic equality.
func (o Owner) Equal(other Owner) bool {
	if o.Team != other.Team || o.DRI != other.DRI || len(o.Contacts) != len(other.Contacts) {
		return false
	}
	for i := range o.Contacts {
		if o.Contacts[i] != other.Contacts[i] {
			return false
		}
	}
	return true
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
