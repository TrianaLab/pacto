package fleet

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The text forms below are shared by the CLI and the TUI so the two print a
// resource the same way.

// Text renders one declared request: its values, the provider revision it
// resolves to and the provider configuration it selects.
func (r RequestedConfiguration) Text() string {
	values := make([]string, 0, len(r.Values.Items))
	for _, f := range r.Values.Items {
		values = append(values, f.Key+"="+f.Value)
	}
	s := "requested (" + r.Provenance + "): " + cmp.Or(strings.Join(values, " "), "none")
	if r.Revision != nil {
		s += ", provider " + revisionText(r.Revision)
	}
	if r.Configuration != "" {
		s += ", configuration " + r.Configuration
	}
	return s
}

// revisionText names a revision as service@version, or by its label when it
// has no version.
func revisionText(r *EntityRef) string {
	if r.ParentService != "" && r.Version != "" {
		return r.ParentService + "@" + r.Version
	}
	return r.Label
}

// Text renders one provisioning status with its provenance: the environment
// and entry, the phase (and the phase a stale or foreign report said), the
// adapter and external reference, signed or unsigned, asOf and, when set, why.
func (st ProvisioningStatus) Text() string {
	phase := st.Phase
	if st.ReportedPhase != "" && st.ReportedPhase != st.Phase {
		phase += " (reported " + st.ReportedPhase + ")"
	}
	parts := []string{cmp.Or(strings.TrimSpace(st.Environment+" "+st.Entry), "-"), phase}
	if adapter := strings.TrimSpace(st.Provider + " " + st.ExternalRef); adapter != "" {
		parts = append(parts, adapter)
	}
	if st.Source != "" && st.Verified {
		parts = append(parts, "signed")
	} else if st.Source != "" {
		parts = append(parts, "unsigned")
	}
	if st.AsOf != nil {
		parts = append(parts, "asOf "+st.AsOf.UTC().Format(time.RFC3339))
	}
	s := strings.Join(parts, "  ")
	if st.Reason != "" {
		s += ": " + st.Reason
	}
	return s
}

// Text renders what Pacto knows about a resource's consumption in one
// environment: figures only when reported, otherwise the state, why and the
// raw figures kept apart.
func (c ResourceConsumption) Text() string {
	where := ""
	if c.Environment != "" {
		where = " " + c.Environment
	}
	switch {
	case c.State == consumptionNotReported:
		return "usage" + where + ": not reported by any provider; for live signals ask Mira (query_metric)"
	case c.Figures != nil:
		return "usage" + where + ": " + strings.Join(append([]string{figuresText(c.Figures)}, usageProvenance(c)...), "  ")
	}
	s := "usage" + where + ": " + c.State
	if c.Reason != "" {
		s += ": " + c.Reason
	}
	switch {
	case c.Reported != nil:
		s += " (" + strings.Join(append([]string{"reported " + figuresText(c.Reported)}, usageProvenance(c)...), ", ") + ")"
	case len(c.Conflict) > 0:
		s += " (" + strings.Join(c.Conflict, ", ") + ")"
	}
	return s
}

// figuresText renders a provider's usage figures as it reported them: each
// metric with its unit, then the cost over its period. Nothing is summed.
func figuresText(f *UsageFigures) string {
	parts := make([]string, 0, len(f.Metrics)+1)
	for _, m := range f.Metrics {
		parts = append(parts, strings.TrimSpace(m.Name+" "+strconv.FormatFloat(m.Value, 'f', -1, 64)+" "+m.Unit))
	}
	if f.Cost != nil {
		parts = append(parts, "cost "+f.Cost.Amount+" "+f.Cost.Currency+"/"+f.Cost.Period)
	}
	return strings.Join(parts, "  ")
}

// usageProvenance is who reported a usage figure: the producer when named,
// signed or unsigned, and asOf.
func usageProvenance(c ResourceConsumption) []string {
	parts := []string{c.Producer}
	if c.Verified {
		parts = append(parts, "signed")
	} else {
		parts = append(parts, "unsigned")
	}
	if c.AsOf != nil {
		parts = append(parts, "asOf "+c.AsOf.UTC().Format(time.RFC3339))
	}
	return slices.DeleteFunc(parts, func(s string) bool { return s == "" })
}
