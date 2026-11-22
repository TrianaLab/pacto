package app

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/trianalab/pacto/v4/internal/ci"
	"github.com/trianalab/pacto/v4/internal/discover"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/diff"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/graph"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/lock"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/semver"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// CIOptions configure one pacto ci plan.
type CIOptions struct {
	Root     string
	Registry string // --registry; empty lets the CI context and images decide
	Fleet    FleetOptions
	// Getenv reads the CI context; nil is an empty environment, never
	// os.Getenv, so only the CLI hands the runner's variables in.
	Getenv func(string) string
	// OnResolved, when set, is asked for a hook per impacted service and
	// the hook gets the baseline and the planned local bundle.
	OnResolved func(service string) func(old, new *contract.Bundle)
	// Bundles, when non-nil, gets each read folder's judged bundle by its
	// row's Dir, so --explain describes the bundle the plan judged.
	Bundles map[string]*contract.Bundle
	// revision is the commit the inferred records name; empty reads each
	// folder's git commit. --publish sets it, so the bundles a plan judges
	// are the bundles it publishes.
	revision string
	// migrations, when non-nil, gets each planned folder's migration by its
	// row's Dir.
	migrations map[string]CIMigration
	// proposed, when non-nil, gets the Dir of each row whose bundle holds a
	// proposal or a carried acceptance, so publish pushes the bundle the
	// plan judged.
	proposed map[string]bool
	// carriedFrom, when non-nil, gets each row's carry baseline by its Dir:
	// the release plan mode carried registry decisions from, which publish
	// checks against the baseline it measures the version from.
	carriedFrom map[string]carryBase
	// TargetSnapshotAuto reads the target snapshot beside the registry the
	// plan reads (D8) when Fleet names no target input, as pacto impact does.
	TargetSnapshotAuto bool
	// Signers verifies the acceptances of announced breaks in place of the
	// installation signer policy; nil reads that.
	Signers *delivery.ClassPolicy
	// Policy is the range policy each row's BREAKING answer is held to.
	Policy impact.Policy
}

// firstVersion is the version an undeclared service.version starts at.
const firstVersion = "1.0.0"

