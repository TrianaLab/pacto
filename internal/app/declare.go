package app

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing/fstest"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"

	"github.com/trianalab/pacto/v4/internal/discover"
	"github.com/trianalab/pacto/v4/internal/pactify"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/diff"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/semver"
	"github.com/trianalab/pacto/v4/pkg/signing"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// DeclareRequest is one declare run: the workload to declare (name, ns/name
// or ns/Kind/name), or the folder pacto init --cluster wrote, the registry
// its contract is published to, the proposed ranges to accept and how it may
// sign. Dynamic reads the cluster with the caller's own credentials.
type DeclareRequest struct {
	Service, Namespace, Dir, Registry   string
	Accept                              []string
	OnBehalfOf, IntentDigest, FromDraft string
	MaxItems                            int
	CallWindow                          time.Duration
	Signing                             declareSigning
	Dynamic                             dynamic.Interface
	// Agent is a run on an agent's behalf: it writes a draft, never a
	// declared record.
	Agent bool
	// OnPlan, when set, is handed the plan before any login starts or
	// anything is written.
	OnPlan func(*DeclarePlan)
}

// DeclarePlan is what a declare run would publish, signed by whom, decided
// without writing anything.
type DeclarePlan struct {
	Service    string `json:"service"`
	Version    string `json:"version"`
	BundleRepo string `json:"bundleRepo"`
	// State is declared for a person's login, else draft.
	State        string           `json:"state"`
	InferredFrom string           `json:"inferredFrom"`
	Cluster      string           `json:"cluster,omitempty"`
	Baseline     *DeclareBaseline `json:"baseline,omitempty"`
	// Deploy is the baseline deploy artifact the version reuses, or none.
	Deploy       string                      `json:"deploy"`
	Ranges       []DeclaredRange             `json:"ranges"`
	Owner        string                      `json:"owner,omitempty"`
	Signer       SignerView                  `json:"signer"`
	PolicyNote   string                      `json:"policyNote,omitempty"`
	Coverage     []contract.InferredCoverage `json:"coverage,omitempty"`
	Hidden       *contract.InferredHidden    `json:"hidden,omitempty"`
	IntentDigest string                      `json:"intentDigest"`
	// AcceptCLI is the command a person runs to accept a draft.
	AcceptCLI string `json:"acceptCLI,omitempty"`
	// Unchanged is the baseline version when nothing changed since it: the
	// plan then writes nothing.
	Unchanged string `json:"unchanged,omitempty"`
	// Skipped are the versions never taken as the baseline, with who signed.
	Skipped []SkippedTag `json:"skipped,omitempty"`
	Notes   []string     `json:"notes,omitempty"`
	// Unbacked are declared changes neither the baseline, the cluster read
	// nor an accepted range backs (an accepted draft).
	Unbacked []string `json:"unbacked,omitempty"`
	// UnbackedUnchecked is why the unbacked check did not run: then nothing
	// is known about what backs the declared changes.
	UnbackedUnchecked string       `json:"unbackedUnchecked,omitempty"`
	Publish           publish.Plan `json:"publish"`
}

// DeclareBaseline is the published version a declaration starts from and
// who signed it.
type DeclareBaseline struct {
	contract.InferredBaseline
	Signer string `json:"signer,omitempty"`
}

// DeclaredRange is one dependency of the declaration: its declared
// compatibility, the range the cluster read proposed, the versions observed
// and whether this run accepted the proposal.
type DeclaredRange struct {
	Dependency    string `json:"dependency"`
	Compatibility string `json:"compatibility"`
	Proposed      string `json:"proposed,omitempty"`
	Observed      string `json:"observed,omitempty"`
	Accepted      bool   `json:"accepted,omitempty"`
}

// SignerView is who signs a declare run: the identity (empty for a person
// before the login), its class, where it comes from (key, ambient, login or
// none) and why it writes what it writes.
type SignerView struct {
	Subject string `json:"subject,omitempty"`
	Issuer  string `json:"issuer,omitempty"`
	Class   string `json:"class"`
	Source  string `json:"source"`
	Why     string `json:"why,omitempty"`
}

// SkippedTag is a version a declaration does not start from: who signed it
// and why that does not count.
type SkippedTag struct {
	Tag    string `json:"tag"`
	Signer string `json:"signer"`
	Why    string `json:"why"`
}

// DeclareError is a declare refusal: a stable code, what happened and the
// command a person runs about it.
type DeclareError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	CLI     string `json:"cli,omitempty"`
}

func (e *DeclareError) Error() string { return e.Message }

// The declare refusal codes.
const (
	codeNoRegistry             = "NoRegistry"
	codeRegistryUnreadable     = "RegistryUnreadable"
	codeWorkloadNotFound       = "WorkloadNotFound"
	codeWorkloadAmbiguous      = "WorkloadAmbiguous"
	codeAlreadyDeclared        = "AlreadyDeclared"
	codeOwnerNeedsApprovers    = "OwnerNeedsApprovers"
	codeRangeNotProposed       = "RangeNotProposed"
	codeVersionConflict        = "VersionConflict"
	codeBaselineNeedsMigration = "BaselineNeedsMigration"
	codeBundleRepoMismatch     = "BundleRepoMismatch"
	codePlanStale              = "PlanStale"
	codeSignerNotAllowed       = "SignerNotAllowed"
	codeSignerUnverified       = "SignerUnverified"
	codeDraftNotFound          = "DraftNotFound"
)

// firstDeclaration is the version of a service its registry holds none of.
const firstDeclaration = "0.1.0"

// plannedPerson is the asker of a planned declaration before the person
// logs in: the declared record is rebuilt with the login's identity.
const plannedPerson = "you"

// declaration is what a plan would publish: the bundle and its record, the
// folder files it was packed from, the bundle repository, the publish input
// and the signing options with the installation policy read.
type declaration struct {
	files   fstest.MapFS
	raw     []byte
	record  *contract.InferredRecord
	bundle  *contract.Bundle
	repo    string
	input   publish.Input
	signing declareSigning
}

// PlanDeclare plans declaring one workload: it drafts its contract from the
// cluster (or reads req.Dir and re-reads the cluster), finds the baseline,
// derives the version, applies the owner rule and the accepted ranges, packs
// the bundle with the record's declaration and decides it against the
// registry. It writes nothing outside a scratch folder it removes. A run
// without a registry is refused before any read. With req.FromDraft it plans
// accepting that draft, the plan a later run with --yes carries out.
func (s *Service) PlanDeclare(ctx context.Context, req DeclareRequest) (*DeclarePlan, *declaration, error) {
	if req.Registry == "" {
		return nil, nil, &DeclareError{Code: codeNoRegistry, Message: "declaring a service needs the registry its contract is published to (--registry)",
			CLI: req.command("")}
	}
	if req.FromDraft != "" {
		return s.fromDraft(ctx, req)
	}
	scratch, err := s.env().mkdirTemp("", "pacto-declare-")
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	pl := &declarePlanner{s: s, req: req, scratch: scratch, reg: s.env().publishRegistry(), plan: &DeclarePlan{Deploy: "none"}}
	d, err := pl.run(ctx)
	return pl.plan, d, err
}

