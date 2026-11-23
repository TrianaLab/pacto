package app

import (
	"cmp"
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/internal/discover"
	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/pkg/backstage"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/semver"
)

// maxSummaryRunes is the service.summary bound of the 2.5 schema.
const maxSummaryRunes = 200

// templateReadsNoWorkload is why a golden path's repository rungs that read
// a workload give nothing.
const templateReadsNoWorkload = "a golden path reads no workload"

// InferredTemplate reads the inferred golden path subject names,
// template:<namespace>/<name>, from the Backstage catalog at baseURL with the
// reader token: through the same source and fold the fleet snapshot uses, so
// the name rule and the bounds are the same. A catalog that cannot be read
// is a refusal with its sanitized reason, never "no such template".
func (s *Service) InferredTemplate(ctx context.Context, baseURL, token, subject string) (*fleet.InferredTemplate, error) {
	ref, ok := strings.CutPrefix(subject, fleet.TemplateSubjectPrefix)
	ns, name, slash := strings.Cut(ref, "/")
	if !ok || !slash || ns == "" || name == "" {
		return nil, &DeclareError{Code: codeTemplateNameInvalid, Message: subject + " names no golden path: a catalog template is template:<namespace>/<name>"}
	}
	if baseURL == "" {
		return nil, &DeclareError{Code: codeTemplateCatalogUnset,
			Message: "accepting a template reads the Backstage catalog: pass --backstage-url (the reader token comes from PACTO_BACKSTAGE_TOKEN)",
			CLI:     "pacto declare " + subject + " --backstage-url <backstage-url> --registry <registry>"}
	}
	src, err := fleetsrc.NewBackstageSource(backstage.SourceKind, baseURL, token, s.env().now)
	if err != nil {
		return nil, &DeclareError{Code: codeTemplateCatalogUnset, Message: err.Error()}
	}
	col, _ := src.Collect(ctx) // ponytail: Collect never fails; its state says why
	u, _ := url.Parse(baseURL) // NewBackstageSource parsed it
	host := u.Host
	if st := col.State; st.Status == fleet.SourceUnavailable {
		return nil, &DeclareError{Code: codeTemplateCatalogUnavailable, Message: "the catalog at " + host + " could not be read: " + st.Error.Message}
	}
	// ponytail: one memory source does no I/O, so the build cannot fail.
	snap, _ := fleet.Build(context.WithoutCancel(ctx), fleet.BuildOptions{Now: s.env().now}, fleet.NewMemorySource(src.ID(), src.Kind(), col))
	for i, t := range snap.InferredTemplates {
		if t.Entity == subject {
			return &snap.InferredTemplates[i], nil
		}
	}
	msg := "no template " + ref + " in the catalog at " + host
	if col.State.Status == fleet.SourcePartial {
		msg += " (the catalog read was partial, so it may hold one past the read)"
	}
	return nil, &DeclareError{Code: codeTemplateNotFound, Message: msg, CLI: "pacto fleet entities --template --backstage-url " + baseURL}
}

// fromTemplate writes the golden path a person accepts from an inferred
// template (Decision 10): a 2.5 pacto.yaml naming the template, its summary
// and service.template, and a record that infers nothing. The golden path
// holds the contract only; the owner the catalog names stays a note. Only a
// first acceptance into an empty repository is planned (Decision 19), so a
// name another service or golden path holds is never written over.
func (pl *declarePlanner) fromTemplate(ctx context.Context) error {
	t := pl.req.Template
	if !contract.ValidSlug(t.Name) {
		return &DeclareError{Code: codeTemplateNameInvalid, Message: fmt.Sprintf("%q is no name a contract can carry: a golden path is named in lower-case letters, digits and dashes", t.Name)}
	}
	pl.plan.Service = t.Name
	tags, err := pl.reg.Tags(ctx, pl.repo())
	if err != nil {
		return &DeclareError{Code: codeRegistryUnreadable, Message: "listing the versions in " + pl.repo() + ": " + err.Error()}
	}
	if v := semver.Filter(tags); len(v) > 0 {
		return &DeclareError{Code: codeTemplateNameTaken, CLI: "pacto publish <dir>",
			Message: fmt.Sprintf("%s already holds %s %s: a template is accepted only into an empty repository; publish later versions of a golden path with pacto publish, "+
				"or accept it with another --registry", pl.repo(), t.Name, v[0])}
	}
	pl.dir = filepath.Join(pl.scratch, t.Name)
	pl.rec = &contract.InferredRecord{Format: contract.InferredRecordFormat, Inputs: contract.InferredInputs{Pacto: pl.s.buildVersion()}, Fields: []contract.Inferred{}}
	pl.plan.Notes = append(pl.plan.Notes, templateNotes(t)...)
	return pl.writeFile(DefaultContractPath, templateContract(t))
}

// templateNotes say where the template was read, that the golden path holds
// the contract only and which owner the catalog names.
func templateNotes(t *fleet.InferredTemplate) []string {
	read := ""
	if t.ObservedAt != nil {
		read = ", read " + t.ObservedAt.UTC().Format(time.RFC3339)
	}
	out := []string{"drafted from " + t.Entity + " in " + t.From + read,
		"the golden path holds the contract only: Backstage keeps the template's steps and skeleton; open it there to scaffold code"}
	if t.OwnerKey != "" {
		out = append(out, "Backstage names "+t.OwnerKey+" as owner: declare service.owner in a later version")
	}
	return out
}

// templateContract is the pacto.yaml of a template's golden path:
// service.summary is the catalog's description, else its title, cut to the
// schema's 200 characters, and left out when the catalog gives neither.
func templateContract(t *fleet.InferredTemplate) []byte {
	summary := ""
	if s := []rune(cmp.Or(t.Summary, t.Title)); len(s) > 0 {
		summary = "  summary: " + discover.Quote(string(s[:min(len(s), maxSummaryRunes)])) + "\n"
	}
	return fmt.Appendf(nil, "pactoVersion: \"2.5\"\nservice:\n  name: %s\n  version: %s\n%s  template: {}\n", discover.Quote(t.Name), firstDeclaration, summary)
}

// templateCommand is the pacto declare that replays a template: subject:
// the catalog, the registry, the draft accepted and intent.
func (req DeclareRequest) templateCommand(intent string) string {
	parts := []string{"pacto declare", req.TemplateSubject, "--backstage-url", cmp.Or(req.BackstageURL, "<backstage-url>"), "--registry", cmp.Or(req.Registry, "<registry>")}
	if req.FromDraft != "" {
		parts = append(parts, "--from-draft", req.FromDraft)
	}
	if intent != "" {
		parts = append(parts, "--intent", intent)
	}
	return strings.Join(parts, " ")
}
