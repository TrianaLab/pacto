package app

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing/fstest"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/discover"
	"github.com/trianalab/pacto/v4/internal/pactify"
	"github.com/trianalab/pacto/v4/internal/shellquote"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/orgindex"
	"github.com/trianalab/pacto/v4/pkg/semver"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// CreateFrom is a service created from a golden path: a declaration whose
// source is the path, never a workload. Path is the digest-pinned
// oci://<repo>@sha256: reference the caller resolved from its own restricted
// fleet view (fleet.Query.CreatablePath); nothing else names what is pulled.
// Repository is where the first version goes (publish.Layout of the registry
// when empty), Revision its publish revision (pacto-create:<caller>:<time>
// when empty). Consumable is the caller's consumable-offerings read, by
// offering name, and ConsumablePartial says that read was partial.
type CreateFrom struct {
	Path, Owner, Namespace, Repository, Revision string
	// Caller is the reviewed person or agent the create runs for: the
	// default revision names them, and only their own drafts are listed.
	Caller            string
	Offerings         []OfferingPick
	Consumable        map[string]CreateOffering
	ConsumablePartial bool
	ConsumableAsOf    string
	// Trust holds the template keys the path's signature is checked with,
	// loaded once when the host started.
	Trust *delivery.TrustConfig
	// OpenIndex opens the organization index repository a published create
	// writes its entry to, through the installation's push credential
	// scoped to it; nil opens it with the process's own credentials.
	OpenIndex func(repo string) (*remote.Repository, error)
	// ReviewURL is the dashboard page a created draft is reviewed and signed
	// at (<dashboard URL>#/fleet/services/<golden path key>), set by a host
	// that serves one; a written draft's AcceptURL adds its name and tag.
	ReviewURL string
}

// OfferingPick is the values a create sets on one offering: once for every
// environment, and per environment the golden path declares.
type OfferingPick struct {
	Offering     string                    `json:"offering"`
	Values       map[string]any            `json:"values,omitempty"`
	Environments map[string]map[string]any `json:"environments,omitempty"`
}

// CreateOffering is one offering a caller may consume: the provider
// repository a pick appends, every consumable provider's repository (a
// configuration may reference any of them) and the consume snippet appended
// when the golden path does not request the offering. Values are checked
// against the schema the contract's own ref selects, never one read here.
type CreateOffering struct {
	Repository   string
	Repositories []string
	Snippet      *fleet.ConsumeSnippet
}

// provides says repo is one of o's consumable providers.
func (o CreateOffering) provides(repo string) bool {
	return slices.ContainsFunc(append([]string{o.Repository}, o.Repositories...), func(r string) bool { return contract.TrimOCIScheme(r) == repo })
}

// CreateRefusal is one refused input of a create: its code, what happened,
// the field it is at (configurations[<name>].values.<key> or
// environments[<env>].<configuration>.<key>), the closest valid value when
// there is one, the allowed values of an enum or class, and the pacto init
// and pacto_create_service fixes.
type CreateRefusal struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field"`
	Closest any    `json:"closest,omitempty"`
	Allowed []any  `json:"allowed,omitempty"`
	CLI     string `json:"cli,omitempty"`
	MCP     string `json:"mcp,omitempty"`
}

// CreatePlan is what a create adds to a plan: the golden path and its
// signature step, the namespace reviewed, the revision the write sends back,
// the pacto init that recreates the folder (with the values file it reads)
// and how complete the consumable read was.
type CreatePlan struct {
	Path                   string             `json:"path"`
	Signature              fleet.CreationStep `json:"signature"`
	Namespace              string             `json:"namespace"`
	Revision               string             `json:"revision"`
	InitCLI                string             `json:"initCLI"`
	ValuesFile             string             `json:"valuesFile,omitempty"`
	ConsumableAsOf         string             `json:"consumableAsOf,omitempty"`
	ConsumableCompleteness string             `json:"consumableCompleteness"`
}

// CreatedService is the person's own handover after a published create:
// the repository snippet with their own token, the pull of what was
// published and where the index entry went.
type CreatedService struct {
	GitCLI  string `json:"gitCLI"`
	PullCLI string `json:"pullCLI"`
	Index   string `json:"index,omitempty"`
	// Indexed is the index entry written: the fleet shows the service
	// through it alone.
	Indexed bool `json:"indexed"`
}

// CreateValuesFile is the values file InitCLI reads when a create set any
// offering value: the planned dependencies and configurations, whole, since
// pacto init merges an array as a whole.
const CreateValuesFile = "pacto-create-values.yaml"

// createRevisionPrefix starts the default revision of a created service.
const createRevisionPrefix = "pacto-create:"

// maxRevisionBytes bounds an edited revision.
const maxRevisionBytes = 128

// The words of a create's refusals and notes.
const (
	createdNote       = "a created service: every value comes from the golden path %s and the values set here; review pacto.yaml"
	createdDraftNote  = "a created service: every value comes from the golden path %s and the values the agent set; review pacto.yaml"
	createdDraftCLI   = "%s:%s is a created-service draft: review and sign it in the dashboard's Create service, or create the service yourself with pacto init --from"
	createdDraftExtra = "%s:%s holds content its golden path and values do not produce: create the service again"
	partialNote       = "the offerings list is partial: an offering not listed may exist"
	serviceExistsText = "%s is already taken in this registry: pick another name, or adopt your own service with pacto declare"
	noWorkloadYet     = "a created service has no workload yet"
	envValuesNote     = "pacto init sets no per-environment value: the environment files this plan publishes hold them, and the published files (the zip or pacto pull) carry them"
)