// declarePlanner holds one plan while it is decided.
type declarePlanner struct {
	s       *Service
	req     DeclareRequest
	scratch string
	reg     publish.Registry
	plan    *DeclarePlan
	// dir is the workload folder, <scratch>/<ns>/<name>; object is its
	// workload as ns/Kind/name.
	dir, object string
	// raw, folder and rec are the folder's pacto.yaml, its contract and
	// its inferred record.
	raw      []byte
	folder   *contract.Contract
	rec      *contract.InferredRecord
	choice   signerChoice
	policy   *delivery.ClassPolicy
	rules    []delivery.PublisherRule
	baseline *contract.Bundle
	judged   map[string]bool
}

func (pl *declarePlanner) run(ctx context.Context) (*declaration, error) {
	raw, err := pl.prepare(ctx)
	if err != nil {
		return nil, err
	}
	pl.identify(ctx)
	return pl.finish(ctx, raw, pl.rec)
}

// prepare drafts the workload, plans the signer, finds the baseline and
// returns pacto.yaml with the owner rule and the accepted ranges applied.
func (pl *declarePlanner) prepare(ctx context.Context) ([]byte, error) {
	if err := pl.source(ctx); err != nil {
		return nil, err
	}
	if err := pl.signer(ctx); err != nil {
		return nil, err
	}
	if err := pl.findBaseline(ctx); err != nil {
		return nil, err
	}
	return pl.edit()
}

// source drafts the workload into the scratch folder, from the cluster or
// from req.Dir re-read against the cluster.
func (pl *declarePlanner) source(ctx context.Context) error {
	var err error
	switch {
	case pl.req.Dir != "":
		err = pl.fromDir(ctx)
	case pl.req.Dynamic == nil:
		err = errors.New("declaring a workload reads the cluster: no Kubernetes client was configured")
	default:
		var nss []string
		if pl.req.Namespace != "" {
			nss = []string{pl.req.Namespace}
		}
		err = pl.draft(ctx, pl.req.Service, nss)
	}
	if err != nil {
		return err
	}
	b, err := readLocalBundle(pl.dir)
	if err != nil {
		return err
	}
	pl.raw, pl.folder = b.RawYAML, b.Contract
	pl.plan.Service = cmp.Or(b.Contract.Service.Name, filepath.Base(pl.dir))
	pl.plan.BundleRepo = pl.repo()
	return nil
}

// draft runs the cluster read for workload into the scratch folder.
func (pl *declarePlanner) draft(ctx context.Context, workload string, nss []string) error {
	co := pactify.ClusterOptions{Workload: workload, Namespaces: nss, Out: pl.scratch, MaxItems: pl.req.MaxItems, CallWindow: pl.req.CallWindow}
	b, err := pactify.Cluster(ctx, pl.req.Dynamic, pactify.Options{}, co, pl.s.env().writeFile)
	for code, kind := range map[string]error{codeWorkloadNotFound: pactify.ErrWorkloadNotFound, codeWorkloadAmbiguous: pactify.ErrWorkloadAmbiguous, codeAlreadyDeclared: pactify.ErrAlreadyDeclared} {
		if errors.Is(err, kind) {
			return &DeclareError{Code: code, Message: err.Error()}
		}
	}
	if err != nil {
		return fmt.Errorf("reading the workload from the cluster: %w", err)
	}
	pl.dir, pl.rec = b.Results[0].Dir, b.Results[0].Inferred
	pl.object = pl.rec.Inputs.Cluster.Object
	return nil
}

// fromDir copies the folder pacto init --cluster wrote into the scratch
// folder and re-reads the workload it names: published cluster facts are
// always this run's. When the re-read fails, the folder's cluster facts are
// dropped and the plan says why.
func (pl *declarePlanner) fromDir(ctx context.Context) error {
	raw, err := os.ReadFile(filepath.Join(pl.req.Dir, contract.InferredRecordFile))
	var rec *contract.InferredRecord
	if err == nil {
		rec, err = contract.ParseInferredRecord(raw)
	}
	if err != nil || rec.Inputs.Cluster == nil {
		return &DeclareError{Code: codeWorkloadNotFound, Message: "the folder holds no cluster draft: run pacto init --cluster first",
			CLI: "pacto init --cluster --out <folder>"}
	}
	pl.object = rec.Inputs.Cluster.Object
	ns, _, name := splitObject(pl.object)
	pl.dir = filepath.Join(pl.scratch, ns, name)
	if err := pl.copyFolder(os.DirFS(pl.req.Dir)); err != nil {
		return err
	}
	why := "no Kubernetes client was configured"
	if pl.req.Dynamic != nil {
		var de *DeclareError
		switch err = pl.draft(ctx, pl.object, []string{ns}); {
		case err == nil:
			return nil
		case errors.As(err, &de) && de.Code != codeWorkloadNotFound:
			return err // a read that found a contract or two workloads is no failed read
		}
		why = err.Error()
	}
	pl.plan.Notes = append(pl.plan.Notes, "the cluster was not read again ("+why+"), so the folder's cluster facts are dropped and unknown")
	pl.rec = withoutClusterFacts(rec)
	return nil
}

// copyFolder writes every file of src into the workload folder but its
// manifests (redrafted from the cluster) and its inferred record (read
// again): a kept manifests.yaml would make the record cite it.
func (pl *declarePlanner) copyFolder(src fs.FS) error {
	return fs.WalkDir(src, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path == manifestsFile || path == contract.InferredRecordFile {
			return err
		}
		data, err := fs.ReadFile(src, path)
		if err == nil {
			err = pl.writeFile(path, data)
		}
		return err
	})
}

// manifestsFile is the workload snapshot a cluster draft writes: never part
// of a declaration, which is a contract and no deploy artifact.
const manifestsFile = "manifests.yaml"

