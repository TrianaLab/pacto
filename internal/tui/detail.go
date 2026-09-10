package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// detailScreen renders one fleet.EntityDetail in a scrollable viewport. It
// keeps the ref it was opened with so every verb sees the same selection here
// as on the list.
type detailScreen struct {
	ref     fleet.EntityRef
	vp      viewport.Model
	loadErr error
}

func newDetailScreen(c *Context, ref fleet.EntityRef) screen {
	d := &detailScreen{ref: ref, vp: viewport.New()}
	d.refresh(c)
	return d
}

// refresh re-reads the entity from the current snapshot. The root Model calls
// this on every screen after a reload, so the page a reader is looking at
// describes the fleet the footer says it does. A lookup that now fails is
// recorded rather than left showing the old body: an entity that has gone is
// news, not a rendering problem.
func (d *detailScreen) refresh(c *Context) {
	det, err := c.Query.EntityDetail(d.ref.Kind, d.ref.Key)
	if err != nil {
		d.loadErr = err
		return
	}
	d.loadErr = nil
	d.vp.SetContent(renderDetail(det))
}

func (d *detailScreen) selected() (fleet.EntityRef, bool) { return d.ref, true }

func (d *detailScreen) Title() string {
	if d.ref.Label != "" {
		return d.ref.Label
	}
	return d.ref.Key
}

func (d *detailScreen) Update(c *Context, msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		if cmd, handled := dispatchVerb(c, d, k); handled {
			return d, cmd
		}
	}
	d.resize(c)
	vp, cmd := d.vp.Update(msg)
	d.vp = vp
	return d, cmd
}

// resize sizes the viewport. A viewport built with no options is 0x0 and its
// View returns the empty string, so this must run before the first render. The
// content is set by refresh rather than here: Update calls resize on every
// message, and re-rendering the whole body on every keypress buys nothing.
func (d *detailScreen) resize(c *Context) {
	d.vp.SetWidth(c.Width)
	h := c.Height - 3
	if h < 3 {
		h = 3
	}
	d.vp.SetHeight(h)
}

func (d *detailScreen) View(c *Context) string {
	if d.loadErr != nil {
		return errorStyle.Render("lookup failed: " + d.loadErr.Error())
	}
	d.resize(c)
	return d.vp.View()
}

// renderDetail turns the discriminated detail envelope into text. Exactly one
// payload is populated; an envelope with none is a fleet bug, and saying so is
// better than rendering a convincing blank page.
func renderDetail(d *fleet.EntityDetail) string {
	var b strings.Builder
	switch {
	case d.Service != nil:
		renderServiceDetail(&b, d.Service)
	case d.Revision != nil:
		renderRevisionDetail(&b, d.Revision)
	case d.Target != nil:
		renderTargetDetail(&b, d.Target)
	case d.Owner != nil:
		renderOwnerDetail(&b, d.Owner)
	case d.Source != nil:
		renderSourceDetail(&b, d.Source)
	default:
		return warnStyle.Render("the fleet returned a detail envelope with no payload")
	}
	if len(d.Actions) > 0 {
		b.WriteString("\n" + headerStyle.Render("Suggested actions") + "\n")
		for _, a := range d.Actions {
			b.WriteString("  " + safeText(a) + "\n")
		}
	}
	return b.String()
}

// field writes one aligned label/value line, skipping empty values so a detail
// page never shows a column of blanks. Every value here comes from the fleet,
// so it goes through safeText.
func field(b *strings.Builder, label, value string) {
	if value == "" {
		return
	}
	styledField(b, label, safeText(value))
}

// styledField is field for a value this package rendered itself. Such a value
// is control characters on purpose — that is what a lipgloss style is — so it
// must not go through safeText, which would print the style as text.
func styledField(b *strings.Builder, label, value string) {
	fmt.Fprintf(b, "  %s %s\n", dimStyle.Render(pad(label+":", 18)), value)
}

func section(b *strings.Builder, title string) {
	b.WriteString("\n" + headerStyle.Render(title) + "\n")
}

