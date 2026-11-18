package app

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/internal/ci"
	"github.com/trianalab/pacto/v4/internal/discover"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/diff"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/graph"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/lock"
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
}

// firstVersion is the version an undeclared service.version starts at.
const firstVersion = "1.0.0"

// CIPlan plans every service folder under opts.Root: the CI context, the
// registry with its source and one row per folder, judged against the
// folder's baseline published revision. It reads the repository and the one
// registry host it trusts, and writes nothing. The results map holds each
// impacted row's answer by service.
func (s *Service) CIPlan(ctx context.Context, opts CIOptions) (*ci.Plan, map[string]*impact.Result, error) {
	cctx := ci.Detect(opts.Getenv)
	p := &ci.Plan{Platform: cctx.Platform, Base: cctx.Base, OIDC: cctx.OIDC}
	walk, err := discover.Walk(ctx, opts.Root, func(dir string, err error) error {
		if dir == opts.Root {
			return err
		}
		p.Notes = append(p.Notes, "unreadable, skipped: "+err.Error())
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
	folders, images := s.ciFolders(opts.Root, walk.Dirs, cat)
	var notes []string
	p.Registry, notes = ci.ResolveRegistry(opts.Registry, cctx, images)
	p.Notes = append(p.Notes, notes...)
	p.Notes = append(p.Notes, blockCollisions(folders)...)
	pl := &ciPlanner{s: s, p: p, reg: s.env().publishRegistry(), allowed: allowedHost(p.Registry)}
	for _, f := range folders {
		pl.plan(ctx, f)
		p.Rows = append(p.Rows, f.row)
	}
	p.Settle()
	return p, nil, nil
}

// ciFolder is one service folder being planned.
type ciFolder struct {
	dir string
	row ci.Row
	b   *contract.Bundle // nil when the folder could not be read
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
func (s *Service) ciFolders(root string, dirs []string, cat *externalcatalog.Catalog) ([]*ciFolder, []ci.ImageRef) {
	var out []*ciFolder
	var images []ci.ImageRef
	for _, dir := range dirs {
		f := &ciFolder{dir: dir, row: ci.Row{Dir: relSlash(root, dir)}}
		out = append(out, f)
		b, err := s.ciBundle(dir, root, cat)
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
// repository root: the one bundle a plan judges.
func (s *Service) ciBundle(dir, root string, cat *externalcatalog.Catalog) (*contract.Bundle, error) {
	b, err := readLocalBundle(dir)
	if err != nil {
		return nil, err
	}
	return b, completeLocal(b, dir, dirBase(dir), root, s.localInputs(dir, ""), cat)
}

// recordImages counts the row's inferred fields and returns the images they
// name, with folder-qualified sources.
func (f *ciFolder) recordImages() []ci.ImageRef {
	rec, _ := contract.ReadInferredRecord(f.b.FS) // ponytail: the record was just written by completion
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
	switch reg.Source {
	case "--registry", "GITHUB_REPOSITORY_OWNER", "CI_REGISTRY_IMAGE":
		return hostOf(reg.Value)
	}
	return ""
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
}

// registryName is the plan's registry as messages name it.
func (pl *ciPlanner) registryName() string { return cmp.Or(pl.p.Registry.Value, "none") }

// plan fills one folder's row, first rule that applies: validation, the
// repository, the baseline, changed, the version rule.
func (pl *ciPlanner) plan(ctx context.Context, f *ciFolder) {
	if f.row.Impact.Gate != "" || !pl.validate(ctx, f) {
		return
	}
	repo, declared, ok := pl.repository(f)
	if !ok {
		return
	}
	f.row.Repository = repo
	pl.judge(ctx, f, repo, declared)
}

// validate validates the folder with policy and configuration refs fenced
// to the allowed host. A refused ref leaves the row unverified, never
// invalid: what it would have said is unknown.
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
		f.decide(impact.GateUnverified, "policy ref on "+fence.refused[0]+" not fetched: outside the registry "+pl.registryName())
	}
	return f.row.Impact.Gate == ""
}

// repository is the folder's bundle repository and whether a declared
// source names it: publish.bundle when declared, else <registry>/<name>.
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
	if pub := f.b.Contract.Publish; pub != nil && pub.Bundle != "" {
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
	same, why := Unchanged(base.b, f.b)
	f.row.Reason = why
	if same {
		f.row.Changed = "no"
		f.row.Impact = ci.Impact{Gate: impact.GateClear, Text: "-"}
		return
	}
	f.row.Changed = "yes"
	f.row.Classification = classify(ctx, base.b, f.b).String()
	if pulls.version(ctx, f, bases) {
		f.decide(impact.GateUnverified, "pending")
	}
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
		bd, _ := contentDigest(&contract.Bundle{FS: contract.WithoutInferredRecord(base.FS)})
		ld, _ := contentDigest(&contract.Bundle{FS: contract.WithoutInferredRecord(local.FS)})
		if bd == ld {
			return false, "the inferred record differs from " + v
		}
		return false, fmt.Sprintf("content %s != baseline %s %s", shortDigest(ld), v, shortDigest(bd))
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
