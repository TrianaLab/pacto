package contract

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Guarantees is the behavior an interface promises (interfaces[].guarantees)
// or a consumer relies on (dependencies[].requires[]), in a closed vocabulary
// (pactoVersion 2.2). Every field is optional; an empty one promises or
// requires nothing. Durations are Go durations such as 30s, 5m or 168h.
//
// Delivery, Ordering, DedupeWindow and Retention describe event streams;
// Idempotent and Timeout describe request/reply calls.
type Guarantees struct {
	// Profile is the version of this vocabulary. Omitted means "1", the only
	// version so far.
	Profile string `yaml:"profile,omitempty" json:"profile,omitempty"`
	// Delivery is what happens when a delivery fails partway.
	Delivery string `yaml:"delivery,omitempty" json:"delivery,omitempty"`
	// Ordering is the scope in which messages arrive in the order sent.
	Ordering string `yaml:"ordering,omitempty" json:"ordering,omitempty"`
	// DedupeWindow is how long a repeated message is recognised and dropped.
	DedupeWindow string `yaml:"dedupeWindow,omitempty" json:"dedupeWindow,omitempty"`
	// Retention is how long a message stays available for replay.
	Retention string `yaml:"retention,omitempty" json:"retention,omitempty"`
	// Idempotent means repeating a call has the effect of making it once.
	Idempotent bool `yaml:"idempotent,omitempty" json:"idempotent,omitempty"`
	// Timeout is the longest a call takes to answer.
	Timeout string `yaml:"timeout,omitempty" json:"timeout,omitempty"`
}

// GuaranteeTerm is one guarantee a [Guarantees] sets: its name as a reader
// says it, its value and, for a duration, how an offered one has to compare
// with a required one ("at least" or "at most").
type GuaranteeTerm struct {
	Name, Value, Bound string
}

// Terms lists the guarantees g sets, in the order the contract reference
// lists the vocabulary. Profile is the vocabulary's version, not a guarantee.
func (g Guarantees) Terms() []GuaranteeTerm {
	var out []GuaranteeTerm
	for _, t := range []GuaranteeTerm{
		{"delivery", g.Delivery, ""}, {"ordering", g.Ordering, ""},
		{"dedupe window", g.DedupeWindow, "at least"}, {"retention", g.Retention, "at least"},
		{"timeout", g.Timeout, "at most"}, {"idempotent", boolText(g.Idempotent), ""},
	} {
		if t.Value != "" {
			out = append(out, t)
		}
	}
	return out
}

// Text names the guarantees g sets on one line, such as "delivery
// at-least-once, timeout 5s, idempotent". required spells out each duration's
// bound ("timeout at most 5s"), as a dependency's requirement reads.
func (g Guarantees) Text(required bool) string {
	parts := make([]string, 0, 6)
	for _, t := range g.Terms() {
		switch {
		case t.Name == "idempotent":
			parts = append(parts, t.Name)
		case required && t.Bound != "":
			parts = append(parts, t.Name+" "+t.Bound+" "+t.Value)
		default:
			parts = append(parts, t.Name+" "+t.Value)
		}
	}
	return strings.Join(parts, ", ")
}

// String is the interface a requirement names and what it requires of it,
// such as "http: timeout at most 5s, idempotent". A requirement that names only
// an interface requires that the provider declares it, and reads "http".
func (r Requirement) String() string {
	if t := r.Text(true); t != "" {
		return r.Interface + ": " + t
	}
	return r.Interface
}

// Ordering scopes. Global ordering also orders every key.
const (
	OrderingPerKey = "per-key"
	OrderingGlobal = "global"
)

// Requirement is the behavior a consumer requires from one interface of the
// provider a dependency names.
type Requirement struct {
	Interface  string `yaml:"interface" json:"interface"`
	Guarantees `yaml:",inline"`
}

// Mismatch is one required guarantee a provider does not meet: Offered is
// empty when the provider does not declare it. Guarantee "interface" means the
// provider declares no interface named Interface.
type Mismatch struct {
	Consumer  string `json:"consumer"`
	Provider  string `json:"provider"`
	Interface string `json:"interface"`
	Guarantee string `json:"guarantee"`
	Required  string `json:"required"`
	Offered   string `json:"offered,omitempty"`
}

// bounds is how an offered duration has to compare with a required one.
var bounds = map[string]string{"dedupeWindow": ">= ", "retention": ">= ", "timeout": "<= "}

func (m Mismatch) String() string {
	if m.Guarantee == "interface" {
		return fmt.Sprintf("%s requires interface %s from %s, which declares no such interface", m.Consumer, m.Interface, m.Provider)
	}
	offered := m.Offered
	if offered == "" {
		offered = "nothing"
	}
	return fmt.Sprintf("%s requires %s %s %s%s from %s, which offers %s", m.Consumer, m.Interface, m.Guarantee, bounds[m.Guarantee], m.Required, m.Provider, offered)
}

// Unmet returns every guarantee dep requires that provider does not meet,
// naming consumer as the declarer. It is empty when every requirement holds.
//
// ponytail: profiles are not compared while "1" is the only one; a second
// profile needs a mapping between them here.
func Unmet(consumer string, dep Dependency, provider *Contract) []Mismatch {
	var out []Mismatch
	for _, r := range dep.Requires {
		m := Mismatch{Consumer: consumer, Provider: provider.Service.Name, Interface: r.Interface}
		var offered *Guarantees
		for i := range provider.Interfaces {
			if provider.Interfaces[i].Name == r.Interface {
				offered = &Guarantees{}
				if g := provider.Interfaces[i].Guarantees; g != nil {
					offered = g
				}
			}
		}
		if offered == nil {
			m.Guarantee, m.Required = "interface", r.Interface
			out = append(out, m)
			continue
		}
		check := func(name, req, off string, ok bool) {
			if req != "" && !ok {
				m.Guarantee, m.Required, m.Offered = name, req, off
				out = append(out, m)
			}
		}
		o := *offered
		check("delivery", r.Delivery, o.Delivery, r.Delivery == o.Delivery)
		check("ordering", r.Ordering, o.Ordering, r.Ordering == o.Ordering || o.Ordering == OrderingGlobal)
		check("dedupeWindow", r.DedupeWindow, o.DedupeWindow, atLeast(o.DedupeWindow, r.DedupeWindow))
		check("retention", r.Retention, o.Retention, atLeast(o.Retention, r.Retention))
		check("timeout", r.Timeout, o.Timeout, atLeast(r.Timeout, o.Timeout))
		check("idempotent", boolText(r.Idempotent), boolText(o.Idempotent), o.Idempotent || !r.Idempotent)
	}
	return out
}

// atLeast reports whether duration a is at least duration b. A missing or
// unreadable duration meets nothing: the schema admits only readable ones.
func atLeast(a, b string) bool {
	da, errA := time.ParseDuration(a)
	db, errB := time.ParseDuration(b)
	return errA == nil && errB == nil && da >= db
}

func boolText(b bool) string {
	if !b {
		return ""
	}
	return strconv.FormatBool(b)
}