func renderServiceDetail(b *strings.Builder, s *fleet.ServiceDetailData) {
	field(b, "domain", s.Domain)
	if s.Ownership != nil {
		field(b, "owner", s.Ownership.Owner)
	}
	section(b, "Summary")
	field(b, "revisions", fmt.Sprintf("%d (%d in use)", s.Summary.Revisions, s.Summary.RevisionsInUse))
	field(b, "targets", fmt.Sprintf("%d", s.Summary.Targets))
	field(b, "dependencies", fmt.Sprintf("%d", s.Summary.DeclaredDependencies))
	field(b, "dependents", fmt.Sprintf("%d", s.Dependents.Total))
	if s.ActiveRevisions.Total > 0 {
		section(b, "Active revisions")
		for _, r := range s.ActiveRevisions.Items {
			fmt.Fprintf(b, "  %s\n", safeText(r.Label))
		}
		if s.ActiveRevisions.Truncated {
			b.WriteString("  " + dimStyle.Render(fmt.Sprintf("... and %d more", s.ActiveRevisions.Total-len(s.ActiveRevisions.Items))) + "\n")
		}
	}
	if s.Findings.Total > 0 {
		section(b, fmt.Sprintf("Findings (%d)", s.Findings.Total))
		for _, f := range s.Findings.Items {
			fmt.Fprintf(b, "  %s %s: %s\n", statusStyle(string(f.Finding.Severity)).Render(safeText(string(f.Finding.Severity))), safeText(f.Entity.Label), safeText(f.Finding.Message))
		}
		if s.Findings.Truncated {
			b.WriteString("  " + dimStyle.Render(fmt.Sprintf("... and %d more", s.Findings.Total-len(s.Findings.Items))) + "\n")
		}
	}
}

func renderRevisionDetail(b *strings.Builder, r *fleet.RevisionDetailData) {
	field(b, "service", r.Service.Label)
	field(b, "version", r.Version)
	field(b, "pacto version", r.PactoVersion)
	field(b, "valid", fmt.Sprintf("%t", r.Valid))
	field(b, "workload", r.Workload)
	if r.Ownership != nil {
		field(b, "owner", r.Ownership.Owner)
	}
	renderRevisionIdentity(b, r.Identity)
	if r.Provenance.Source != "" {
		section(b, "Provenance")
		field(b, "source", r.Provenance.Source)
	}
	if r.Readiness != nil {
		section(b, "Readiness")
		field(b, "passing", fmt.Sprintf("%t", r.Readiness.Passing))
		field(b, "score", fmt.Sprintf("%d/%d", r.Readiness.Score, r.Readiness.MinScore))
		field(b, "checks", fmt.Sprintf("%d done, %d not done", r.Readiness.DoneCount, r.Readiness.NotDoneCount))
	}
	if r.Interfaces.Total > 0 {
		section(b, fmt.Sprintf("Interfaces (%d)", r.Interfaces.Total))
		for _, iface := range r.Interfaces.Items {
			fmt.Fprintf(b, "  %s: %s\n", safeText(iface.Name), safeText(iface.Type))
		}
	}
	if r.Configurations.Total > 0 {
		section(b, fmt.Sprintf("Configurations (%d)", r.Configurations.Total))
		for _, cfg := range r.Configurations.Items {
			fmt.Fprintf(b, "  %s\n", safeText(cfg.Name))
		}
	}
	if r.Policies.Total > 0 {
		section(b, fmt.Sprintf("Policies (%d)", r.Policies.Total))
		for _, pol := range r.Policies.Items {
			fmt.Fprintf(b, "  %s\n", safeText(pol.Name))
		}
	}
	if r.ExactTargets.Total > 0 || r.InferredTargets.Total > 0 {
		section(b, "Targets")
		field(b, "exact", fmt.Sprintf("%d", r.ExactTargets.Total))
		field(b, "inferred", fmt.Sprintf("%d", r.InferredTargets.Total))
	}
}

func renderRevisionIdentity(b *strings.Builder, id fleet.RevisionIdentity) {
	section(b, "Identity")
	field(b, "class", string(id.IdentityClass))
	field(b, "digest", id.Digest)
	field(b, "requested ref", id.RequestedRef)
	field(b, "resolved ref", id.ResolvedRef)
	field(b, "retrievable", fmt.Sprintf("%t", id.Retrievable))
}