// offeringValueCodes maps a value check's finding code to the create refusal
// it is (the same text, never an alias).
var offeringValueCodes = map[finding.Code]string{
	finding.CodeParameterNotDeclared: codeParameterNotDeclared,
	finding.CodeParameterOutOfRange:  codeParameterOutOfRange,
	finding.CodeParameterSensitive:   codeParameterSensitive,
	finding.CodeClassNotOffered:      codeClassNotOffered,
	finding.CodeTierNotTunable:       codeTierNotTunable,
}

// fromGoldenPath writes the created service's folder: the golden path run
// through InitFrom into the scratch folder with no plugin, the offering values
// applied inside each offering's closed parameter space, pactoVersion raised
// as far as the record needs, and a record that infers nothing. A name the
// repository already holds is refused before anything is pulled.
func (pl *declarePlanner) fromGoldenPath(ctx context.Context) error {
	c := *pl.req.Create
	pl.req.Create = &c
	name := pl.req.Service
	if err := checkCreateInputs(name, c); err != nil {
		return err
	}
	repo, err := createRepo(pl.req.Registry, c.Repository, name)
	if err != nil {
		return err
	}
	c.Repository, pl.plan.Service = repo, name
	if c.Revision, err = createRevision(c.Revision, cmp.Or(c.Caller, plannedPerson), pl.s.env().now()); err != nil {
		return err
	}
	if err := pl.createTaken(ctx); err != nil {
		return err
	}
	pl.dir = filepath.Join(pl.scratch, name)
	r, err := pl.s.InitFrom(ctx, InitOptions{Dir: pl.dir, Name: name, Owner: c.Owner, Registry: path.Dir(repo)}, InitFrom{From: c.Path, Trust: c.Trust})
	if err != nil {
		return err
	}
	valuesFile, err := pl.applyOfferings(ctx)
	if err != nil {
		return err
	}
	pl.rec = &contract.InferredRecord{Format: contract.InferredRecordFormat,
		Inputs: contract.InferredInputs{Pacto: pl.s.buildVersion(), Discovery: discover.Version}, Fields: []contract.Inferred{}}
	pl.plan.Create = &CreatePlan{Path: c.Path, Signature: signatureStep(r.Steps), Namespace: c.Namespace, Revision: c.Revision,
		InitCLI: pl.req.command(""), ValuesFile: valuesFile, ConsumableAsOf: c.ConsumableAsOf, ConsumableCompleteness: string(fleet.CompletenessComplete)}
	pl.plan.Notes = append(pl.plan.Notes, fmt.Sprintf(createdNote, c.Path))
	if slices.ContainsFunc(c.Offerings, func(p OfferingPick) bool { return len(p.Environments) > 0 }) {
		pl.plan.Notes = append(pl.plan.Notes, envValuesNote)
	}
	if c.ConsumablePartial {
		pl.plan.Create.ConsumableCompleteness = string(fleet.CompletenessPartial)
		pl.plan.Notes = append(pl.plan.Notes, partialNote)
	}
	pl.plan.create = createIntent(c)
	return nil
}

// checkCreateInputs refuses a create whose name, owner or golden path
// reference is not one a create may take.
func checkCreateInputs(name string, c CreateFrom) error {
	switch {
	case !contract.ValidServiceName(name):
		return &DeclareError{Code: codeFieldNotAcceptable, Message: fmt.Sprintf("%q is not a service name: use lower-case letters, digits and dashes, at most 63", name)}
	case c.Owner == "":
		return &DeclareError{Code: codeOwnerRequired, Message: "a created service names its owner team"}
	}
	if !pinnedPath(c.Path) {
		return &DeclareError{Code: codeGoldenPathNotDeclared, Message: "a service is created only from a declared golden path, pinned by digest"}
	}
	return nil
}

// pinnedPath says ref is an oci://<repo>@sha256: reference.
func pinnedPath(ref string) bool {
	repo, _, digest := contract.SplitOCIRef(contract.TrimOCIScheme(ref))
	return strings.HasPrefix(ref, contract.OCIScheme) && repo != "" && strings.HasPrefix(digest, "sha256:")
}

// createRepo is the repository a created service publishes to: the
// registry's layout for name, or the edited one, which must sit under the
// installation registry and end in the service name, so neither a golden
// path nor a caller steers the push credential elsewhere.
func createRepo(registry, repo, name string) (string, error) {
	registry = strings.TrimSuffix(contract.TrimOCIScheme(registry), "/")
	if repo == "" {
		return registry + "/" + name, nil
	}
	repo = contract.TrimOCIScheme(repo)
	rest, under := strings.CutPrefix(repo, registry+"/")
	if !under || path.Base(rest) != name || strings.ContainsAny(rest, ":@") || path.Clean("/"+rest) != "/"+rest {
		return "", &DeclareError{Code: codeRepositoryOutsideRegistry,
			Message: fmt.Sprintf("%s is not a repository a created %s publishes to: it sits under %s and ends in %s", repo, name, registry, name)}
	}
	return repo, nil
}

