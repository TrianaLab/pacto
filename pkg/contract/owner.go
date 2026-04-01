package contract

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Owner represents ownership information for a service.
// It supports two forms for backward compatibility:
//   - Legacy string: "team/payments"
//   - Structured object: { team: "foundations", dri: "eduardo.diaz", contacts: [...] }
type Owner struct {
	// raw holds the original string value (legacy form).
	raw string

	// structured holds the parsed object form (nil when legacy string).
	structured *OwnerInfo
}

// OwnerInfo is the structured ownership metadata.
type OwnerInfo struct {
	Team     string         `yaml:"team,omitempty" json:"team,omitempty"`
	DRI      string         `yaml:"dri,omitempty" json:"dri,omitempty"`
	Contacts []OwnerContact `yaml:"contacts,omitempty" json:"contacts,omitempty"`
}

// OwnerContact is a provider-neutral contact point.
type OwnerContact struct {
	Type    string `yaml:"type" json:"type"`
	Value   string `yaml:"value" json:"value"`
	Purpose string `yaml:"purpose,omitempty" json:"purpose,omitempty"`
}

// NewOwnerFromString creates an Owner from a legacy string value.
func NewOwnerFromString(s string) Owner {
	return Owner{raw: s}
}

// NewOwnerFromInfo creates an Owner from structured ownership metadata.
func NewOwnerFromInfo(info OwnerInfo) Owner {
	return Owner{structured: &info}
}

// IsEmpty returns true if no ownership information is set.
func (o Owner) IsEmpty() bool {
	return o.raw == "" && o.structured == nil
}

// IsStructured returns true if the owner uses the structured object form.
func (o Owner) IsStructured() bool {
	return o.structured != nil
}

// String returns the legacy string value. Empty if structured form.
func (o Owner) String() string {
	return o.raw
}

// Info returns the structured ownership info, or nil if legacy string form.
func (o Owner) Info() *OwnerInfo {
	return o.structured
}

// Team returns the team name. For legacy strings, returns the full string.
// For structured owners, returns the Team field.
func (o Owner) Team() string {
	if o.structured != nil {
		return o.structured.Team
	}
	return o.raw
}

// DRI returns the DRI. Empty for legacy string owners.
func (o Owner) DRI() string {
	if o.structured != nil {
		return o.structured.DRI
	}
	return ""
}

// Contacts returns the contacts list. Nil for legacy string owners.
func (o Owner) Contacts() []OwnerContact {
	if o.structured != nil {
		return o.structured.Contacts
	}
	return nil
}

// DisplayString returns a human-readable representation of the owner.
// For legacy strings, returns the string as-is.
// For structured owners, returns the team name (or DRI if no team).
func (o Owner) DisplayString() string {
	if o.structured != nil {
		if o.structured.Team != "" {
			return o.structured.Team
		}
		if o.structured.DRI != "" {
			return o.structured.DRI
		}
		return "(structured owner)"
	}
	return o.raw
}

// Equal reports whether two Owner values are semantically equal.
func (o Owner) Equal(other Owner) bool {
	if o.IsEmpty() && other.IsEmpty() {
		return true
	}
	if o.IsStructured() != other.IsStructured() {
		return false
	}
	if !o.IsStructured() {
		return o.raw == other.raw
	}
	// Both structured: compare fields.
	a, b := o.structured, other.structured
	if a.Team != b.Team || a.DRI != b.DRI {
		return false
	}
	if len(a.Contacts) != len(b.Contacts) {
		return false
	}
	for i := range a.Contacts {
		if a.Contacts[i] != b.Contacts[i] {
			return false
		}
	}
	return true
}

// MatchesFilter returns true if the owner matches a search query.
// Matches against team, dri, and contact values (case-insensitive).
func (o Owner) MatchesFilter(query string) bool {
	if o.IsEmpty() {
		return false
	}
	q := strings.ToLower(query)
	if o.structured != nil {
		if strings.Contains(strings.ToLower(o.structured.Team), q) {
			return true
		}
		if strings.Contains(strings.ToLower(o.structured.DRI), q) {
			return true
		}
		for _, c := range o.structured.Contacts {
			if strings.Contains(strings.ToLower(c.Value), q) {
				return true
			}
		}
		return false
	}
	return strings.Contains(strings.ToLower(o.raw), q)
}

// MarshalJSON implements json.Marshaler.
func (o Owner) MarshalJSON() ([]byte, error) {
	if o.structured != nil {
		return json.Marshal(o.structured)
	}
	if o.raw != "" {
		return json.Marshal(o.raw)
	}
	// Empty owner: omitted by the caller via omitempty tag
	return json.Marshal(nil)
}

// UnmarshalJSON implements json.Unmarshaler.
func (o *Owner) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		*o = Owner{}
		return nil
	}

	// Try string first (most common).
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*o = Owner{raw: s}
		return nil
	}

	// Try structured object.
	var info OwnerInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return fmt.Errorf("owner must be a string or object: %w", err)
	}
	*o = Owner{structured: &info}
	return nil
}

// MarshalYAML implements yaml.Marshaler.
func (o Owner) MarshalYAML() (interface{}, error) {
	if o.structured != nil {
		return o.structured, nil
	}
	if o.raw != "" {
		return o.raw, nil
	}
	return nil, nil
}

// UnmarshalYAML implements yaml.Unmarshaler.
func (o *Owner) UnmarshalYAML(unmarshal func(interface{}) error) error {
	// Try string first.
	var s string
	if err := unmarshal(&s); err == nil {
		*o = Owner{raw: s}
		return nil
	}

	// Try structured object.
	var info OwnerInfo
	if err := unmarshal(&info); err != nil {
		return fmt.Errorf("owner must be a string or object: %w", err)
	}
	*o = Owner{structured: &info}
	return nil
}