// CIPlan plans every service folder under opts.Root: the CI context, the
// registry with its source and one row per folder, judged against the
// folder's baseline published revision. It reads the repository and the one
// registry host it trusts, and writes nothing. The results map holds each
// impacted row's answer by service.
func (s *Service) CIPlan(ctx context.Context, opts CIOptions) (*ci.Plan, map[string]*impact.Result, error) {
	registry, err := ci.NormalizeRegistry(opts.Registry)
	if err != nil {
		return nil, nil, err
	}
	cctx := ci.Detect(opts.Getenv)
	p := &ci.Plan{Platform: cctx.Platform, Base: cctx.Base, OIDC: cctx.OIDC}
	walk, err := discover.Walk(ctx, opts.Root, func(dir string, err error) error {
		if dir == opts.Root {
			return err
		}
		p.Notes = append(p.Notes, "unreadable, skipped: "+err.Error())
		p.Unreadable++
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	cat, lims, err := s.externalCatalog(ctx, opts.Fleet)
	if err != nil {
		return nil, nil, err
	}
	for _, l := range lims {
		p.Notes = append(p.Notes, l.Message)
	}
	for _, sk := range walk.Skipped {
		p.Skipped = append(p.Skipped, ci.Skip{Rel: sk.Rel, Rule: sk.Rule})
	}
	folders, images := s.ciFolders(opts.Root, walk.Dirs, opts.revision, cat)
	s.ciRegistry(ctx, p, registry, cctx, opts.Fleet.Detected, images)
	p.Notes = append(p.Notes, blockCollisions(folders)...)
	pl := &ciPlanner{s: s, p: p, reg: s.env().publishRegistry(), allowed: allowedHost(p.Registry), root: opts.Root, revision: opts.revision, cat: cat,
		acceptances: sync.OnceValue(func() *acceptanceStore {
			return s.ownerRuled(newAcceptanceStore(ctx, s.env(), impactTrust(ImpactOptions{Signers: opts.Signers, Fleet: opts.Fleet})), opts.Fleet.Provenance)
		}),
		decl: sync.OnceValue(func() *declarationTrust {
			return declarationTrustFrom(ctx, impactTrust(ImpactOptions{Signers: opts.Signers, Fleet: opts.Fleet}))
		}),
		ownerPolicy: ownerPolicyOnce(ctx, s)}
	pl.planAll(ctx, folders, opts)
	results, snap, err := s.ciImpacts(ctx, p, folders, opts, pl.allowed)
	if err != nil {
		return nil, nil, err
	}
	for _, f := range folders {
		pl.propose(ctx, f, snap, opts)
		f.row.Notes = append(f.row.Notes, breakNotes(f.judged, results[f.row.Service])...)
		p.Rows = append(p.Rows, f.row)
	}
	p.Ranges = ciRanges(folders, opts.Root)
	p.Settle(opts.Root)
	return p, results, nil
}

// planAll plans each folder and records what opts asks to be handed back.
func (pl *ciPlanner) planAll(ctx context.Context, folders []*ciFolder, opts CIOptions) {
	for _, f := range folders {
		pl.plan(ctx, f)
		f.judged = f.b
		if f.carried && opts.proposed != nil {
			opts.proposed[f.row.Dir] = true
		}
		if opts.Bundles != nil && f.b != nil {
			opts.Bundles[f.row.Dir] = f.b
		}
		if opts.migrations != nil {
			opts.migrations[f.row.Dir] = f.migration
		}
		if opts.carriedFrom != nil {
			opts.carriedFrom[f.row.Dir] = f.carriedFrom
		}
	}
}

// ciRegistry sets the plan's registry with its source: --registry, else the
// installation advertisement in detected, else what ci.ResolveRegistry finds.
func (s *Service) ciRegistry(ctx context.Context, p *ci.Plan, registry string, cctx ci.Context, detected string, images []ci.ImageRef) {
	var advertised ci.Value
	if registry == "" {
		// --registry wins: the advertisement is not read at all.
		var note string
		advertised, note = s.ciAdvertised(ctx, detected)
		if note != "" {
			p.Notes = append(p.Notes, note)
		}
	}
	var notes []string
	p.Registry, notes = ci.ResolveRegistry(registry, advertised, cctx, images)
	p.Notes = append(p.Notes, notes...)
}

// ciFolder is one service folder being planned.
type ciFolder struct {
	dir string
	row ci.Row
	b   *contract.Bundle // nil when the folder could not be read
	// asWritten is b before pacto ci raised its pactoVersion in memory, and
	// migration what it did; b itself when it did not.
	asWritten *contract.Bundle
	migration CIMigration
	// base is the baseline a changed row's impact runs against; nil when an
	// earlier rule already decided the row.
	base *contract.Bundle
	// judged is the bundle the plan judged and publish pushes: b before
	// impact replaced it with the planned revision.
	judged *contract.Bundle
	// carried is set when b holds acceptances carried from the baseline.
	carried bool
	// carriedFrom is the release registry decisions were carried from
	// (its b is nil): the baseline, or the newest final release below a
	// candidate baseline, the one publish measures a version from.
	carriedFrom carryBase
}

// decide sets the row's impact unless an earlier rule already did.
func (f *ciFolder) decide(gate, text string) {
	if f.row.Impact.Gate == "" {
		f.row.Impact = ci.Impact{Gate: gate, Text: gate + " (" + text + ")"}
	}
}

// block makes the row blocked whatever an earlier rule said, unless it
// already is.
func (f *ciFolder) block(text string) {
	if f.row.Impact.Gate != impact.GateBlocked {
		f.row.Impact = ci.Impact{}
		f.decide(impact.GateBlocked, text)
	}
}

// ciFolders reads every folder and the images their inferred records name.
// A folder that cannot be read stays a blocked row, never a dropped service.
func (s *Service) ciFolders(root string, dirs []string, revision string, cat *externalcatalog.Catalog) ([]*ciFolder, []ci.ImageRef) {
	var out []*ciFolder
	var images []ci.ImageRef
	for _, dir := range dirs {
		f := &ciFolder{dir: dir, row: ci.Row{Dir: relSlash(root, dir)}}
		out = append(out, f)
		b, err := s.ciBundle(dir, root, revision, cat)
		if err != nil {
			f.row.Errors = append(f.row.Errors, err.Error())
		}
		if b == nil {
			f.decide(impact.GateBlocked, "cannot read the contract")
			continue
		}
		if err != nil {
			f.decide(impact.GateUnverified, "inferred values unknown")
		}
		f.b, f.row.Service = b, b.Contract.Service.Name
		images = append(images, f.recordImages()...)
	}
	return out, images
}

// ciBundle is the folder dir read and completed with the ci root as its
// repository root, its record naming revision: the bundle a plan judges,
// before any in-memory migration.
func (s *Service) ciBundle(dir, root, revision string, cat *externalcatalog.Catalog) (*contract.Bundle, error) {
	b, err := readLocalBundle(dir)
	if err != nil {
		return nil, err
	}
	return b, completeLocal(b, dir, dirBase(dir), root, s.localInputs(dir, revision), cat)
}

// recordImages counts the row's inferred fields and returns the images they
// name, with folder-qualified sources.
func (f *ciFolder) recordImages() []ci.ImageRef {
	rec, _ := contract.ReadInferredRecord(f.b.FS) // ponytail: the record was just written by completion
	f.row.Inferred = 0
	if rec == nil {
		return nil
	}
	f.row.Inferred = len(rec.Fields)
	var images []ci.ImageRef
	for _, fl := range rec.Fields {
		if fl.Field == "image" {
			images = append(images, ci.ImageRef{Image: fl.Value, Source: path.Join(f.row.Dir, fl.Source)})
		}
	}
	return images
}

// blockCollisions blocks every folder whose service name another folder
// claims: an inferred claim through contract.NameCollisions, two declared
// ones since they would publish to one tag. It returns the error lines.
func blockCollisions(folders []*ciFolder) []string {
	byDir := map[string]*ciFolder{}
	var claims []contract.NameClaim
	for _, f := range folders {
		if f.b == nil || f.row.Service == "" {
			continue
		}
		inf, inferred := f.b.NameInferred()
		byDir[f.row.Dir] = f
		claims = append(claims, contract.NameClaim{Dir: f.row.Dir, Name: f.row.Service, Source: inf.Source, Inferred: inferred})
	}
	var errs []string
	for _, c := range contract.NameCollisions(claims) {
		var dirs []string
		source := ""
		for _, cl := range c.Claims {
			dirs = append(dirs, cl.Dir)
			if cl.Inferred && source == "" {
				source = cl.Source
			}
		}
		errs = append(errs, fmt.Sprintf("error: %s both infer service.name '%s' (%s). Declare service.name in one of them.", strings.Join(dirs, " and "), c.Name, source))
		for _, cl := range c.Claims {
			others := slices.DeleteFunc(slices.Clone(dirs), func(d string) bool { return d == cl.Dir })
			byDir[cl.Dir].block("name collision with " + strings.Join(others, ", "))
		}
	}
	declared := map[string][]string{}
	for _, cl := range claims {
		if !cl.Inferred {
			declared[cl.Name] = append(declared[cl.Name], cl.Dir)
		}
	}
	for name, dirs := range declared {
		if len(dirs) < 2 {
			continue
		}
		for _, d := range dirs {
			byDir[d].block("two folders declare service.name '" + name + "'")
		}
	}
	return errs
}

// allowedHost is the one registry host a plan contacts: the host of a
// registry the caller or the CI platform named, never one the repository
// content names (credentials never follow repository content).
func allowedHost(reg ci.Value) string {
	switch {
	case reg.Source == "--registry", reg.Source == "GITHUB_REPOSITORY_OWNER", reg.Source == "CI_REGISTRY_IMAGE",
		strings.HasPrefix(reg.Source, advertisedSource):
		return hostOf(reg.Value)
	}
	return ""
}

// advertisedSource starts the source of a registry read from an available
// installation advertisement, the only advertised registry the plan may
// contact.
const advertisedSource = "the installation advertised at "

// ciAdvertised is the registry the installation advertisement at ref names
// (--detected), with a note when one was named but cannot be used: only an
// available detection (fresh and, for k8s:, written by the operator) with an
// installation block counts, so a stale or unreadable one never picks the
// registry or the host the plan contacts. Neither does one read while
// PACTO_REGISTRY_* credentials are set: they answer any host, and a detection
// a ConfigMap writer can forge must not choose where they are sent.
func (s *Service) ciAdvertised(ctx context.Context, ref string) (ci.Value, string) {
	src := s.detectionReader(ref)
	if src == nil {
		return ci.Value{}, ""
	}
	res, st := src.Read(ctx)
	status := string(st.Status)
	if st.Status == fleet.SourceAvailable && res.Installation == nil {
		status = "absent"
	}
	if status != string(fleet.SourceAvailable) {
		return ci.Value{}, "installation advertisement " + status + ": not used"
	}
	reg, err := ci.NormalizeRegistry(res.Installation.Registry)
	if err != nil || reg == "" {
		return ci.Value{}, "installation advertisement invalid: not used"
	}
	if env := oci.EnvCredentialOptions(); env.Token != "" || (env.Username != "" && env.Password != "") {
		// The detection is not signed, and PACTO_REGISTRY_* credentials
		// answer any host: the advertisement never picks where they go.
		return ci.Value{}, "installation advertises " + reg + ", not contacted: PACTO_REGISTRY_* credentials answer any host; pass --registry " + reg
	}
	return ci.Value{Value: reg, Source: advertisedSource + ref}, ""
}

// hostOf is a repository reference's registry host.
func hostOf(ref string) string {
	host, _, _ := strings.Cut(ref, "/")
	return host
}

// ciPlanner judges each folder against the registry.
type ciPlanner struct {
	s       *Service
	p       *ci.Plan
	reg     publish.Registry
	allowed string // the one host the plan reads from; empty reads none
	// root, revision and cat complete a migrated bundle the way ciBundle
	// completed the folder.
	root, revision string
	cat            *externalcatalog.Catalog
	// acceptances reads the acceptances a baseline carries, built once.
	acceptances func() *acceptanceStore
	// decl judges registry declarations, built once per run.
	decl func() *declarationTrust
	// ownerPolicy is the owner policy the carried owners of a bundle
	// repository are judged with (ownerPolicyOnce).
	ownerPolicy func(repo string) (*delivery.OwnerPolicy, string)
}

// ownerPolicyOnce reads the owner policy beside a bundle repository on
// first need, once per run for every repository under the same parent: the
// target snapshots that carry it are looked up beside the repository, never
// in it (snapshotRoots).
func ownerPolicyOnce(ctx context.Context, s *Service) func(repo string) (*delivery.OwnerPolicy, string) {
	type read struct {
		policy *delivery.OwnerPolicy
		from   string
	}
	got := map[string]read{}
	return func(repo string) (*delivery.OwnerPolicy, string) {
		parent := path.Dir(repo)
		r, ok := got[parent]
		if !ok {
			r.policy, r.from = s.OwnerPolicy(ctx, repo, "")
			got[parent] = r
		}
		return r.policy, r.from
	}
}

// registryName is the plan's registry as messages name it.
func (pl *ciPlanner) registryName() string { return cmp.Or(pl.p.Registry.Value, "none") }

// plan fills one folder's row, first rule that applies: validation, the
// repository, the baseline, changed, the version rule. A valid folder is
// raised to the current pactoVersion in memory first, so every rule after
// validation, and the publish, reads the bundle it would publish.
func (pl *ciPlanner) plan(ctx context.Context, f *ciFolder) {
	f.asWritten = f.b
	if f.row.Impact.Gate != "" || !pl.validate(ctx, f) {
		return
	}
	fence := &hostFence{inner: &bundleResolverAdapter{svc: pl.s, base: rootBase(f.dir)}, host: pl.allowed}
	f.b, f.migration = pl.s.migrateForCI(ctx, f.b, f.dir, pl.root, fence, pl.s.localInputs(f.dir, pl.revision), pl.cat)
	// The raised bundle's record is the one judged and published: count it.
	// ponytail: its images miss registry inference, resolved before the plan.
	f.recordImages()
	if repo, declared, ok := pl.repository(f); ok {
		f.row.Repository = repo
		pl.judge(ctx, f, repo, declared)
	}
	if n := migrationNote(f); n != "" {
		f.row.Notes = append(f.row.Notes, n)
	}
}

// migratedNote ends the note of a contract raised in memory; publish mode
// turns its "publishes as" into "published as" once the bundle is pushed.
const migratedNote = " (migrated in memory)"

// migrationNote is the plan line of a behind contract: kept and why, raised
// in memory, or unchanged against a baseline an earlier CLI published as
// written, which raises only when the service next changes. A current
// contract has none.
func migrationNote(f *ciFolder) string {
	m := f.migration
	switch {
	case m.State == migrationKept:
		return fmt.Sprintf("pacto.yaml is %s, kept at %s (%s)", m.From, m.From, m.Reason)
	case m.State != migrationMigrated:
		return ""
	case f.b == f.asWritten:
		return fmt.Sprintf("pacto.yaml is %s, published as %s (raises to %s when it next changes)", m.From, m.From, m.To)
	case f.row.Changed == "no":
		return fmt.Sprintf("pacto.yaml is %s, published as %s%s", m.From, m.To, migratedNote)
	case f.row.Changed == "":
		// No baseline was read: it may hold the as-written bundle, which keeps m.From.
		return fmt.Sprintf("pacto.yaml is %s, would publish as %s if it changed (baseline not read)", m.From, m.To)
	}
	return fmt.Sprintf("pacto.yaml is %s, publishes as %s%s", m.From, m.To, migratedNote)
}

// validate validates the folder with policy and configuration refs fenced
// to the allowed host. A refused ref, of either kind, leaves the row
// unverified, never invalid: what it would have said is unknown.
func (pl *ciPlanner) validate(ctx context.Context, f *ciFolder) bool {
	fence := &hostFence{inner: &bundleResolverAdapter{svc: pl.s, base: rootBase(f.dir)}, host: pl.allowed}
	res := validation.ValidateWithResolver(ctx, f.b.Contract, f.b.RawYAML, f.b.FS, fence)
	if len(fence.refused) > 0 {
		res = validation.Validate(f.b.Contract, f.b.RawYAML, f.b.FS)
	}
	for _, e := range res.Errors {
		f.row.Errors = append(f.row.Errors, strings.TrimPrefix(e.Path+": ", ": ")+e.Message)
	}
	switch {
	case !res.IsValid():
		f.decide(impact.GateBlocked, fmt.Sprintf("invalid: %d errors", len(res.Errors)))
	case len(fence.refused) > 0:
		f.decide(impact.GateUnverified, "ref on "+fence.refused[0]+" not fetched: outside the registry "+pl.registryName())
	}
	return f.row.Impact.Gate == ""
}

// repository is the folder's bundle repository and whether a declared
// source names it: publish.bundle when declared, the repository a derived
// section publishes to when none is, else <registry>/<name>.
// It makes no request; a repository outside the allowed host is not read.
func (pl *ciPlanner) repository(f *ciFolder) (string, bool, bool) {
	reg := pl.p.Registry
	switch {
	case reg.Value == "":
		f.decide(impact.GateUnverified, "no registry")
		return "", false, false
	case pl.allowed == "":
		f.decide(impact.GateUnverified, "registry inferred from images; pass --registry")
		return "", false, false
	}
	repo, declared := reg.Value+"/"+f.row.Service, reg.Source != "GITHUB_REPOSITORY_OWNER"
	pub := f.b.Contract.Publish
	if pub == nil {
		repo = derivedBundleRepo(f.dir, reg.Value, f.row.Service)
	}
	if pub != nil && pub.Bundle != "" {
		repo, declared = publish.Repo(pub.Bundle), true
		if h := hostOf(repo); h != pl.allowed {
			f.decide(impact.GateUnverified, "publish.bundle names "+h+", outside the registry "+reg.Value+"; pass --registry "+h+" to trust it")
			return "", false, false
		}
	}
	return repo, declared, true
}

// judge reads the folder's baseline and fills CHANGED, VERSION and
// CLASSIFICATION. Every bundle is read by the digest its tag pointed at
// once, so the revision judged is the revision printed.
func (pl *ciPlanner) judge(ctx context.Context, f *ciFolder, repo string, declaredRepo bool) {
	tags, err := pl.reg.Tags(ctx, repo)
	if err != nil {
		f.decide(impact.GateUnverified, "baseline unreadable: "+err.Error())
		return
	}
	versions := semver.Filter(tags)
	if len(versions) == 0 {
		firstPublish(f, repo, declaredRepo, pl.p.Registry.Source)
		return
	}
	declared := f.b.Contract.Service.Version
	bases := publish.Bases(versions, declared)
	tag := versions[0]
	switch {
	case slices.Contains(versions, declared):
		tag = declared
	case len(bases) > 0:
		tag = bases[len(bases)-1]
	}
	pulls := baselines{pl: pl, repo: repo, got: map[string]pulled{}}
	base, ok := pulls.get(ctx, f, tag)
	if !ok {
		return
	}
	f.row.Baseline, f.row.BaselineDigest = tag, base.digest
	migrated := f.asWritten != f.b
	if !pl.carryFrom(ctx, f, pulls, tags, carryBase{repo: repo, version: tag, digest: base.digest, b: base.b}, bases) {
		return
	}
	same, why := Unchanged(base.b, f.b)
	if !same && migrated && !f.carried {
		// A baseline an earlier CLI published as written is this content
		// too: a migration alone never republishes.
		if sameWritten, whyWritten := Unchanged(base.b, f.asWritten); sameWritten {
			same, why, f.b = true, whyWritten, f.asWritten
			f.recordImages()
		}
	}
	f.row.Reason = why
	if same {
		f.row.Changed = "no"
		f.row.Impact = ci.Impact{Gate: impact.GateClear, Text: "-"}
		return
	}
	f.row.Changed = "yes"
	f.row.Classification = classify(ctx, base.b, f.b).String()
	if pulls.version(ctx, f, bases) {
		f.base = base.b // impact decides the row
	}
	if f.row.Derived {
		f.row.Notes = append(f.row.Notes, planModeNote)
	}
}

// carryFrom carries the registry decisions of the row's carry base
// (baselines.carryBase) into the folder's bundle; false when the row is
// decided (unverified).
func (pl *ciPlanner) carryFrom(ctx context.Context, f *ciFolder, pulls baselines, tags []string, base carryBase, bases []string) bool {
	cb, ok := pulls.carryBase(ctx, f, base, bases)
	if !ok {
		return false
	}
	dc, ok := pl.declaration(ctx, f, tags, cb)
	return ok && pl.carry(ctx, f, cb, dc)
}

// carry folds the acceptances base carries into the folder's bundle, so
// the plan judges and publish pushes the accepted fields as declared. A
// declared version that is already published pins no range: its contract
// cannot change. A cited acceptance that cannot be read leaves the row
// unverified: false.
func (pl *ciPlanner) carry(ctx context.Context, f *ciFolder, base carryBase, dc *declarationCarry) bool {
	res, err := carryForward(ctx, pl.acceptances(), base, f.b, dc)
	if err != nil {
		f.decide(impact.GateUnverified, "carried acceptances unreadable: "+err.Error())
		return false
	}
	f.row.Notes = append(f.row.Notes, res.Notes...)
	switch {
	case res.Bundle == f.b:
		f.row.Carried = res.Carried
	case res.Pinned+res.Declared > 0 && f.b.Contract.Service.Version == base.version && !unchangedBy(base.b, res.Bundle):
		// The declared version is published with other content: pinning a
		// field would change its contract. Each decision waits for the next
		// version, and says so.
		f.row.Notes = append(f.row.Notes, "registry decisions carry with the next version: bump service.version")
		f.row.Carried = waiting(res.Carried)
	default:
		f.b, f.carried, f.row.Accepted, f.row.Carried = res.Bundle, true, res.Pinned, res.Carried
		f.recordImages()
	}
	return true
}

// unchangedBy reports whether b holds what base published.
func unchangedBy(base, b *contract.Bundle) bool {
	same, _ := Unchanged(base, b)
	return same
}

// waiting is carried with every field that would carry marked dropped
// until the next version.
func waiting(carried []ci.Carried) []ci.Carried {
	out := slices.Clone(carried)
	for i := range out {
		if out[i].State == ci.CarriedCarried {
			out[i].State, out[i].Reason = ci.CarriedDropped, "carries with the next version: bump service.version"
		}
	}
	return out
}

// declaration is the registry declaration base carries, with the fields it
// decides under the owner rule (Decision 3, 5); nil for none. A check that
// could not complete leaves the row unverified: false.
func (pl *ciPlanner) declaration(ctx context.Context, f *ciFolder, tags []string, base carryBase) (*declarationCarry, bool) {
	t := pl.decl()
	svc := f.b.Contract.Service
	d, dropped, notes, err := t.declarationSource(ctx, pl.reg, wantService{Name: svc.Name, Domain: svc.Domain}, tags, base)
	f.row.Notes = append(f.row.Notes, notes...)
	switch {
	case err != nil:
		f.decide(impact.GateUnverified, err.Error())
		return nil, false
	case d == nil && dropped != nil:
		return &declarationCarry{cands: dropped}, true
	case d == nil:
		return nil, true
	case t.publicGood:
		f.row.Notes = append(f.row.Notes, declarationsPublicGoodNote)
	}
	now := pl.s.env().now()
	var v ownerVerdict
	if !d.Bundle.Contract.Service.Owner.IsEmpty() || len(d.Record.Declaration.Accepted) > 0 {
		policy, from := pl.ownerPolicy(base.repo)
		if v, err = t.ownerHistory(ctx, pl.reg, base.repo, tags, d, policy, from, now); err != nil {
			f.decide(impact.GateUnverified, "registry declaration "+d.Ref+" could not be checked: "+err.Error())
			return nil, false
		}
	}
	return &declarationCarry{d: d, cands: declarationCandidates(d, v, now)}, true
}

// firstPublish fills the row of a service with no published version. It
// reads clear only when its name and its repository are declared: a 404 on
// a guessed name is no evidence.
func firstPublish(f *ciFolder, repo string, declaredRepo bool, source string) {
	v := f.b.Contract.Service.Version
	f.row.Changed, f.row.Next, f.row.Derived = "yes", cmp.Or(v, firstVersion), v == ""
	f.row.Reason = "no version of " + repo + " is published"
	_, inferred := f.b.NameInferred()
	switch {
	case inferred:
		f.decide(impact.GateUnverified, "first publish of "+repo+": name inferred; declare service.name")
	case !declaredRepo:
		f.decide(impact.GateUnverified, "first publish of "+repo+": registry from "+source+"; pass --registry")
	default:
		f.row.Impact = ci.Impact{Gate: impact.GateClear, Text: "first publish, clear"}
	}
}

// pulled is a baseline bundle and the digest it was read by.
type pulled struct {
	b      *contract.Bundle
	digest string
}

// baselines reads a row's published versions, each tag once, by digest.
type baselines struct {
	pl   *ciPlanner
	repo string
	got  map[string]pulled
}

// get is the bundle tag points at, read as repo@digest; false (with the
// row unverified) when the registry cannot say.
func (bs baselines) get(ctx context.Context, f *ciFolder, tag string) (pulled, bool) {
	if p, ok := bs.got[tag]; ok {
		return p, true
	}
	head, err := bs.pl.reg.Head(ctx, bs.repo+":"+tag)
	if err == nil && head == nil {
		err = fmt.Errorf("%s:%s is listed but not found", bs.repo, tag)
	}
	var b *contract.Bundle
	if err == nil {
		b, err = bs.pl.reg.Pull(ctx, bs.repo+"@"+head.Digest)
	}
	if err != nil {
		f.decide(impact.GateUnverified, "baseline unreadable: "+err.Error())
		return pulled{}, false
	}
	bs.got[tag] = pulled{b: b, digest: head.Digest}
	return bs.got[tag], true
}

// carryBase is the release a row carries registry decisions from, and
// records it on f: base, or for a derived version whose base is a
// candidate the newest final release below it (bases[0]), the one publish
// measures the version from. false (with the row unverified) when it
// cannot be read.
func (bs baselines) carryBase(ctx context.Context, f *ciFolder, base carryBase, bases []string) (carryBase, bool) {
	if f.b.Contract.Service.Version == "" && len(bases) > 1 {
		final, ok := bs.get(ctx, f, bases[0])
		if !ok {
			return carryBase{}, false
		}
		base = carryBase{repo: bs.repo, version: bases[0], digest: final.digest, b: final.b}
	}
	f.carriedFrom = carryBase{repo: base.repo, version: base.version, digest: base.digest}
	return base, true
}

// version fills VERSION: the declared version checked against every base
// publish checks (a refusal blocks the row, since publish would refuse it
// too), or the smallest version every base accepts, derived. It reports
// whether the row is still undecided.
func (bs baselines) version(ctx context.Context, f *ciFolder, bases []string) bool {
	declared := f.b.Contract.Service.Version
	if declared != "" && declared == f.row.Baseline {
		f.decide(impact.GateBlocked, declared+" is published with other content"+publish.NextVersion)
		return false
	}
	next := ""
	for _, tag := range bases {
		base, ok := bs.get(ctx, f, tag)
		if !ok {
			return false
		}
		need := diff.RequiredBump(classify(ctx, base.b, f.b), tag)
		if declared != "" {
			if err := diff.CheckBump(tag, declared, need); err != nil {
				f.decide(impact.GateBlocked, err.Error())
				return false
			}
			continue
		}
		floor, _ := diff.Floor(tag, need) // ponytail: tag passed semver.Filter, so it parses
		if next == "" || semver.Compare(floor, next) > 0 {
			next = floor
		}
	}
	f.row.Next, f.row.Derived = cmp.Or(declared, next), declared == ""
	return true
}

// classify is the change from base to local, with local's undeclared parts
// filled the way publish fills them.
func classify(ctx context.Context, base, local *contract.Bundle) diff.Classification {
	return diff.Compare(ctx, base.Contract, normalised(local.Contract, base.Contract.Service.Version), base.FS, local.FS).Classification
}

// normalised is a copy of c with an absent pactoVersion and service.version
// filled the way publish fills them: the current version and version.
func normalised(c *contract.Contract, version string) *contract.Contract {
	n := *c
	n.PactoVersion = cmp.Or(n.PactoVersion, contract.Current())
	n.Service.Version = cmp.Or(n.Service.Version, version)
	return &n
}

// contentDigest is a bundle's content without pacto.yaml, which publish
// rewrites.
func contentDigest(b *contract.Bundle) (string, error) {
	return lock.HashFS(contract.WithoutFile(b.FS, DefaultContractPath))
}

// inferredRecordErr is why b's inferred record cannot be read or verified,
// nil when it can or b has none.
func inferredRecordErr(b *contract.Bundle) error {
	rec, err := contract.ReadInferredRecord(b.FS)
	if err == nil && rec != nil {
		err = contract.VerifyInferredRecord(b.FS, rec)
	}
	return err
}

// Unchanged reports whether local holds what base published, and why. It
// compares content, never a manifest digest (publish labels the manifest and
// pins pactoVersion): the files other than pacto.yaml, the inferred record
// by its cited inputs (contract.InferredUnchanged), and the parsed contracts
// once local's undeclared pactoVersion and service.version are filled the
// way publish fills them.
func Unchanged(base, local *contract.Bundle) (bool, string) {
	v := base.Contract.Service.Version
	same, what := contract.InferredUnchanged(base, local, contentDigest)
	switch {
	case !same && what != "":
		return false, "inferred inputs differ from " + v + " (" + what + ")"
	case !same:
		bd, berr := contentDigest(&contract.Bundle{FS: contract.WithoutInferredRecord(base.FS)})
		ld, lerr := contentDigest(&contract.Bundle{FS: contract.WithoutInferredRecord(local.FS)})
		switch {
		case berr != nil:
			return false, "baseline " + v + " content unreadable: " + berr.Error()
		case lerr != nil:
			return false, "content unreadable: " + lerr.Error()
		case bd != ld:
			return false, fmt.Sprintf("content %s != baseline %s %s", shortDigest(ld), v, shortDigest(bd))
		}
		// The same content, so InferredUnchanged refused a record that does
		// not read or verify: say which, never assert a difference.
		if err := inferredRecordErr(local); err != nil {
			return false, "inferred record unreadable: " + err.Error()
		}
		// ponytail: local's record verified and the content matches, so
		// only the baseline's record can have failed.
		return false, "baseline " + v + " inferred record unreadable: " + fmt.Sprint(inferredRecordErr(base))
	}
	if keys := contractKeys(base.Contract, normalised(local.Contract, v)); len(keys) > 0 {
		return false, "contract differs from " + v + " (" + strings.Join(keys, ", ") + ")"
	}
	if what == "fields" {
		return true, "same content as " + v
	}
	return true, "only the inferred record differs (" + what + "): not republished"
}

// contractKeys are the top-level contract keys a and b differ in, sorted.
func contractKeys(a, b *contract.Contract) []string {
	top := func(c *contract.Contract) map[string]json.RawMessage {
		var m map[string]json.RawMessage
		raw, _ := json.Marshal(c) // ponytail: a parsed contract always marshals
		_ = json.Unmarshal(raw, &m)
		return m
	}
	am, bm := top(a), top(b)
	var keys []string
	for k := range am {
		if string(am[k]) != string(bm[k]) {
			keys = append(keys, k)
		}
	}
	for k := range bm {
		if _, ok := am[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	return keys
}

// shortDigest is a digest cut to its first 4 hex characters.
func shortDigest(d string) string {
	if len(d) > len("sha256:")+4 {
		return d[:len("sha256:")+4] + "…"
	}
	return d
}

// hostFence is the policy resolver of a plan: it refuses, before any
// request, every oci:// ref outside the one allowed host and records the
// host, since a ref is text a pull request author controls.
type hostFence struct {
	inner   validation.OriginBundleResolver
	host    string
	refused []string
}

// RootBase implements [validation.OriginBundleResolver].
func (f *hostFence) RootBase() string { return f.inner.RootBase() }

// ResolveBundle implements [validation.BundleResolver].
func (f *hostFence) ResolveBundle(ctx context.Context, ref string) (*contract.Bundle, error) {
	b, _, err := f.ResolveBundleFrom(ctx, f.RootBase(), ref)
	return b, err
}

// ResolveBundleFrom implements [validation.OriginBundleResolver].
func (f *hostFence) ResolveBundleFrom(ctx context.Context, base, ref string) (*contract.Bundle, string, error) {
	if r := graph.ParseDependencyRef(ref); r.IsOCI() {
		if h := hostOf(r.Location); f.host == "" || h != f.host {
			f.refused = append(f.refused, h)
			return nil, "", fmt.Errorf("%s is outside the registry this plan reads", h)
		}
	}
	return f.inner.ResolveBundleFrom(ctx, base, ref)
}

// relSlash is dir's slash path below root, "." for the root itself.
func relSlash(root, dir string) string {
	rel, _ := filepath.Rel(root, dir) // Walk yields only dirs under root
	return filepath.ToSlash(rel)
}

// ciChangesSource is the snapshot source holding the repository's folders,
// changed ones as planned, under their repository's domain.
const ciChangesSource = "ci-changes"

// ciImpacts runs impact for every changed row over one snapshot: the
// repository, the planned revisions and the consumer sources the caller
// named, read through a store fenced to the hosts the caller named. A row
// reads clear only when a consumer source answered, the answer is complete
// and nothing inferred changed. It returns the snapshot it ran over, nil when
// no row changed.
func (s *Service) ciImpacts(ctx context.Context, p *ci.Plan, folders []*ciFolder, opts CIOptions, allowed string) (map[string]*impact.Result, *fleet.FleetSnapshot, error) {
	p.ConsumerSources = opts.Fleet.ConsumerSources()
	repo := &fleet.Collection{}
	unpublished := map[fleet.ServiceKey]bool{}
	var todo []*ciFolder
	for _, f := range folders {
		if f.base != nil {
			// The planned revision carries the version it would publish, so
			// a consumer's range is checked against it.
			planned := *f.b
			planned.Contract = normalised(f.b.Contract, f.row.Next)
			f.b = &planned
			todo = append(todo, f)
			// The baseline says the service is published at its repository,
			// so a consumer that names it only by that ref resolves to it.
			repo.Revisions = append(repo.Revisions, fleet.RawRevision{Bundle: f.base, Domain: contract.OCIDomain(f.row.Repository),
				RequestedRef: "oci://" + f.row.Repository + "@" + f.row.BaselineDigest, Digest: f.row.BaselineDigest})
		}
		// Every folder with a repository joins under that repository's
		// domain, so a repository-local consumer that declares the name of
		// a changed service is found. ponytail: a folder decided before its
		// repository (invalid, outside the registry) is left out.
		if f.row.Repository != "" {
			rev := fleet.RawRevision{Bundle: f.b, Domain: contract.OCIDomain(f.row.Repository), RequestedRef: f.dir, Baseline: f.row.BaselineDigest}
			rev.Digest, _ = lock.HashFS(contract.WithoutInferredRecord(f.b.FS)) // the folder was just read
			repo.Revisions = append(repo.Revisions, rev)
			if f.row.BaselineDigest == "" {
				unpublished[fleet.NewServiceKeyDomain(rev.Domain, f.b.Contract.Service.Name)] = true
			}
		}
	}
	if len(todo) == 0 {
		return nil, nil, nil
	}
	fo := opts.Fleet
	if len(fo.LocalRoots) == 0 {
		fo.LocalRoots = []string{opts.Root}
	}
	derived, look, _ := s.deriveTargetSnapshot(ctx, ImpactOptions{OldPath: "oci://" + todo[0].row.Repository, TargetSnapshotAuto: opts.TargetSnapshotAuto, Fleet: fo})
	fo = derived.Fleet
	p.ConsumerSources = fo.ConsumerSources()
	fenced := s.fencedTo(append(fenceHosts(fo), allowed))
	withChanges := fo
	withChanges.extraSources = append(slices.Clone(fo.extraSources), fleet.NewMemorySource(ciChangesSource, "argument", repo))
	snap, err := fenced.Fleet(ctx, withChanges)
	if err != nil {
		return nil, nil, err
	}
	p.Snapshot = snapshotInfo(snap)
	evidence, answered := consumerEvidence(snap), answeringSources(snap)
	results := map[string]*impact.Result{}
	for _, f := range todo {
		if opts.OnResolved != nil {
			opts.OnResolved(f.row.Service)(f.base, f.b)
		}
		// The rebuild that adds the index keeps the planned revisions.
		io := ImpactOptions{OldPath: "oci://" + f.row.Repository + "@" + f.row.BaselineDigest, NewPath: f.dir, Domain: contract.OCIDomain(f.row.Repository), Fleet: withChanges, unpublished: unpublished,
			Acceptances: true, Signers: opts.Signers, Getenv: opts.Getenv, Policy: opts.Policy}
		res, err := fenced.impactOnResolved(ctx, io, snap, true, f.base, f.b)
		if err != nil {
			f.decide(impact.GateUnverified, "impact: "+err.Error())
			continue
		}
		res.Registry = look
		results[f.row.Service] = res
		p.NoteIndex(res)
		f.row.Impact = rowImpact(res, f.b, evidence)
		f.row.Advisories = res.Advisories
		f.row.Impact.Sources = answered
	}
	return results, snap, nil
}

// fenceHosts are the registry hosts the caller named as consumer sources.
func fenceHosts(o FleetOptions) []string {
	var hosts []string
	for _, ref := range o.CatalogRoots {
		if r := graph.ParseDependencyRef(ref); r.IsOCI() {
			hosts = append(hosts, hostOf(r.Location))
		}
	}
	for _, ref := range o.OCIRefs {
		hosts = append(hosts, hostOf(contract.TrimOCIScheme(ref)))
	}
	return hosts
}

// fencedTo is a Service whose bundle store reads only hosts and writes
// nothing; nil when s has no store.
func (s *Service) fencedTo(hosts []string) *Service {
	f := &Service{PluginRunner: s.PluginRunner, Version: s.Version, ExternalCatalogRef: s.ExternalCatalogRef, sys: s.sys}
	if s.BundleStore != nil {
		f.BundleStore = fencedStore{inner: s.BundleStore, hosts: hosts}
	}
	// ponytail: the copy starts with an empty catalog memo, so an organization
	// catalog is read once more; share the loader if that read ever matters.
	return f
}

// fencedStore is the bundle store a plan's impact reads through: only the
// hosts the caller named, since a ref inside a contract is text a pull
// request author controls, and never a write.
type fencedStore struct {
	inner oci.BundleStore
	hosts []string
}

// check refuses a ref outside the fence.
func (f fencedStore) check(ref string) error {
	if h := hostOf(contract.TrimOCIScheme(ref)); h == "" || !slices.Contains(f.hosts, h) {
		return fmt.Errorf("%s is outside the registries this plan reads", h)
	}
	return nil
}

// Push implements [oci.BundleStore]: a plan writes nothing.
func (fencedStore) Push(context.Context, string, *contract.Bundle) (string, error) {
	return "", errors.New("a plan writes nothing")
}

// Pull implements [oci.BundleStore].
func (f fencedStore) Pull(ctx context.Context, ref string) (*contract.Bundle, error) {
	if err := f.check(ref); err != nil {
		return nil, err
	}
	return f.inner.Pull(ctx, ref)
}

// Resolve implements [oci.BundleStore].
func (f fencedStore) Resolve(ctx context.Context, ref string) (string, error) {
	if err := f.check(ref); err != nil {
		return "", err
	}
	return f.inner.Resolve(ctx, ref)
}

// ListTags implements [oci.BundleStore].
func (f fencedStore) ListTags(ctx context.Context, repo string) ([]string, error) {
	if err := f.check(repo); err != nil {
		return nil, err
	}
	return f.inner.ListTags(ctx, repo)
}

// CacheDir implements [oci.CacheLocator] when the inner store does, so
// --cache reads the store's own cache.
func (f fencedStore) CacheDir() string { return bundleStoreCacheDir(f.inner) }

// consumerSource reports whether st is a source the caller named that can
// name a consumer: not the repository, not the planned revisions and not an
// advisory source.
func consumerSource(st fleet.SourceState) bool {
	return st.Kind != "local" && st.ID != ciChangesSource && !st.Advisory
}

// sourceStatus is st's status, "empty" for an available source that holds
// nothing.
func sourceStatus(st fleet.SourceState) string {
	if st.Status == fleet.SourceAvailable && st.RevisionCount+st.TargetCount == 0 {
		return "empty"
	}
	return string(st.Status)
}

// snapshotInfo is what the plan prints about the snapshot impact ran over:
// only the consumer sources, so an advisory one never reads as a consumer
// source.
func snapshotInfo(snap *fleet.FleetSnapshot) ci.SnapshotInfo {
	info := ci.SnapshotInfo{AsOf: snap.GeneratedAt, Completeness: string(snap.Completeness)}
	for _, st := range snap.Sources {
		if consumerSource(st) {
			info.Sources = append(info.Sources, ci.SourceLine{Name: ci.Redact(st.ID), Status: sourceStatus(st)})
		}
	}
	return info
}

// answeringSources are the redacted names of the consumer sources that
// answered: available and holding something.
func answeringSources(snap *fleet.FleetSnapshot) []string {
	out := []string{}
	for _, st := range snap.Sources {
		if consumerSource(st) && sourceStatus(st) == string(fleet.SourceAvailable) {
			out = append(out, ci.Redact(st.ID))
		}
	}
	return out
}

// consumerEvidence is why no consumer source answered, "" when one did.
func consumerEvidence(snap *fleet.FleetSnapshot) string {
	why := "no consumer source"
	for _, st := range snap.Sources {
		switch {
		case !consumerSource(st):
		case sourceStatus(st) == string(fleet.SourceAvailable):
			return ""
		case why == "no consumer source":
			why = "consumer source " + ci.Redact(st.ID) + " " + sourceStatus(st)
		}
	}
	return why
}

// maxNamed is how many blocking consumers a row names.
const maxNamed = 3

// rowImpact maps an impact answer onto a row. A blocked answer stands on its
// own; a clear one needs a declared name, no inferred change, a consumer
// source that answered and a complete answer, else the first that fails.
func rowImpact(res *impact.Result, local *contract.Bundle, evidence string) ci.Impact {
	switch res.Gate {
	case impact.GateBlocked:
		names := res.BlockingServices()
		text := res.GateReason
		if len(names) > 0 {
			text = strings.Join(names[:min(len(names), maxNamed)], ", ")
			if len(names) > maxNamed {
				text += fmt.Sprintf(", +%d", len(names)-maxNamed)
			}
		}
		return ci.Impact{Gate: impact.GateBlocked, Text: "blocked (" + text + ")", Blocking: names}
	case impact.GateUnverified:
		if res.GateCode() == impact.GateCodeInferredPossibleBreak {
			return unverified("possible break (inferred): interfaces[" + res.Advisories[0].Interface + "]; declare it to classify")
		}
		return unverified(res.GateReason)
	}
	_, nameInferred := local.NameInferred()
	switch fields := inferredChanges(res); {
	case len(fields) > 0:
		return unverified("inferred fields changed: " + strings.Join(fields, ", ") + "; declare them to classify")
	case nameInferred:
		return unverified("service.name inferred; declare it")
	case evidence != "":
		return unverified(evidence)
	case res.Completeness != fleet.CompletenessComplete:
		// The answer says why it is partial; the repository itself, never
		// only a consumer source, can be the cause.
		why := "impact answer " + cmp.Or(string(res.Completeness), string(fleet.CompletenessPartial))
		if len(res.Limitations) > 0 {
			why += ": " + res.Limitations[0].Message
		}
		return unverified(why)
	}
	return ci.Impact{Gate: impact.GateClear, Text: "clear"}
}

// breakNotes are the row notes of the consumers the break b announces: where
// each stands and when the announcement expires, with where that date came
// from (declared, or recorded in pacto.inferred.yaml from the commit time).
func breakNotes(b *contract.Bundle, res *impact.Result) []string {
	if b == nil || res == nil {
		return nil
	}
	rec, _ := contract.ReadInferredRecord(b.FS) // propose just wrote it
	var out []string
	seen := map[string]bool{}
	for _, c := range res.Consumers {
		if c.Break == nil || seen[c.Service] {
			continue
		}
		seen[c.Service] = true
		note := "break " + c.Break.Text(c.Service)
		if c.Break.Expires != "" {
			note += "; expires " + c.Break.Expires + " (" + expirySource(b.Contract.Breaks, rec, c.Service) + ")"
		}
		out = append(out, note)
	}
	return out
}

// expirySource says where the expiry of the break naming consumer comes
// from: "declared", or the recorded source with "inferred".
func expirySource(breaks []contract.Break, rec *contract.InferredRecord, consumer string) string {
	i := slices.IndexFunc(breaks, func(b contract.Break) bool { return slices.Contains(b.Consumers, consumer) })
	if i >= 0 && breaks[i].Expires != "" {
		return "declared"
	}
	src := "commit time + 30d"
	if rec != nil {
		for _, f := range rec.Fields {
			if f.Field == "breaks["+strconv.Itoa(i)+"].expires" {
				src = strings.Replace(strings.Replace(f.Source, ":", " ", 1), " +", " + ", 1)
			}
		}
	}
	return src + ", inferred"
}

// unverified is an unverified row impact saying why.
func unverified(why string) ci.Impact {
	return ci.Impact{Gate: impact.GateUnverified, Text: "unverified (" + why + ")"}
}

// inferredChanges are the inferred fields the change touches.
func inferredChanges(res *impact.Result) []string {
	var out []string
	for _, set := range [][]diff.Change{res.BreakingChanges, res.PotentiallyBreakingChanges, res.NonBreakingChanges} {
		for _, c := range set {
			if f, ok := strings.CutPrefix(c.Path, "inferred."); ok {
				out = append(out, f)
			}
		}
	}
	return out
}