// createRevision is the revision a created service publishes as: the
// default names caller and now; an edited one is 1-128 printable bytes with
// no whitespace and never another person's pacto-create: revision.
func createRevision(rev, caller string, now time.Time) (string, error) {
	prefix := createRevisionPrefix + caller + ":"
	if rev == "" {
		return prefix + now.UTC().Format(time.RFC3339), nil
	}
	bad := len(rev) > maxRevisionBytes || strings.ContainsFunc(rev, func(r rune) bool { return unicode.IsSpace(r) || !unicode.IsPrint(r) })
	if at, ok := strings.CutPrefix(rev, prefix); ok {
		_, err := time.Parse(time.RFC3339, at)
		bad = bad || err != nil
	} else if strings.HasPrefix(rev, createRevisionPrefix) {
		bad = true
	}
	if bad {
		return "", &DeclareError{Code: codeFieldNotAcceptable, Message: "the revision is 1 to 128 printable characters with no whitespace, " +
			"and a " + createRevisionPrefix + " revision names only the person creating the service"}
	}
	return rev, nil
}

// createTaken refuses a repository that holds a version, or a draft that is
// not the caller's own, with one message that says only that the name is
// taken. The caller's own drafts are the plan's.
func (pl *declarePlanner) createTaken(ctx context.Context) error {
	tags, err := pl.reg.Tags(ctx, pl.repo())
	if err != nil {
		return &DeclareError{Code: codeRegistryUnreadable, Message: "listing the versions in " + pl.repo() + ": " + err.Error()}
	}
	pl.tags = tags
	taken := len(semver.Filter(tags)) > 0
	if !taken {
		// Ownership is decided over every draft, never the listed few, and a
		// draft that does not read is unknown, never someone else's.
		drafts, _ := pl.draftsUpTo(ctx, -1)
		if slices.ContainsFunc(drafts, func(d DraftView) bool { return d.State == draftUnreadable }) {
			return &DeclareError{Code: codeRegistryUnreadable, Message: "a draft in " + pl.repo() + " could not be read, so Pacto cannot tell whether the name is yours: try again"}
		}
		caller := pl.req.Create.Caller
		own := slices.DeleteFunc(drafts, func(d DraftView) bool { return caller == "" || d.OnBehalfOf != caller && d.Asker.Subject != caller })
		taken = len(own) != len(drafts)
		pl.plan.Drafts, pl.plan.DraftsTotal = own[:min(len(own), MaxListedDrafts)], len(own)
	}
	if taken {
		pl.plan.Drafts, pl.plan.DraftsTotal = nil, 0
		return &DeclareError{Code: codeServiceExists, Message: fmt.Sprintf(serviceExistsText, pl.plan.Service)}
	}
	return nil
}

// signatureStep is the golden path's signature step of an InitFrom run.
func signatureStep(steps []fleet.CreationStep) fleet.CreationStep {
	i := slices.IndexFunc(steps, func(s fleet.CreationStep) bool { return s.Step == fleet.StepSignature })
	return steps[i] // ponytail: InitFrom always records the golden path's signature step
}

// createDoc is the created pacto.yaml as a decoded document, the values a
// create edits, and the refusals collected while editing it.
type createDoc struct {
	c        CreateFrom
	doc      map[string]any
	envFiles map[string]map[string]any // environment -> configuration -> values
	refusals []CreateRefusal
	// pathConfigs is how many configurations the golden path itself
	// declares; a pick appends after them.
	pathConfigs int
	// changed is a pick applied; raised is pactoVersion raised to 2.5.
	changed, raised bool
}

// applyOfferings sets each pick's values in the created pacto.yaml (an
// offering the path does not request is appended from its consume snippet),
// writes the per-environment values into the environment files and checks
// every offering configuration, the golden path's own values included,
// against the offering's closed schema. Every refusal is collected; any
// refuses the plan with nothing written. It returns the values file InitCLI
// reads, empty when nothing was set.
func (pl *declarePlanner) applyOfferings(ctx context.Context) (string, error) {
	file := filepath.Join(pl.dir, DefaultContractPath)
	raw, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	d := &createDoc{c: *pl.req.Create, envFiles: map[string]map[string]any{}}
	_ = contract.DecodeYAML(raw, &d.doc) // ponytail: InitFrom wrote and validated it
	if err := d.readEnvFiles(os.DirFS(pl.dir)); err != nil {
		return "", err
	}
	d.pathConfigs = len(d.configurations())
	picks := slices.Clone(d.c.Offerings)
	slices.SortFunc(picks, func(a, b OfferingPick) int { return strings.Compare(a.Offering, b.Offering) })
	for _, p := range picks {
		d.pick(p)
	}
	d.check(pl.resolveRefs(ctx, d), pl.req.createInit(false))
	if len(d.refusals) > 0 {
		return "", refusedCreate(d.refusals)
	}
	// The record a declaration carries needs 2.5, which keeps every older
	// field.
	if v, _ := d.doc["pactoVersion"].(string); validation.InferredRecordNeedsRaise(v) {
		d.doc["pactoVersion"], d.raised = "2.5", true
	}
	if !d.changed && !d.raised {
		return "", nil
	}
	if err := d.write(pl.dir, pl.s.env().writeFile); err != nil || !d.changed {
		return "", err
	}
	return d.valuesFile(), nil
}