// writeFile writes one file of the workload folder.
func (pl *declarePlanner) writeFile(path string, data []byte) error {
	full := filepath.Join(pl.dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return pl.s.env().writeFile(full, data, 0o644)
}

// withoutClusterFacts is rec with no cluster read: its cluster-sourced
// fields, cluster input, coverage and hidden counts dropped.
func withoutClusterFacts(rec *contract.InferredRecord) *contract.InferredRecord {
	out := *rec
	out.Fields = slices.DeleteFunc(slices.Clone(rec.Fields), func(f contract.Inferred) bool { return strings.HasPrefix(f.Source, "cluster:") })
	out.Inputs.Cluster, out.Coverage, out.Hidden = nil, nil, nil
	return &out
}

// splitObject splits ns/Kind/name.
func splitObject(object string) (ns, kind, name string) {
	parts := strings.SplitN(object, "/", 3)
	parts = append(parts, "", "", "")
	return parts[0], parts[1], parts[2]
}

// signer reads the installation signer policy and plans who signs.
func (pl *declarePlanner) signer(ctx context.Context) error {
	o := &pl.req.Signing
	if pl.req.Dynamic != nil {
		var publishers, classNote string
		o.Policy, publishers, o.PolicyNote = readSignerPolicy(ctx, pl.req.Dynamic)
		pl.rules = publisherRules(publishers)
		if o.Rules, classNote = policyClasses(ctx, pl.reg, publishers); classNote != "" {
			pl.plan.Notes = append(pl.plan.Notes, classNote)
		}
	} else {
		o.PolicyNote = "no installation signer policy was read (no Kubernetes client was configured); readers judge this signature with their own"
	}
	pl.policy = o.Policy
	c, err := plannedSigner(ctx, *o, pl.req.Agent)
	if err != nil {
		return err
	}
	pl.choice = c
	p := pl.plan
	p.Signer = SignerView{Subject: c.Identity.Subject, Issuer: c.Identity.Issuer, Class: string(c.Class), Source: c.Source, Why: c.Why}
	p.State, p.PolicyNote = contract.DeclarationDraft, o.PolicyNote
	if c.MayDeclare {
		p.State = contract.DeclarationDeclared
	}
	if c.Source == signerAmbient {
		p.Notes = append(p.Notes, "an ambient OIDC token is set: this run writes a draft; a person accepts it with pacto declare <service> --from-draft <tag>")
	}
	if n := rekorNotice(c.Identity, o.RekorURL); n != "" {
		p.Notes = append(p.Notes, n)
	}
	return nil
}

// findBaseline lists the bundle repository's versions and takes the newest
// final one that counts as the baseline (Decision 4): one a publisher the
// installation trusts signed, or an earlier declaration by a person its
// signer policy allows. Every higher version is listed as skipped; versions
// present with none that counts refuse the plan. With a baseline its files
// are written into the workload folder and the workload is drafted again,
// so its declared contract is kept.
func (pl *declarePlanner) findBaseline(ctx context.Context) error {
	tags, err := pl.reg.Tags(ctx, pl.repo())
	if err != nil {
		return &DeclareError{Code: codeRegistryUnreadable, Message: "listing the versions in " + pl.repo() + ": " + err.Error()}
	}
	if len(semver.Filter(tags)) == 0 {
		return nil
	}
	tag, digest, err := pl.countingTag(ctx, tags)
	if err != nil {
		return err
	}
	base, err := pl.reg.Pull(ctx, pl.repo()+"@"+digest)
	if err != nil {
		return &DeclareError{Code: codeRegistryUnreadable, Message: "reading the baseline " + tag + ": " + err.Error()}
	}
	if err := pl.checkBaseline(base); err != nil {
		return err
	}
	pl.baseline = base
	deploy, err := baselineDeploy(ctx, pl.reg, base, tag)
	if err != nil {
		return &DeclareError{Code: codeRegistryUnreadable, Message: "reading the deploy artifact of the baseline " + tag + ": " + err.Error()}
	}
	pl.plan.Baseline.InferredBaseline = contract.InferredBaseline{Version: tag, Bundle: pl.repo() + "@" + digest, Deploy: deploy}
	pl.plan.Deploy = cmp.Or(pl.plan.Baseline.Deploy, "none")
	return pl.redraft(ctx, base)
}

// repo is the bundle repository: always --registry's, so a baseline never
// chooses where the person's credentials push.
func (pl *declarePlanner) repo() string {
	bundle, _, _ := publish.Layout(pl.req.Registry, pl.plan.Service, "")
	return bundle
}

// countingTag is the newest final version that counts as the baseline,
// listing every higher one as skipped. With none, the plan is refused.
func (pl *declarePlanner) countingTag(ctx context.Context, tags []string) (string, string, error) {
	tm, err := declareRoot(pl.req.Signing.TrustedRoot)
	if err != nil {
		return "", "", err
	}
	pl.judged = map[string]bool{}
	for _, t := range semver.Filter(tags) {
		if strings.Contains(t, "-") {
			continue // a candidate is never a baseline
		}
		r, err := pl.reg.Head(ctx, pl.repo()+":"+t)
		if err != nil {
			return "", "", &DeclareError{Code: codeRegistryUnreadable, Message: "reading " + pl.repo() + ":" + t + ": " + err.Error()}
		}
		if r == nil {
			continue // the tag went away since the listing
		}
		counts, signer, why := pl.judge(ctx, tm, r.Digest)
		pl.judged[r.Digest] = counts
		if counts {
			pl.plan.Baseline = &DeclareBaseline{Signer: signer}
			return t, r.Digest, nil
		}
		pl.plan.Skipped = append(pl.plan.Skipped, SkippedTag{Tag: t, Signer: signer, Why: why})
	}
	if len(pl.plan.Skipped) == 0 {
		return "", "", &DeclareError{Code: codeVersionConflict, Message: pl.repo() + " holds only candidates: declare from a final version"}
	}
	top := pl.plan.Skipped[0]
	return "", "", &DeclareError{Code: codeVersionConflict, Message: fmt.Sprintf("%s holds %s, signed by %s, which no publisher the installation trusts "+
		"and no person its signer policy allows signed; it is never written over, so no version can be declared above it", pl.repo(), top.Tag, top.Signer)}
}

// judge is whether repo@digest counts as a baseline, who signed it and,
// when it does not count, why.
func (pl *declarePlanner) judge(ctx context.Context, tm root.TrustedMaterial, digest string) (bool, string, string) {
	signers, err := publish.SignersOf(ctx, pl.reg, tm, pl.req.Signing.RekorURL != "", pl.repo(), digest)
	signer := "nobody"
	switch {
	case len(signers) > 0:
		signer = signers[0].Subject + " (" + signers[0].Issuer + ")"
	case !errors.Is(err, publish.ErrNoSignature):
		signer = "unknown (its signatures could not be read)"
	}
	v := publish.JudgeRead(digest, signers, err, delivery.ServiceRef{Name: pl.plan.Service}, "", pl.rules, delivery.PublisherSourceRule, nil, nil)
	if v.Verdict == delivery.VerdictAccepted {
		return true, signer, ""
	}
	// ponytail: a person the signer policy allows counts whatever the
	// bundle holds; reading its record to see it is a declaration is the
	// upgrade path.
	for _, sg := range signers {
		if sg.Class == delivery.ClassHuman && pl.policy != nil && checkInstallationSigner(sg, pl.policy) == nil {
			return true, signer, ""
		}
	}
	return false, signer, cmp.Or(v.Reason, "signed by no publisher the installation trusts")
}

// checkBaseline refuses a baseline that names another bundle repository or
// is below pactoVersion 2.5, which a declaration's record needs.
func (pl *declarePlanner) checkBaseline(base *contract.Bundle) error {
	if pub := base.Contract.Publish; pub != nil && publish.Repo(pub.Bundle) != pl.repo() {
		return &DeclareError{Code: codeBundleRepoMismatch, Message: fmt.Sprintf("the baseline declares publish.bundle %s, not %s: declare with the registry it publishes to",
			publish.Repo(pub.Bundle), pl.repo())}
	}
	if v := base.Contract.PactoVersion; v != "2.5" {
		return &DeclareError{Code: codeBaselineNeedsMigration, Message: "the baseline is at pactoVersion " + v + ", and a declaration needs 2.5: migrate it and publish it first",
			CLI: "pacto migrate <dir>"}
	}
	return nil
}

// baselineDeploy is the baseline's deploy artifact at version, repo@digest,
// or "" when it declares none or the tag is not there. A read that fails is
// an error, never "none": an unread deploy artifact is unknown.
func baselineDeploy(ctx context.Context, reg publish.Registry, base *contract.Bundle, version string) (string, error) {
	pub := base.Contract.Publish
	if pub == nil {
		return "", nil
	}
	for _, a := range pub.Artifacts {
		if a.Name != pub.Deploy {
			continue
		}
		r, err := reg.Head(ctx, publish.Repo(a.Ref)+":"+version)
		if err != nil {
			return "", err
		}
		if r != nil {
			return publish.Repo(a.Ref) + "@" + r.Digest, nil
		}
	}
	return "", nil
}

// redraft writes the baseline's files into a fresh workload folder and
// drafts the workload again: the cluster read keeps the baseline's
// pacto.yaml and completes its record. With req.Dir the person's file is the
// declaration and is kept as it is.
func (pl *declarePlanner) redraft(ctx context.Context, base *contract.Bundle) error {
	if pl.req.Dir != "" {
		return nil
	}
	// ponytail: a folder that is not removed keeps its manifests.yaml, which
	// the record then cites and the bundle leaves out: validation refuses it.
	_ = os.RemoveAll(pl.dir)
	if err := pl.copyFolder(contract.WithoutInferredRecord(base.FS)); err != nil {
		return err
	}
	ns, _, _ := splitObject(pl.object)
	// The cluster read keeps the baseline's pacto.yaml as it is.
	pl.raw, pl.folder = base.RawYAML, base.Contract
	return pl.draft(ctx, pl.object, []string{ns})
}

// edit applies the owner rule and the accepted ranges to the folder's
// pacto.yaml and record.
func (pl *declarePlanner) edit() ([]byte, error) {
	raw, err := pl.owner()
	if err != nil {
		return nil, err
	}
	raw, accepted, err := applyAccept(raw, pl.rec, pl.req.Accept)
	if err != nil {
		return nil, err
	}
	pl.rec.Fields = withoutDeclaredProposals(pl.rec.Fields, pl.folder, accepted)
	pl.rec.Declaration = &contract.InferredDeclaration{Accepted: accepted}
	return raw, nil
}

// withoutDeclaredProposals drops each proposal for a dependency pacto.yaml
// already declares a range for, unless this run accepted it: the declared
// range (the baseline's, or the person's own file) stands, and a proposal
// nobody accepted beside it is moot.
func withoutDeclaredProposals(fields []contract.Inferred, c *contract.Contract, accepted []contract.InferredAcceptance) []contract.Inferred {
	return slices.DeleteFunc(fields, func(f contract.Inferred) bool {
		if f.Proposed == "" || slices.ContainsFunc(accepted, func(a contract.InferredAcceptance) bool { return a.Field == f.Field }) {
			return false
		}
		return slices.ContainsFunc(c.Dependencies, func(d contract.Dependency) bool {
			return "dependencies["+d.Name+"].compatibility" == f.Field && d.Compatibility != contract.CompatibilityUnknown && d.Compatibility != ""
		})
	})
}

// owner applies ownerRule: a cluster draft's owner the baseline does not
// declare leaves pacto.yaml and stays in the record as inferred; a folder's
// is refused, since the file is the person's.
func (pl *declarePlanner) owner() ([]byte, error) {
	planned, rec := pl.folder, pl.rec
	var base *contract.Contract
	if pl.baseline != nil {
		base = pl.baseline.Contract
	}
	err := ownerRule(base, planned)
	pl.plan.Owner = planned.Service.Owner.KeyString()
	if err == nil {
		return pl.raw, nil
	}
	if pl.req.Dir != "" {
		return nil, err
	}
	pl.plan.Owner = ""
	pl.plan.Notes = append(pl.plan.Notes, "service.owner "+planned.Service.Owner.KeyString()+" stays in pacto.inferred.yaml as inferred: "+
		"setting an owner needs the owner rule of a later release")
	rec.Fields = append(rec.Fields, ownerField(planned.Service.Owner, rec))
	slices.SortFunc(rec.Fields, func(a, b contract.Inferred) int {
		return cmp.Or(strings.Compare(a.Field, b.Field), strings.Compare(a.Source, b.Source))
	})
	return contract.RemoveServiceOwner(pl.raw)
}

// ownerRule refuses a declaration that sets an owner the baseline does not
// already declare. 513-S4 replaces it with the owner rule.
func ownerRule(baseline, planned *contract.Contract) error {
	o := planned.Service.Owner.KeyString()
	if o == "" || (baseline != nil && baseline.Service.Owner.KeyString() == o) {
		return nil
	}
	return &DeclareError{Code: codeOwnerNeedsApprovers, Message: "remove service.owner from pacto.yaml; it stays in pacto.inferred.yaml as inferred. " +
		"Setting an owner needs the owner rule of a later release"}
}

// ownerField is the owner a cluster draft read from the workload's team
// label, as the record field it stays as.
func ownerField(o contract.Owner, rec *contract.InferredRecord) contract.Inferred {
	value, declare := o.Team, "service:\n  owner:\n    team: "+discover.Quote(o.Team)+"\n"
	if o.Team == "" {
		value, declare = o.DRI+" (dri)", "service:\n  owner:\n    dri: "+discover.Quote(o.DRI)+"\n"
	}
	return contract.Inferred{Field: "service.owner", Value: value, Source: "cluster:label " + rec.Inputs.Cluster.Object + " team",
		Confidence: contract.ConfidenceLikely, Declare: declare, AsOf: rec.Inputs.Cluster.AsOf}
}

// applyAccept declares the proposed range of each named dependency in raw,
// in place of compatibility: unknown, and returns the acceptances. A name
// with no proposed range is refused.
func applyAccept(raw []byte, rec *contract.InferredRecord, names []string) ([]byte, []contract.InferredAcceptance, error) {
	var out []contract.InferredAcceptance
	for _, n := range slices.Compact(slices.Sorted(slices.Values(names))) {
		field := "dependencies[" + n + "].compatibility"
		i := slices.IndexFunc(rec.Fields, func(f contract.Inferred) bool { return f.Field == field && f.Proposed != "" })
		if i < 0 {
			return nil, nil, &DeclareError{Code: codeRangeNotProposed, Message: "no range is proposed for " + n + ": accept a dependency the cluster read proposes a range for"}
		}
		var err error
		if raw, err = contract.SetCompatibility(raw, n, rec.Fields[i].Proposed); err != nil {
			return nil, nil, &DeclareError{Code: codeRangeNotProposed, Message: err.Error()}
		}
		out = append(out, contract.InferredAcceptance{Field: field, Range: rec.Fields[i].Proposed})
	}
	return raw, out, nil
}

// identify names the inferred service the declaration comes from and reads
// the cluster's kube-system namespace uid, empty when it cannot.
func (pl *declarePlanner) identify(ctx context.Context) {
	ns, kind, name := splitObject(pl.object)
	pl.plan.InferredFrom = fleet.K8sCaller("", ns, kind, name)
	why := "no Kubernetes client was configured"
	if pl.req.Dynamic != nil && pl.rec.Inputs.Cluster == nil {
		why = "this run read no fact from the cluster"
	} else if pl.req.Dynamic != nil {
		o, err := pl.req.Dynamic.Resource(gvrNamespaces).Get(ctx, "kube-system", metav1.GetOptions{})
		if err == nil {
			pl.plan.Cluster = string(o.GetUID())
			return
		}
		why = err.Error()
	}
	pl.plan.Notes = append(pl.plan.Notes, "the cluster's identity was not read ("+why+"), so the declaration names no cluster")
}

// finish builds the record's declaration, derives the version, packs the
// bundle, computes the intent digest and decides the bundle against the
// registry.
func (pl *declarePlanner) finish(ctx context.Context, raw []byte, rec *contract.InferredRecord) (*declaration, error) {
	p := pl.plan
	accepted := rec.Declaration.Accepted
	rec.Declaration = pl.declarationBlock(accepted)
	p.Coverage, p.Hidden = rec.Coverage, rec.Hidden
	if p.State == contract.DeclarationDraft {
		p.AcceptCLI = acceptCLI(p.Service, pl.req.Registry, "<tag>")
	}
	version, err := pl.version(ctx, raw, rec)
	if err != nil || p.Unchanged != "" {
		return nil, err
	}
	// ponytail: raw parsed as a contract, so it decodes and service is a mapping.
	raw, _ = contract.SetServiceVersion(raw, version)
	d, err := pl.pack(raw, rec)
	if err != nil {
		return nil, err
	}
	p.Version, p.Ranges = version, ranges(d.bundle.Contract, rec, accepted)
	p.IntentDigest = intentDigest(p, raw, accepted, "")
	d.input = publish.Input{Bundle: d.bundle, Revision: "declare:" + p.IntentDigest, ContractOnly: true, BaselineDeploy: strings.TrimSuffix(p.Deploy, "none"),
		Publish: &contract.Publish{Bundle: "oci://" + pl.repo()},
		Checks:  []publish.Check{{Name: "signer", Passed: true, Message: "signed by " + signerText(p.Signer)}}}
	p.Publish = publish.MakePlan(ctx, pl.reg, d.input)
	return d, nil
}

// declarationBlock is the record's declaration as planned: the asker is who
// would sign (the person before the login), a draft requests the ranges a
// declaration accepts.
func (pl *declarePlanner) declarationBlock(accepted []contract.InferredAcceptance) *contract.InferredDeclaration {
	p := pl.plan
	d := &contract.InferredDeclaration{State: p.State, Asker: askerOf(pl.choice), InferredFrom: p.InferredFrom, Cluster: p.Cluster}
	if p.Baseline != nil {
		b := p.Baseline.InferredBaseline
		d.Baseline = &b
	}
	if p.State == contract.DeclarationDraft {
		d.OnBehalfOf, d.Requested = pl.req.OnBehalfOf, accepted
		return d
	}
	d.Accepted = accepted
	return d
}

// askerOf is the record's asker for a planned signer: the identity that
// signs, {"", none, agent} for an unsigned draft and the person before the
// login. An ambient token of a person's class signs a draft as an agent: a
// certificate cannot show who held the token, so it never stands for the
// person.
func askerOf(c signerChoice) contract.InferredSigner {
	switch {
	case c.Source == signerNone:
		return contract.InferredSigner{Issuer: contract.SignerUnsigned, Class: contract.SignerClassAgent}
	case c.Source == signerKey:
		return contract.InferredSigner{Issuer: signerKey, Class: contract.SignerClassAgent}
	case c.MayDeclare:
		return contract.InferredSigner{Subject: cmp.Or(c.Identity.Subject, plannedPerson), Issuer: c.Identity.Issuer, Class: contract.SignerClassHuman}
	}
	class := string(c.Class)
	if c.Class == delivery.ClassHuman {
		class = contract.SignerClassAgent
	}
	return contract.InferredSigner{Subject: c.Identity.Subject, Issuer: cmp.Or(c.Identity.Issuer, "unknown"), Class: class}
}

// version is the version the declaration publishes as (Decision 4): 0.1.0
// with no baseline, else the baseline raised by the change since it. A
// declaration that holds what its baseline published sets Unchanged.
func (pl *declarePlanner) version(ctx context.Context, raw []byte, rec *contract.InferredRecord) (string, error) {
	if pl.baseline == nil {
		return firstDeclaration, nil
	}
	local, err := pl.pack(raw, rec)
	if err != nil {
		return "", err
	}
	d, err := publish.DeriveVersion(ctx, pl.reg, publish.DeriveInput{Repo: pl.repo(), Local: local.bundle, Unchanged: Unchanged,
		Publish: &contract.Publish{Bundle: "oci://" + pl.repo()},
		Verify: func(_ context.Context, ref string) error {
			if _, digest, _ := strings.Cut(ref, "@"); pl.judged[digest] {
				return nil
			}
			return publish.ErrNoSignature
		}})
	if err != nil {
		return "", &DeclareError{Code: codeVersionConflict, Message: err.Error()}
	}
	if d.Unchanged {
		pl.plan.Unchanged, pl.plan.Version = d.Version, d.Version
	}
	return d.Version, nil
}

// pack writes raw and rec into the workload folder and packs the
// declaration bundle from it.
func (pl *declarePlanner) pack(raw []byte, rec *contract.InferredRecord) (*declaration, error) {
	if err := pl.writeFile(DefaultContractPath, raw); err != nil {
		return nil, err
	}
	b, err := declarationBundle(pl.dir, rec)
	if err != nil {
		return nil, err
	}
	return &declaration{files: b.FS.(fstest.MapFS), raw: raw, record: rec, bundle: b, repo: pl.repo(), signing: pl.req.Signing}, nil
}

// declarationBundle packs the folder dir as a declaration: every file the
// .pactoignore keeps but manifests.yaml, with rec as its inferred record, in
// memory. pacto.yaml must be at pactoVersion 2.5, and the bundle must
// validate (the declaration stage included), so an invalid declaration is
// never signed.
func declarationBundle(dir string, rec *contract.InferredRecord) (*contract.Bundle, error) {
	b, err := readLocalBundle(dir)
	if err != nil {
		return nil, err
	}
	if v := b.Contract.PactoVersion; v != "2.5" {
		return nil, fmt.Errorf("a declaration is at pactoVersion 2.5, not %q", v)
	}
	files, err := copyFSWithReplace(contract.WithoutFile(contract.WithoutInferredRecord(b.FS), manifestsFile), "", nil)
	if err != nil {
		return nil, err
	}
	files[contract.InferredRecordFile] = &fstest.MapFile{Data: rec.Marshal(), Mode: 0o644}
	out := &contract.Bundle{Contract: b.Contract, RawYAML: b.RawYAML, FS: files}
	return out, validDeclaration(out)
}

// validDeclaration refuses a declaration bundle that does not validate, the
// declaration stage included.
func validDeclaration(b *contract.Bundle) error {
	r := validation.Validate(b.Contract, b.RawYAML, b.FS)
	if r.IsValid() {
		return nil
	}
	msgs := make([]string, 0, len(r.Errors))
	for _, e := range r.Errors {
		msgs = append(msgs, e.Path+": "+e.Message)
	}
	return errors.New("the declaration is not valid: " + strings.Join(msgs, "; "))
}

// ranges are the declaration's dependencies as the plan lists them.
func ranges(c *contract.Contract, rec *contract.InferredRecord, accepted []contract.InferredAcceptance) []DeclaredRange {
	out := []DeclaredRange{}
	for _, dep := range c.Dependencies {
		r := DeclaredRange{Dependency: dep.Name, Compatibility: dep.Compatibility}
		field := "dependencies[" + dep.Name + "].compatibility"
		if i := slices.IndexFunc(rec.Fields, func(f contract.Inferred) bool { return f.Field == field }); i >= 0 {
			r.Proposed = rec.Fields[i].Proposed
			if v, ok := strings.CutPrefix(rec.Fields[i].Source, "cluster:fleet reported versions "); ok {
				r.Observed = v
			}
		}
		r.Accepted = slices.ContainsFunc(accepted, func(a contract.InferredAcceptance) bool { return a.Field == field })
		out = append(out, r)
	}
	return out
}

// signerText is how a plan names who signs.
func signerText(v SignerView) string {
	switch {
	case v.Source == signerLogin:
		return "you, after a Sigstore login (" + v.Issuer + ")"
	case v.Source == signerNone:
		return "nobody (an unsigned draft)"
	case v.Subject == "":
		return "a " + v.Source + " that names no person"
	}
	return v.Subject + " (" + v.Issuer + ")"
}

// intentDigest binds what a declaration declares, never what was observed
// or who signs (Decision 15): the inferred service, the service, version
// and bundle repository, pacto.yaml, the accepted (or requested) ranges, the
// baseline bundle and the draft accepted.
func intentDigest(p *DeclarePlan, raw []byte, accepted []contract.InferredAcceptance, draft string) string {
	sum := sha256.Sum256(raw)
	acc := slices.Clone(accepted)
	slices.SortFunc(acc, func(a, b contract.InferredAcceptance) int { return strings.Compare(a.Field, b.Field) })
	baseline := ""
	if p.Baseline != nil {
		baseline = p.Baseline.Bundle
	}
	intent, _ := json.Marshal(struct {
		Subject    string                        `json:"subject"`
		Service    string                        `json:"service"`
		Version    string                        `json:"version"`
		BundleRepo string                        `json:"bundleRepo"`
		Contract   string                        `json:"contract"`
		Accepted   []contract.InferredAcceptance `json:"accepted"`
		Baseline   string                        `json:"baseline"`
		Draft      string                        `json:"draft"`
	}{p.InferredFrom, p.Service, p.Version, p.BundleRepo, "sha256:" + hex.EncodeToString(sum[:]), acc, baseline, draft})
	out := sha256.Sum256(intent)
	return "sha256:" + hex.EncodeToString(out[:])
}

// DeclareResult is what a declare run did: the plan it carried out, whether
// it wrote anything, the bundle it pushed (repo@digest) under which tag, and
// whether that is a draft and is signed.
type DeclareResult struct {
	Plan      DeclarePlan `json:"plan"`
	Executed  bool        `json:"executed"`
	Published string      `json:"published,omitempty"`
	Tag       string      `json:"tag,omitempty"`
	Draft     bool        `json:"draft,omitempty"`
	Signed    bool        `json:"signed,omitempty"`
}

// Declare plans a declaration (or the acceptance of req.FromDraft) and
// carries it out. Only a person's interactive Sigstore login publishes the
// declared version, contract only and checked to be signed by exactly that
// person before any tag is written; every other signer writes the next
// draft-<n> tag a person accepts with --from-draft. A plan with nothing new
// writes nothing.
func (s *Service) Declare(ctx context.Context, req DeclareRequest) (*DeclareResult, error) {
	plan, d, err := s.PlanDeclare(ctx, req)
	res := &DeclareResult{}
	if plan != nil {
		res.Plan = *plan
	}
	if err != nil || plan.Unchanged != "" {
		return res, err
	}
	if err := checkDeclarable(req, plan); err != nil {
		return res, err
	}
	if req.OnPlan != nil {
		req.OnPlan(plan)
	}
	c, err := s.declareSigner(ctx, d.signing, req.Agent)
	switch {
	case err != nil:
		return res, err
	case c.MayDeclare:
		return res, s.publishDeclared(ctx, res, d, c)
	case req.FromDraft != "":
		return res, &DeclareError{Code: codeSignerNotAllowed, Message: "accepting a draft takes a person's Sigstore login: " + c.Why}
	}
	return res, s.writeDraft(ctx, req, res, d, c)
}

// checkDeclarable refuses a run whose intent digest is not the fresh plan's,
// and a declared version the registry refuses, before anyone logs in.
func checkDeclarable(req DeclareRequest, p *DeclarePlan) error {
	if req.IntentDigest != "" && req.IntentDigest != p.IntentDigest {
		return &DeclareError{Code: codePlanStale, Message: "the plan changed since intent " + req.IntentDigest + " was reviewed; it is now " + p.IntentDigest +
			": review the new plan and run again with its digest", CLI: req.command(p.IntentDigest)}
	}
	if p.State == contract.DeclarationDeclared && !p.Publish.Ready {
		return &DeclareError{Code: codeVersionConflict, Message: "the registry refuses " + p.Service + " " + p.Version + ": " + publish.Refusal(p.Publish)}
	}
	return nil
}

// publishDeclared publishes the declared version signed by the person c
// logged in as: the record names the person as asker, and the pushed bundle
// must carry exactly one verified signature, by that person, before its tag
// is written.
func (s *Service) publishDeclared(ctx context.Context, res *DeclareResult, d *declaration, c signerChoice) error {
	dec := *d.record.Declaration
	dec.State, dec.OnBehalfOf = contract.DeclarationDeclared, ""
	dec.Asker = contract.InferredSigner{Subject: c.Identity.Subject, Issuer: c.Identity.Issuer, Class: contract.SignerClassHuman}
	dec.Accepted, dec.Requested = slices.Concat(dec.Accepted, dec.Requested), nil
	if err := d.redeclare(&dec); err != nil {
		return err
	}
	reg, tm, tlog := s.env().publishRegistry(), c.Signer.Root, d.signing.RekorURL != ""
	tools := checkedTools{execTools: signerTools(reg, c.Signer, signing.Policy{AnyIdentity: true, Root: tm, Tlog: tlog}),
		check: func(ctx context.Context, ref string) error {
			repo, digest, _ := strings.Cut(ref, "@")
			got, err := publish.SignersOf(ctx, reg, tm, tlog, repo, digest)
			if err == nil {
				err = sameSigner(c.Identity, got)
			}
			if err != nil {
				return &DeclareError{Code: codeSignerUnverified, Message: "nothing was tagged: " + err.Error()}
			}
			return nil
		}}
	r, err := publish.Apply(ctx, reg, tools, d.input)
	if err := declareWriteError(err); err != nil {
		return err
	}
	res.Plan.Publish, res.Plan.Signer.Subject, res.Plan.Signer.Issuer = r.Plan, c.Identity.Subject, c.Identity.Issuer
	res.Executed, res.Signed, res.Tag, res.Published = true, true, r.Plan.Version, d.repo+"@"+r.Digests[publish.BundleItem]
	return nil
}

// writeDraft writes the declaration as the next draft-<n> tag: its asker is
// whoever signed (nobody for an unsigned draft), the ranges it would accept
// are requests, and the plan names the command a person accepts it with.
func (s *Service) writeDraft(ctx context.Context, req DeclareRequest, res *DeclareResult, d *declaration, c signerChoice) error {
	dec := *d.record.Declaration
	dec.State, dec.Asker, dec.OnBehalfOf, dec.Draft = contract.DeclarationDraft, askerOf(c), req.OnBehalfOf, nil
	dec.Requested, dec.Accepted = slices.Concat(dec.Accepted, dec.Requested), nil
	if err := d.redeclare(&dec); err != nil {
		return err
	}
	signed, reg := c.Source != signerNone, s.env().publishRegistry()
	r, tag, err := publish.ApplyDraft(ctx, reg, signerTools(reg, c.Signer, draftPolicy(d.signing, c)), d.input, signed)
	if err := declareWriteError(err); err != nil {
		return err
	}
	res.Plan.Publish = r.Plan
	res.Executed, res.Draft, res.Signed, res.Tag, res.Published = true, true, signed, tag, d.repo+"@"+r.Digests[publish.BundleItem]
	res.Plan.State, res.Plan.AcceptCLI = contract.DeclarationDraft, acceptCLI(res.Plan.Service, req.Registry, tag)
	return nil
}

// command is the pacto declare that replays req: every argument the intent
// digest binds, and intent when it is set. Without --yes it prints the plan.
func (req DeclareRequest) command(intent string) string {
	parts := []string{"pacto declare", req.Service}
	add := func(flag, v string) {
		if v != "" {
			parts = append(parts, flag, v)
		}
	}
	add("--namespace", req.Namespace)
	add("--dir", req.Dir)
	add("--registry", cmp.Or(req.Registry, "<registry>"))
	for _, n := range req.Accept {
		add("--accept", n)
	}
	add("--from-draft", req.FromDraft)
	if req.MaxItems != 0 {
		add("--max-items", strconv.Itoa(req.MaxItems))
	}
	if req.CallWindow != 0 {
		add("--call-window", req.CallWindow.String())
	}
	add("--intent", intent)
	return strings.Join(parts, " ")
}

// acceptCLI is the command a person accepts the draft tag of service with.
func acceptCLI(service, registry, tag string) string {
	return "pacto declare " + service + " --registry " + registry + " --from-draft " + tag
}

// draftPolicy is what a draft's own signature is checked against when it is
// written: the key, or any identity under the trusted root (the record names
// who signed). An unsigned draft checks nothing.
func draftPolicy(o declareSigning, c signerChoice) signing.Policy {
	switch c.Source {
	case signerNone:
		return signing.Policy{}
	case signerKey:
		return signing.Policy{Key: c.Signer.Keypair.GetPublicKey()}
	}
	return signing.Policy{AnyIdentity: true, Root: c.Signer.Root, Tlog: o.RekorURL != ""}
}

// declareWriteError is a publish failure as a declare refusal: a tag another
// run wrote, or a registry that refuses the version since the plan, is a
// version conflict.
func declareWriteError(err error) error {
	var de *DeclareError
	switch {
	case err == nil, errors.As(err, &de):
		return err
	case errors.Is(err, publish.ErrTagRace), errors.Is(err, publish.ErrNotReady):
		return &DeclareError{Code: codeVersionConflict, Message: err.Error()}
	}
	return err
}

// checkedTools signs as execTools does, then runs check on what it signed,
// before publish writes any tag.
type checkedTools struct {
	execTools
	check func(ctx context.Context, ref string) error
}

// Sign implements publish.Tools.
func (t checkedTools) Sign(ctx context.Context, sign contract.PublishSign, ref string) (bool, error) {
	signed, err := t.execTools.Sign(ctx, sign, ref)
	if err == nil {
		err = t.check(ctx, ref)
	}
	return signed, err
}

// redeclare replaces the record's declaration in the bundle, which must
// still validate.
func (d *declaration) redeclare(dec *contract.InferredDeclaration) error {
	d.record.Declaration = dec
	d.files[contract.InferredRecordFile] = &fstest.MapFile{Data: d.record.Marshal(), Mode: 0o644}
	return validDeclaration(d.bundle)
}

// fromDraft plans accepting a draft: the draft's pacto.yaml is the
// declaration, with every range the agent requested back to unknown, the
// workload read again under the person's own credentials (the draft's cluster
// facts are never signed again), the version and owner rule decided against
// today's baseline, and every declared change neither the baseline, this
// cluster read nor an accepted range backs listed as unbacked.
func (s *Service) fromDraft(ctx context.Context, req DeclareRequest) (*DeclarePlan, *declaration, error) {
	repo, _, _ := publish.Layout(req.Registry, req.Service, "")
	dr, err := readDraft(ctx, s.env().publishRegistry(), repo, req)
	if err != nil {
		return nil, nil, err
	}
	dir, err := s.env().mkdirTemp("", "pacto-draft-")
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := dr.writeTo(dir, s.env().writeFile); err != nil {
		return nil, nil, err
	}
	sub := req
	sub.Dir, sub.FromDraft = dir, ""
	plan, d, err := s.PlanDeclare(ctx, sub)
	switch {
	case err != nil || plan.Unchanged != "":
		return plan, d, err
	case plan.State != contract.DeclarationDeclared:
		return plan, nil, &DeclareError{Code: codeSignerNotAllowed, Message: "accepting a draft takes a person's Sigstore login: " + plan.Signer.Why}
	}
	plan.Unbacked, plan.UnbackedUnchecked = s.unbacked(ctx, req, dr.object, d.bundle)
	plan.Notes = append(plan.Notes, dr.notes(req.Accept)...)
	// The record is packed again with the person as asker before it is signed.
	dec := d.record.Declaration
	dec.Draft = &contract.InferredDraft{Tag: req.FromDraft, Digest: repo + "@" + dr.digest, Asker: dr.dec.Asker, OnBehalfOf: dr.dec.OnBehalfOf}
	plan.IntentDigest = intentDigest(plan, d.raw, dec.Accepted, dec.Draft.Digest)
	return plan, d, nil
}

// draftRead is a draft pulled from the registry: its bundle, digest,
// declaration, the workload it names (ns/Kind/name) and what its signature
// shows.
type draftRead struct {
	bundle         *contract.Bundle
	digest, object string
	dec            *contract.InferredDeclaration
	signature      string
}

// readDraft pulls req.FromDraft from repo by digest and checks it is a draft
// of req.Service whose signature, when it names an identity, verifies as its
// asker.
func readDraft(ctx context.Context, reg publish.Registry, repo string, req DeclareRequest) (*draftRead, error) {
	ref := repo + ":" + req.FromDraft
	r, err := reg.Head(ctx, ref)
	if err == nil && r != nil && strings.HasPrefix(req.FromDraft, publish.DraftPrefix) {
		var b *contract.Bundle
		if b, err = reg.Pull(ctx, repo+"@"+r.Digest); err == nil {
			return checkDraft(ctx, reg, repo, req, &draftRead{bundle: b, digest: r.Digest})
		}
	}
	if err != nil {
		return nil, &DeclareError{Code: codeRegistryUnreadable, Message: "reading " + ref + ": " + err.Error()}
	}
	return nil, &DeclareError{Code: codeDraftNotFound, Message: "there is no draft " + ref, CLI: "pacto declare " + req.Service + " --registry " + req.Registry}
}

// checkDraft checks dr holds a draft declaration of req.Service and what its
// signature shows.
func checkDraft(ctx context.Context, reg publish.Registry, repo string, req DeclareRequest, dr *draftRead) (*draftRead, error) {
	rec, err := contract.ReadInferredRecord(dr.bundle.FS)
	if err != nil || rec == nil || rec.Declaration == nil || rec.Declaration.State != contract.DeclarationDraft || rec.Inputs.Cluster == nil ||
		dr.bundle.Contract.Service.Name != req.Service {
		return nil, &DeclareError{Code: codeDraftNotFound, Message: repo + ":" + req.FromDraft + " holds no draft declaration of " + req.Service}
	}
	dr.dec, dr.object = rec.Declaration, rec.Inputs.Cluster.Object
	a := dr.dec.Asker
	if a.Subject == "" || a.Issuer == signerKey {
		dr.signature = "the draft's signer names no identity Pacto can check: its proposer is asserted"
		return dr, nil
	}
	tm, err := declareRoot(req.Signing.TrustedRoot)
	if err != nil {
		return nil, err
	}
	got, err := publish.SignersOf(ctx, reg, tm, req.Signing.RekorURL != "", repo, dr.digest)
	if err == nil {
		err = sameSigner(delivery.PublisherIdentity{Subject: a.Subject, Issuer: a.Issuer}, got)
	}
	if err != nil {
		return nil, &DeclareError{Code: codeSignerUnverified, Message: "the draft " + req.FromDraft + " is not signed by the asker it names: " + err.Error()}
	}
	dr.signature = "the draft is signed by " + a.Subject + " (" + a.Issuer + ")"
	return dr, nil
}

// writeTo writes the draft's files into dir, with every range the agent
// requested back to compatibility: unknown, so only a range this run
// accepts is declared.
func (dr *draftRead) writeTo(dir string, write func(string, []byte, os.FileMode) error) error {
	return fs.WalkDir(dr.bundle.FS, ".", func(path string, de fs.DirEntry, err error) error {
		if err != nil || de.IsDir() {
			return err
		}
		data, err := fs.ReadFile(dr.bundle.FS, path)
		for _, r := range dr.dec.Requested {
			if path != DefaultContractPath || err != nil {
				break
			}
			name := strings.TrimSuffix(strings.TrimPrefix(r.Field, "dependencies["), "].compatibility")
			data, err = contract.UnsetCompatibility(data, name, r.Range)
		}
		if err == nil {
			err = os.MkdirAll(filepath.Dir(filepath.Join(dir, path)), 0o755)
		}
		if err == nil {
			err = write(filepath.Join(dir, path), data, 0o644)
		}
		return err
	})
}

// notes say what the draft's signature shows and what the agent requested
// that this run does not accept.
func (dr *draftRead) notes(accept []string) []string {
	out := []string{dr.signature}
	for _, r := range dr.dec.Requested {
		name := strings.TrimSuffix(strings.TrimPrefix(r.Field, "dependencies["), "].compatibility")
		if !slices.Contains(accept, name) {
			out = append(out, "requested by the agent: "+name+" "+r.Range+"; it stays unknown unless you pass --accept "+name)
		}
	}
	return out
}

// unbacked lists each declared change of final (path and value) that
// neither the baseline, this run's cluster read nor an accepted range backs:
// final compared with the declaration this run would make of object from the
// cluster and the baseline alone. When that check cannot run, it lists
// nothing and says why.
func (s *Service) unbacked(ctx context.Context, req DeclareRequest, object string, final *contract.Bundle) ([]string, string) {
	scratch, err := s.env().mkdirTemp("", "pacto-declare-")
	if err != nil {
		return nil, err.Error()
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	ref := req
	ref.Service, ref.Namespace, ref.Dir, ref.FromDraft = object, "", "", ""
	pl := &declarePlanner{s: s, req: ref, scratch: scratch, reg: s.env().publishRegistry(), plan: &DeclarePlan{}}
	raw, err := pl.prepare(ctx)
	var base *contract.Contract
	if err == nil {
		base, err = contract.Parse(bytes.NewReader(raw))
	}
	if err != nil {
		return nil, err.Error()
	}
	out := []string{}
	for _, c := range diff.Compare(ctx, base, final.Contract, os.DirFS(pl.dir), final.FS).Changes {
		if c.Path != "service.version" && !strings.HasPrefix(c.Path, "inferred.") {
			out = append(out, c.Path+" "+fmt.Sprint(cmp.Or(c.NewValue, c.OldValue)))
		}
	}
	if !reflect.DeepEqual(base.Publish, final.Contract.Publish) {
		out = append(out, "publish")
	}
	return out, ""
}