func renderTargetDetail(b *strings.Builder, t *fleet.TargetDetailData) {
	field(b, "service", t.Service.Label)
	if t.Revision != nil {
		field(b, "revision", t.Revision.Label)
	}
	field(b, "link state", t.LinkState)
	field(b, "scope", t.Scope)
	field(b, "kind", t.Kind)
	field(b, "compliance", t.Compliance)
	field(b, "source", t.Source)
	field(b, "stale", fmt.Sprintf("%t", t.Stale))
	if t.Ownership != nil {
		field(b, "owner", t.Ownership.Owner)
	}
	if t.Coverage != nil {
		section(b, "Coverage")
		field(b, "evaluated", fmt.Sprintf("%d/%d", t.Coverage.Evaluated, t.Coverage.Required))
	}
	if t.Readiness != nil {
		section(b, "Readiness")
		field(b, "passing", fmt.Sprintf("%t", t.Readiness.Passing))
		field(b, "score", fmt.Sprintf("%d/%d", t.Readiness.Score, t.Readiness.MinScore))
		field(b, "checks", fmt.Sprintf("%d done, %d not done", t.Readiness.DoneCount, t.Readiness.NotDoneCount))
	}
	if t.ObservedRuntime.Count > 0 {
		totalStr := fmt.Sprintf("%d", t.ObservedRuntime.Count)
		if t.ObservedRuntime.Total != nil {
			totalStr = fmt.Sprintf("%d", *t.ObservedRuntime.Total)
		}
		section(b, fmt.Sprintf("Observed runtime (%s)", totalStr))
		for _, kv := range t.ObservedRuntime.Items {
			fmt.Fprintf(b, "  %s: %s\n", safeText(kv.Key), safeText(kv.Value))
		}
		if t.ObservedRuntime.Truncated {
			b.WriteString("  " + dimStyle.Render("... truncated") + "\n")
		}
	}
	if t.Findings.Total > 0 {
		section(b, fmt.Sprintf("Findings (%d)", t.Findings.Total))
		for _, f := range t.Findings.Items {
			fmt.Fprintf(b, "  %s: %s\n", statusStyle(string(f.Severity)).Render(safeText(string(f.Severity))), safeText(f.Message))
		}
		if t.Findings.Truncated {
			b.WriteString("  " + dimStyle.Render(fmt.Sprintf("... and %d more", t.Findings.Total-len(t.Findings.Items))) + "\n")
		}
	}
}

func renderOwnerDetail(b *strings.Builder, o *fleet.OwnerDetailData) {
	section(b, "Summary")
	field(b, "services", fmt.Sprintf("%d", o.Summary.Services))
	field(b, "revisions", fmt.Sprintf("%d", o.Summary.Revisions))
	field(b, "targets", fmt.Sprintf("%d", o.Summary.Targets))
	if o.Services.Total > 0 {
		section(b, fmt.Sprintf("Services (%d)", o.Services.Total))
		for _, svc := range o.Services.Items {
			fmt.Fprintf(b, "  %s\n", safeText(svc.Label))
		}
		if o.Services.Truncated {
			b.WriteString("  " + dimStyle.Render(fmt.Sprintf("... and %d more", o.Services.Total-len(o.Services.Items))) + "\n")
		}
	}
	if o.Attention.Total > 0 {
		section(b, fmt.Sprintf("Attention (%d)", o.Attention.Total))
		for _, item := range o.Attention.Items {
			fmt.Fprintf(b, "  %s %s: %s\n", statusStyle(item.Severity).Render(safeText(item.Severity)), safeText(item.Service), safeText(item.Summary))
		}
		if o.Attention.Truncated {
			b.WriteString("  " + dimStyle.Render(fmt.Sprintf("... and %d more", o.Attention.Total-len(o.Attention.Items))) + "\n")
		}
	}
}

func renderSourceDetail(b *strings.Builder, s *fleet.SourceDetailData) {
	field(b, "kind", s.Kind)
	field(b, "health", s.Health)
	if s.LastSuccessfulSync != nil {
		field(b, "last sync", s.LastSuccessfulSync.Format("2006-01-02 15:04:05"))
	}
	if s.ObservedAt != nil {
		field(b, "observed at", s.ObservedAt.Format("2006-01-02 15:04:05"))
	}
	section(b, "Records")
	field(b, "revisions", fmt.Sprintf("%d", s.RevisionCount))
	field(b, "targets", fmt.Sprintf("%d", s.TargetCount))
	section(b, "Contributed entities")
	field(b, "services", fmt.Sprintf("%d", s.Contributed.Services))
	field(b, "revisions", fmt.Sprintf("%d", s.Contributed.Revisions))
	field(b, "targets", fmt.Sprintf("%d", s.Contributed.Targets))
	if s.Error != nil {
		section(b, "Error")
		field(b, "message", s.Error.Message)
	}
}