// refusedCreate is the one refusal that carries every refused input.
func refusedCreate(refusals []CreateRefusal) error {
	msgs := make([]string, len(refusals))
	for i, r := range refusals {
		msgs[i] = r.Field + ": " + r.Message
	}
	return &DeclareError{Code: refusals[0].Code, Message: strings.Join(msgs, "; "), Refusals: refusals}
}

// resolveRefs resolves every configuration ref of the edited document to
// the revision and configuration it selects, as the contract validator does.
// A golden path with a ref was already resolved by InitFrom, which needs the
// bundle store to.
func (pl *declarePlanner) resolveRefs(ctx context.Context, d *createDoc) validation.ConfigRefs {
	c, _ := contract.Parse(strings.NewReader(pactify.MarshalContract(d.doc))) // ponytail: the document InitFrom wrote and validated, edited only in its values
	refs, _ := validation.ResolveConfigRefs(ctx, c, os.DirFS(pl.dir), validation.ConfigRefOptions{Resolver: pl.s.PolicyResolver(pl.dir), Now: pl.s.env().now()})
	return refs
}

// configurations is the document's configurations as maps.
func (d *createDoc) configurations() []map[string]any {
	list, _ := d.doc["configurations"].([]any)
	out := make([]map[string]any, 0, len(list))
	for _, x := range list {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// environments is the document's environments as maps.
func (d *createDoc) environments() []map[string]any {
	list, _ := d.doc["environments"].([]any)
	out := make([]map[string]any, 0, len(list))
	for _, x := range list {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// readEnvFiles reads each declared environment's values file.
func (d *createDoc) readEnvFiles(fsys fs.FS) error {
	for _, e := range d.environments() {
		name, _ := e["name"].(string)
		file, _ := e["file"].(string)
		_, values, err := contract.ReadEnvironmentValues(fsys, contract.Environment{Name: name, File: file})
		if err != nil {
			return fmt.Errorf("reading the values of environment %s: %w", name, err)
		}
		d.envFiles[name] = values
	}
	return nil
}

// configRefRepo is the repository a configuration ref names, "" for none.
func configRefRepo(cfg map[string]any) string {
	ref, _ := cfg["ref"].(string)
	repo, _, _ := contract.SplitOCIRef(contract.TrimOCIScheme(ref))
	return repo
}

// offeringOf is the consumable offering one of whose providers the
// configuration references, "" for none.
func (d *createDoc) offeringOf(cfg map[string]any) string {
	repo := configRefRepo(cfg)
	for _, name := range slices.Sorted(maps.Keys(d.c.Consumable)) {
		if d.c.Consumable[name].provides(repo) {
			return name
		}
	}
	return ""
}

// pick applies one offering's values: refused when the offering is not one
// the caller may consume, appended when the path does not request it.
func (d *createDoc) pick(p OfferingPick) {
	o, ok := d.c.Consumable[p.Offering]
	if !ok {
		d.refusals = append(d.refusals, d.notConsumable(p.Offering))
		return
	}
	cfgs := d.requesting(o)
	if len(cfgs) == 0 {
		d.appendOffering(o)
		cfgs = d.requesting(o)
	}
	if len(cfgs) == 0 && (len(p.Values) > 0 || len(p.Environments) > 0) {
		d.refusals = append(d.refusals, CreateRefusal{Code: codeParameterNotDeclared, Field: "offerings[" + p.Offering + "]",
			Message: p.Offering + " takes no configuration values"})
		return
	}
	d.changed = true
	for _, cfg := range cfgs {
		values, _ := cfg["values"].(map[string]any)
		if values == nil && len(p.Values) > 0 {
			values = map[string]any{}
			cfg["values"] = values
		}
		mergeValues(values, p.Values)
		d.pickEnvironments(cfg, p)
	}
}

// mergeValues sets every leaf of src in dst, descending into the maps both
// hold, so a nested value set leaves its siblings as the golden path sets
// them.
func mergeValues(dst, src map[string]any) {
	for k, v := range src {
		sub, ok := v.(map[string]any)
		if have, isMap := dst[k].(map[string]any); ok && isMap {
			mergeValues(have, sub)
			continue
		}
		dst[k] = v
	}
}

// requesting is the document's configurations that reference o.
func (d *createDoc) requesting(o CreateOffering) []map[string]any {
	return slices.DeleteFunc(d.configurations(), func(cfg map[string]any) bool { return !o.provides(configRefRepo(cfg)) })
}

// pickEnvironments sets p's per-environment values for cfg in the
// environment files; an environment the path does not declare is refused.
func (d *createDoc) pickEnvironments(cfg map[string]any, p OfferingPick) {
	name, _ := cfg["name"].(string)
	for _, env := range slices.Sorted(maps.Keys(p.Environments)) {
		file, ok := d.envFiles[env]
		if !ok {
			d.refusals = append(d.refusals, CreateRefusal{Code: codeEnvironmentNotDeclared, Field: "environments[" + env + "]",
				Message: "the golden path declares no environment " + env})
			continue
		}
		values, _ := file[name].(map[string]any)
		if values == nil {
			values = map[string]any{}
		}
		mergeValues(values, p.Environments[env])
		file[name] = values
	}
}

// appendOffering appends o's consume snippet to the document: its dependency
// unless one of that name is declared, and its configurations.
func (d *createDoc) appendOffering(o CreateOffering) {
	if o.Snippet == nil {
		return
	}
	e := o.Snippet.Entries
	deps, _ := d.doc["dependencies"].([]any)
	if dep := e.Dependency; dep != nil && !slices.ContainsFunc(deps, func(x any) bool { m, _ := x.(map[string]any); return m["name"] == dep.Name }) {
		d.doc["dependencies"] = append(deps, asMap(dep))
	}
	cfgs, _ := d.doc["configurations"].([]any)
	for _, c := range e.Configurations {
		cfgs = append(cfgs, asMap(c.Configuration))
	}
	if len(e.Configurations) > 0 {
		d.doc["configurations"] = cfgs
	}
}

// asMap is v as a decoded YAML document.
func asMap(v any) map[string]any {
	data, _ := yaml.Marshal(v) // ponytail: contract types always marshal
	var m map[string]any
	_ = contract.DecodeYAML(data, &m)
	return m
}

// notConsumable refuses an offering the caller may not consume, worded the
// same whether it is hidden or absent, and as unknown on a partial read.
func (d *createDoc) notConsumable(name string) CreateRefusal {
	if d.c.ConsumablePartial {
		return CreateRefusal{Code: codeOfferingUnknown, Field: "offerings[" + name + "]",
			Message: "Pacto could not confirm " + name + " is consumable: the offerings list is partial"}
	}
	return CreateRefusal{Code: codeOfferingNotConsumable, Field: "offerings[" + name + "]",
		Message: name + " is not an offering you may consume here: pacto fleet offerings --consumable lists those you may"}
}

// check judges every configuration whose ref reaches an offering against
// the schema that ref selects (the revision and provider configuration the
// contract names), its values once and per environment. A ref that reaches
// no offering, such as a shared schema library, is the contract validator's;
// one that does not read is unknown and refused.
func (d *createDoc) check(refs validation.ConfigRefs, initCLI string) {
	refuse := func(r CreateRefusal) {
		if !slices.ContainsFunc(d.refusals, func(x CreateRefusal) bool { return x.Field == r.Field }) {
			d.refusals = append(d.refusals, r)
		}
	}
	for i, cfg := range d.configurations() {
		repo := configRefRepo(cfg)
		if repo == "" {
			continue
		}
		cfgName, _ := cfg["name"].(string)
		r := refs[cfgName]
		if r.Offering == nil {
			if r.Err != nil {
				refuse(CreateRefusal{Code: codeOfferingUnknown, Field: "configurations[" + cfgName + "]",
					Message: "Pacto could not read " + repo + ", so it cannot tell whether it is an offering you may consume: try again"})
			}
			continue
		}
		name := r.Offering.Service.Offering
		o, ok := d.c.Consumable[name]
		if !ok || r.Provider == r.Offering && !o.provides(repo) {
			refuse(d.notConsumable(name))
			continue
		}
		schema, err := refSchema(r)
		if err != nil {
			refuse(CreateRefusal{Code: codeOfferingUnknown, Field: "configurations[" + cfgName + "]",
				Message: "Pacto could not read the schema configuration " + cfgName + " selects in offering " + name + ", so its values are not checked: try again"})
			continue
		}
		d.checkConfiguration(i, cfgName, name, schema, cfg, initCLI)
	}
}

// refSchema is the schema document r selects.
func refSchema(r validation.ResolvedConfigRef) (map[string]any, error) {
	if r.Err != nil {
		return nil, r.Err
	}
	data, _ := fs.ReadFile(r.ProviderFS, r.SchemaPath) // ponytail: the resolver read and compiled it
	var doc map[string]any
	_ = json.Unmarshal(data, &doc)
	return doc, nil
}

// checkConfiguration checks configuration i (named cfgName, of offering
// name) against schema and rewrites each refusal's field into its stable
// form. Only a configuration the golden path itself declares gets a pacto
// init fix: an appended one is not in the folder pacto init writes.
func (d *createDoc) checkConfiguration(i int, cfgName, name string, schema, cfg map[string]any, initCLI string) {
	values, _ := cfg["values"].(map[string]any)
	perEnv := map[string]map[string]any{}
	for env, file := range d.envFiles {
		if v, ok := file[cfgName].(map[string]any); ok {
			perEnv[env] = v
		}
	}
	if i >= d.pathConfigs {
		initCLI = ""
	}
	for _, r := range validation.CheckOfferingValues(schema, values, perEnv) {
		d.refusals = append(d.refusals, createRefusal(r, i, cfgName, name, initCLI))
	}
}

// createRefusal is a value check's refusal of configuration i (named
// cfgName, of offering) in the stable field form, with both fixes. The CLI
// fix is set only when it runs as given: a closest value for a value the
// golden path's folder holds (initCLI is "" otherwise), never a values file.
func createRefusal(r validation.OfferingValueRefusal, i int, cfgName, offering, initCLI string) CreateRefusal {
	code := cmp.Or(offeringValueCodes[r.Code], string(r.Code))
	out := CreateRefusal{Code: code, Message: r.Message, Closest: r.Closest, Allowed: r.Allowed}
	key, inValues := strings.CutPrefix(r.Field, "values.")
	env, envKey, inEnv := envField(r.Field)
	switch {
	case inValues:
		out.Field = "configurations[" + cfgName + "].values." + key
		if r.Closest != nil && initCLI != "" {
			out.CLI = initCLI + " --set " + shellquote.Quote(fmt.Sprintf("configurations[%d].values.%s=%v", i, key, r.Closest))
		}
		if r.Closest != nil {
			out.MCP = mcpPick(OfferingPick{Offering: offering, Values: nested(key, r.Closest)})
		}
	case inEnv:
		out.Field = "environments[" + env + "]." + cfgName + "." + envKey
		if r.Closest != nil {
			out.MCP = mcpPick(OfferingPick{Offering: offering, Environments: map[string]map[string]any{env: nested(envKey, r.Closest)}})
		}
	default:
		out.Field = "configurations[" + cfgName + "]"
	}
	return out
}

// envField splits environments[<env>].values.<key>.
func envField(field string) (env, key string, ok bool) {
	rest, ok := strings.CutPrefix(field, "environments[")
	if !ok {
		return "", "", false
	}
	env, key, ok = strings.Cut(rest, "].values.")
	return env, key, ok
}

// nested is v at the dotted key as nested maps.
func nested(dotted string, v any) map[string]any {
	parts := strings.Split(dotted, ".")
	out := map[string]any{parts[len(parts)-1]: v}
	for i := len(parts) - 2; i >= 0; i-- {
		out = map[string]any{parts[i]: out}
	}
	return out
}

// mcpPick is the pacto_create_service argument fragment that sets p.
func mcpPick(p OfferingPick) string {
	data, _ := json.Marshal(map[string]any{"offerings": []OfferingPick{p}}) // ponytail: decoded values always marshal
	return string(data)
}

// write writes the edited pacto.yaml and the environment files; an
// environment with no file (pactoVersion 2.6) gets <env>.values.yaml.
func (d *createDoc) write(dir string, write func(string, []byte, os.FileMode) error) error {
	for _, e := range d.environments() {
		name, _ := e["name"].(string)
		if len(d.envFiles[name]) == 0 {
			continue
		}
		file, _ := e["file"].(string)
		if file == "" {
			file = name + ".values.yaml"
			e["file"] = file
		}
		data, _ := yaml.Marshal(d.envFiles[name]) // ponytail: decoded values always marshal
		if err := write(filepath.Join(dir, file), data, 0o644); err != nil {
			return err
		}
	}
	return write(filepath.Join(dir, DefaultContractPath), []byte(pactify.MarshalContract(d.doc)), 0o644)
}

// valuesFile is the text of CreateValuesFile: the planned dependencies and
// configurations.
func (d *createDoc) valuesFile() string {
	m := map[string]any{}
	for _, k := range []string{"dependencies", "configurations"} {
		if v, ok := d.doc[k]; ok {
			m[k] = v
		}
	}
	data, _ := yaml.Marshal(m) // ponytail: decoded values always marshal
	return string(data)
}

// createIntent binds every input of a create: the path, owner, namespace,
// repository, revision and the picks in offering order.
// A pacto-create: revision is the caller's own default (createRevision
// refuses anyone else's) and is bound as the default: its time is the plan's
// clock, so a write in a later second, or one that sends no revision back,
// keeps the plan it reviewed.
func createIntent(c CreateFrom) string {
	picks := slices.Clone(c.Offerings)
	slices.SortFunc(picks, func(a, b OfferingPick) int { return strings.Compare(a.Offering, b.Offering) })
	rev := c.Revision
	if strings.HasPrefix(rev, createRevisionPrefix) {
		rev = ""
	}
	data, _ := json.Marshal(struct {
		Path, Owner, Namespace, Repository, Revision string
		Offerings                                    []OfferingPick
	}{c.Path, c.Owner, c.Namespace, c.Repository, rev, picks}) // ponytail: decoded values always marshal
	return string(data)
}

// createCommand is the pacto init that recreates req's folder locally,
// every token quoted: name, path, owner, the registry the repository sits
// in and, when values were set, the values file.
func (req DeclareRequest) createCommand() string {
	return req.createInit(len(req.Create.Offerings) > 0)
}

// createInit is createCommand, naming the values file only when values.
func (req DeclareRequest) createInit(values bool) string {
	c := req.Create
	repo := cmp.Or(c.Repository, strings.TrimSuffix(req.Registry, "/")+"/"+req.Service)
	parts := []string{"pacto", "init", shellquote.Quote(req.Service), "--from", shellquote.Quote(c.Path), "--owner", shellquote.Quote(c.Owner),
		"--registry", shellquote.Quote(path.Dir(repo))}
	if values {
		parts = append(parts, "--values", CreateValuesFile)
	}
	return strings.Join(parts, " ")
}

// createdHandover is the handover of a published create: the repository
// snippet the person runs with their own token, the pull of the bundle and
// the index entry, written through the scoped opener and never undoing the
// publish.
func (s *Service) createdHandover(ctx context.Context, req DeclareRequest, res *DeclareResult, d *declaration) *CreatedService {
	c := req.Create
	registry := strings.TrimSuffix(contract.TrimOCIScheme(req.Registry), "/")
	index := oci.InstallationRepository(registry, oci.IndexRepository)
	open := c.OpenIndex
	if open == nil {
		open = s.env().refusalRepo
	}
	_, digest, _ := strings.Cut(res.Published, "@")
	note := s.keepIndexEntryWith(ctx, open, d.bundle, d.repo+":"+res.Tag, digest,
		orgindex.Publisher{Identity: res.Plan.Signer.Subject, Issuer: res.Plan.Signer.Issuer}, index)
	return &CreatedService{
		GitCLI:  "gh repo create <org>/" + shellquote.Quote(req.Service) + " --private --clone && cd " + shellquote.Quote(req.Service) + " && " + res.Plan.Create.InitCLI,
		PullCLI: "pacto pull " + shellquote.Quote("oci://"+res.Published),
		Index:   note,
		Indexed: strings.HasPrefix(note, "indexed as "),
	}
}

// createOwner plans a created service's first owner (Decision 15): the
// person who signs must belong to the owner team or be a default approver,
// decided again with the login's identity before anything is pushed.
func (pl *declarePlanner) createOwner(ctx context.Context) []byte {
	proposed := delivery.OwnerClaim{Owner: pl.folder.Service.Owner, Provenance: fleet.OwnerProvenanceDeclared}
	policy, from := pl.ownerPolicy(ctx)
	right := delivery.DecideFirstOwner(pl.plan.Service, pl.choice.Identity, proposed, policy, from, pl.s.env().now())
	pl.plan.Owner, pl.plan.MayAccept = proposed.Owner.KeyString(), right.MayAccept
	pl.owned = &ownerCheck{proposed: proposed, policy: policy, from: from, first: true}
	if policy == nil {
		pl.plan.Notes = append(pl.plan.Notes, "service.owner is "+ownerText(proposed.Owner)+": nobody can be confirmed to create it until a verified owner policy is read, "+
			"since team membership and the default approvers come from it")
		return pl.raw
	}
	who := cmp.Or(strings.Join(right.MayAccept, ", "), "the default approvers")
	pl.plan.Notes = append(pl.plan.Notes, "service.owner is "+ownerText(proposed.Owner)+": only a person among "+who+" may create it")
	return pl.raw
}

// allFiles is every file of a published bundle, the person's handover.
func allFiles(files map[string]*fstest.MapFile) map[string][]byte {
	out := make(map[string][]byte, len(files))
	for name, f := range files {
		out[name] = f.Data
	}
	return out
}

// AcceptURL is the dashboard page a person reviews and signs the created
// draft tag of repo at, "" when no dashboard serves the review. The
// repository is named only when it is not the registry's default.
func (req DeclareRequest) AcceptURL(repo, tag string) string {
	if req.Create.ReviewURL == "" {
		return ""
	}
	q := url.Values{"create": {req.Service}, "draft": {tag}}
	if def, _ := createRepo(req.Registry, "", req.Service); repo != def {
		q.Set("repository", repo)
	}
	return req.Create.ReviewURL + "?" + q.Encode()
}

// createdDraft says rec and c are a created draft: no cluster input,
// nothing inferred from and a digest-pinned golden path origin. A cluster
// draft keeps its cluster input, so an origin never makes it a created one.
func createdDraft(rec *contract.InferredRecord, c *contract.Contract) bool {
	return rec.Inputs.Cluster == nil && rec.Declaration.InferredFrom == "" && c.Service.Origin != nil && pinnedPath(c.Service.Origin.Ref)
}

// createdDraftSubject checks a draft against a create (ok is true when it
// decided): a create accepts only a created draft of its service, and any
// other run refuses a created draft, which has no fleet view to be
// re-checked against and is signed in the dashboard.
func createdDraftSubject(repo string, req DeclareRequest, dr *draftRead, rec *contract.InferredRecord) (bool, error) {
	created := createdDraft(rec, dr.bundle.Contract)
	switch {
	case req.Create != nil && created && dr.bundle.Contract.Service.Name == req.Service:
		return true, nil
	case req.Create != nil:
		return true, &DeclareError{Code: codeDraftNotFound, Message: repo + ":" + req.FromDraft + " holds no created draft of " + req.Service}
	case created:
		return true, &DeclareError{Code: codeDraftNotFound, Message: fmt.Sprintf(createdDraftCLI, repo, req.FromDraft)}
	}
	return false, nil
}

// fromCreatedDraft plans a person accepting an agent's created draft
// (Decision 9). The draft's files are never trusted: the create inputs are
// read from its own contract (owner, the offering values and the values of
// each environment), the golden path is run again from them against the
// person's own view, consumable read and owner rule, and every regenerated
// file but the record must hold what the draft holds. The draft's golden
// path must be the one the caller resolved.
func (s *Service) fromCreatedDraft(ctx context.Context, req DeclareRequest) (*DeclarePlan, *declaration, error) {
	repo, err := createRepo(req.Registry, req.Create.Repository, req.Service)
	if err != nil {
		return nil, nil, err
	}
	dr, err := readDraft(ctx, s.store(req), repo, req)
	if err != nil {
		return nil, nil, err
	}
	c := dr.bundle.Contract
	if c.Service.Origin.Ref != req.Create.Path {
		return nil, nil, &DeclareError{Code: codeGoldenPathNotDeclared,
			Message: repo + ":" + req.FromDraft + " was created from another golden path than the one you create from here"}
	}
	picks, err := draftPicks(dr.bundle, req.Create.Consumable)
	if err != nil {
		return nil, nil, &DeclareError{Code: codeDraftNotFound, Message: fmt.Sprintf(createdDraftExtra, repo, req.FromDraft)}
	}
	create := *req.Create
	create.Owner, create.Repository, create.Offerings = c.Service.Owner.Team, repo, picks
	sub := req
	sub.Create, sub.FromDraft = &create, ""
	plan, d, err := s.PlanDeclare(ctx, sub)
	switch {
	case err != nil:
		return plan, d, err
	case plan.State != contract.DeclarationDeclared:
		return plan, nil, &DeclareError{Code: codeSignerNotAllowed, Message: "accepting a draft takes a person's Sigstore login: " + plan.Signer.Why}
	case !sameFiles(d.files, dr.bundle.FS):
		return plan, nil, &DeclareError{Code: codeDraftNotFound, Message: fmt.Sprintf(createdDraftExtra, repo, req.FromDraft)}
	}
	if i := slices.Index(plan.Notes, fmt.Sprintf(createdNote, create.Path)); i >= 0 {
		plan.Notes[i] = fmt.Sprintf(createdDraftNote, create.Path)
	}
	plan.Notes = append(plan.Notes, dr.signature)
	// The record is packed again with the person as asker before it is signed.
	dec := d.record.Declaration
	dec.Draft = &contract.InferredDraft{Tag: req.FromDraft, Digest: repo + "@" + dr.digest, Asker: dr.dec.Asker, OnBehalfOf: dr.dec.OnBehalfOf}
	plan.IntentDigest = intentDigest(plan, d.raw, dec.Accepted, dec.Draft.Digest)
	return plan, d, nil
}

// draftPicks is the offering values a created draft sets, read from its
// pacto.yaml and environment files: per offering its configurations' values
// and each environment's values for them.
func draftPicks(b *contract.Bundle, consumable map[string]CreateOffering) ([]OfferingPick, error) {
	d := &createDoc{c: CreateFrom{Consumable: consumable}, envFiles: map[string]map[string]any{}}
	_ = contract.DecodeYAML(b.RawYAML, &d.doc) // ponytail: the pulled bundle parsed
	if err := d.readEnvFiles(b.FS); err != nil {
		return nil, err
	}
	byName := map[string]*OfferingPick{}
	for _, cfg := range d.configurations() {
		values, _ := cfg["values"].(map[string]any)
		cfgName, _ := cfg["name"].(string)
		envs := map[string]map[string]any{}
		for env, file := range d.envFiles {
			if v, ok := file[cfgName].(map[string]any); ok {
				envs[env] = v
			}
		}
		// Every configuration of a consumable offering is a pick, a pick with
		// no values included, so an offering the agent added with nothing set
		// is added again. Any other ref is the golden path's own, or content
		// it never had, which the regenerated folder leaves out.
		name := d.offeringOf(cfg)
		if name == "" {
			continue
		}
		p := byName[name]
		if p == nil {
			p = &OfferingPick{Offering: name, Values: map[string]any{}, Environments: map[string]map[string]any{}}
			byName[name] = p
		}
		maps.Copy(p.Values, values)
		for env, v := range envs {
			if p.Environments[env] == nil {
				p.Environments[env] = map[string]any{}
			}
			maps.Copy(p.Environments[env], v)
		}
	}
	out := []OfferingPick{}
	for _, name := range slices.Sorted(maps.Keys(byName)) {
		out = append(out, *byName[name])
	}
	return out, nil
}

// sameFiles says a and b hold the same files but the inferred record, each
// with the same bytes or, for YAML and JSON, the same document.
func sameFiles(a, b fs.FS) bool {
	fa, fb := treeFiles(a), treeFiles(b)
	if len(fa) != len(fb) {
		return false
	}
	for name, x := range fa {
		y, ok := fb[name]
		if !ok || !sameDocument(x, y) {
			return false
		}
	}
	return true
}

// SameDraftFiles says files, what a plan would publish, and the pulled
// draft fsys hold the same files but the inferred record.
func SameDraftFiles(files map[string][]byte, fsys fs.FS) bool {
	m := fstest.MapFS{}
	for name, data := range files {
		m[name] = &fstest.MapFile{Data: data}
	}
	return sameFiles(m, fsys)
}

// treeFiles is every file of fsys but the inferred record.
func treeFiles(fsys fs.FS) map[string][]byte {
	out := map[string][]byte{}
	_ = fs.WalkDir(fsys, ".", func(name string, de fs.DirEntry, err error) error {
		if err == nil && !de.IsDir() && name != contract.InferredRecordFile {
			out[name], err = fs.ReadFile(fsys, name)
		}
		return err
	})
	return out
}

// sameDocument says x and y are the same bytes, or decode to the same YAML
// document (a re-marshalled pacto.yaml or values file).
func sameDocument(x, y []byte) bool {
	if string(x) == string(y) {
		return true
	}
	var dx, dy any
	return contract.DecodeYAML(x, &dx) == nil && contract.DecodeYAML(y, &dy) == nil && reflect.DeepEqual(dx, dy)
}
