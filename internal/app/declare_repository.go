package app

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// imageReadTimeout bounds every image read of the repository rungs
// together: an unreachable registry is a reason, never a wait.
var imageReadTimeout = 5 * time.Second

// maxImageDigests is how many digests of the running image are read.
const maxImageDigests = 2

// RungNote is why one rung of the repository ladder gave nothing.
type RungNote struct {
	Rung   string `json:"rung"`
	Reason string `json:"reason"`
}

// repoInputs are what the repository ladder decides from: the person's own
// answer, the identity the baseline counts by, the running image's verified
// signers, the workload's GitOps repositories, the image's source label and
// the repository map's entry, each with why it is empty; and whether the run
// runs on an agent's behalf.
type repoInputs struct {
	Given        givenRepo
	Baseline     *delivery.PublisherIdentity
	ImageSigners []delivery.PublisherIdentity
	ImageWhy     string
	GitOps       []fleet.InferredRepository
	GitOpsWhy    string
	Label        string
	LabelWhy     string
	Map          *repoMapEntry
	MapWhy       string
	Agent        bool
}

// givenRepo is the person's own answer (--repository, --folder): the URL
// normalized and the folder cleaned.
type givenRepo struct{ URL, Folder string }

// repoMapEntry is one service of a repository map file.
type repoMapEntry struct {
	Repository string `yaml:"repository"`
	Folder     string `yaml:"folder"`
}

// maxRepoMap bounds a repository map file.
const maxRepoMap = 1 << 20

// repoAnswer is the ladder's answer: the repository (nil when no rung gave
// one) and why each rung tried gave nothing.
type repoAnswer struct {
	Repo  *contract.InferredSourceRepository
	Tried []RungNote
}

// repoRung is one rung of the ladder: the repository it names, else why
// it names none.
type repoRung struct {
	name string
	find func(repoInputs) (*contract.InferredSourceRepository, string)
}

// repoRungs are the ladder after the person's own answer, in order.
var repoRungs = []repoRung{
	{contract.RepositoryRungSigned, signedRung}, {contract.RepositoryRungGitOps, gitopsRung},
	{contract.RepositoryRungImageLabel, labelRung}, {contract.RepositoryRungMap, mapRung},
}

// resolveRepository is the first rung that names one repository: the
// person's --repository, signed provenance (the baseline's CI signer alone,
// else the running image's CI signers when they agree), the GitOps source,
// the image's source label, then the repository map. The folder is the
// person's --folder, else that of a GitOps source or the map entry of the
// same repository, else the root. An answer is inferred: it never changes a
// verdict, a pin or a gate.
func resolveRepository(in repoInputs) repoAnswer {
	var a repoAnswer
	var repo *contract.InferredSourceRepository
	if in.Given.URL != "" {
		repo = &contract.InferredSourceRepository{URL: in.Given.URL, Rung: contract.RepositoryRungGiven}
	}
	for _, r := range repoRungs {
		if repo != nil {
			break
		}
		var why string
		if repo, why = r.find(in); repo == nil {
			a.Tried = append(a.Tried, RungNote{Rung: r.name, Reason: why})
		}
	}
	if repo == nil {
		return a
	}
	repo.Folder, repo.FolderRung = folderOf(in, repo.URL)
	a.Repo = repo
	return a
}

// folderOf is the folder of the repository url and how it was found.
func folderOf(in repoInputs, url string) (string, string) {
	if in.Given.Folder != "" {
		return in.Given.Folder, contract.RepositoryRungGiven
	}
	if f := gitopsFolders(in.GitOps, url); len(f) == 1 {
		return f[0], contract.RepositoryRungGitOps
	}
	if m := in.Map; m != nil && m.Repository == url && m.Folder != "" {
		return m.Folder, contract.RepositoryRungMap
	}
	return ".", contract.FolderRungDefault
}

// gitopsFolders are the distinct folders the GitOps repositories of url
// name; an empty path is the root.
func gitopsFolders(repos []fleet.InferredRepository, url string) []string {
	var out []string
	for _, r := range repos {
		if u, ok := normalizeRepoURL(r.URL); ok && u == url {
			if f := cleanFolder(r.Path); contract.ValidRepositoryFolder(f) && !slices.Contains(out, f) {
				out = append(out, f)
			}
		}
	}
	return out
}

// labelRung is the repository the running image's
// org.opencontainers.image.source label names. The label is unsigned, so a
// pull request needs a person to confirm it.
func labelRung(in repoInputs) (*contract.InferredSourceRepository, string) {
	if in.Label == "" {
		return nil, cmp.Or(in.LabelWhy, "no image label names a repository")
	}
	u, ok := normalizeRepoURL(in.Label)
	if !ok {
		return nil, "the image label is not a repository URL"
	}
	return &contract.InferredSourceRepository{URL: u, Rung: contract.RepositoryRungImageLabel}, ""
}

// mapRung is the repository the repository map names for the service.
func mapRung(in repoInputs) (*contract.InferredSourceRepository, string) {
	if in.Map == nil {
		return nil, cmp.Or(in.MapWhy, "no --repo-map given")
	}
	return &contract.InferredSourceRepository{URL: in.Map.Repository, Rung: contract.RepositoryRungMap}, ""
}

// mapNotRead is why a repository map given without --pr gave nothing.
const mapNotRead = "repository map not read (only with --pr)"

// readRepoMap is service's entry of the repository map file at path: YAML
// keyed by service name, decoded strictly, at most maxRepoMap bytes, its
// URL normalized and its folder cleaned. Nil, and why, when the map does
// not name the service.
func readRepoMap(path, service string) (*repoMapEntry, string, error) {
	f, err := os.Open(path) //nolint:gosec // the person's own file
	if err != nil {
		return nil, "", fmt.Errorf("reading the repository map: %w", err)
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, maxRepoMap+1))
	if err == nil && len(raw) > maxRepoMap {
		err = fmt.Errorf("it is larger than %d bytes", maxRepoMap)
	}
	var m map[string]repoMapEntry
	if err == nil {
		dec := yaml.NewDecoder(bytes.NewReader(raw))
		dec.KnownFields(true)
		if err = dec.Decode(&m); errors.Is(err, io.EOF) {
			err = nil
		}
	}
	if err != nil {
		return nil, "", fmt.Errorf("the repository map %s: %w", path, err)
	}
	e, ok := m[service]
	if !ok {
		return nil, "the repository map does not name " + service, nil
	}
	u, ok := normalizeRepoURL(e.Repository)
	if !ok {
		return nil, "", fmt.Errorf("the repository map %s: %s.repository %q is not a repository URL", path, service, e.Repository)
	}
	folder, err := givenFolder(e.Folder)
	if err != nil {
		return nil, "", fmt.Errorf("the repository map %s: %s.folder: %w", path, service, err)
	}
	return &repoMapEntry{Repository: u, Folder: folder}, "", nil
}

// givenFolder is a folder a person wrote, cleaned; empty stays empty.
func givenFolder(f string) (string, error) {
	if f == "" {
		return "", nil
	}
	c := cleanFolder(f)
	if !contract.ValidRepositoryFolder(c) {
		return "", fmt.Errorf("%q is not a folder inside the repository", f)
	}
	return c, nil
}

// givenRepository is req's --repository and --folder, checked.
func givenRepository(req DeclareRequest) (givenRepo, error) {
	var g givenRepo
	if req.Repository != "" {
		u, ok := normalizeRepoURL(req.Repository)
		if !ok {
			return g, fmt.Errorf("--repository %q is not a repository URL", req.Repository)
		}
		g.URL = u
	}
	f, err := givenFolder(req.Folder)
	if err != nil {
		return g, fmt.Errorf("--folder: %w", err)
	}
	g.Folder = f
	return g, nil
}

// prCheck refuses a pull request to a repository nobody confirmed: no
// answer at all, an unsigned image label no person confirmed (with the same
// --repository or a map entry; an agent never confirms its own), or an
// agent's repository or map entry that no signed provenance or GitOps
// source names. cli is the command a person runs with --repository url.
func prCheck(in repoInputs, a repoAnswer, cli func(url string) string) error {
	r := a.Repo
	switch {
	case r == nil:
		return &DeclareError{Code: codeRepositoryUnknown, CLI: cli("<url>"),
			Message: "the pull request needs the service's repository and no source named it: pass --repository <url> or add the service to --repo-map"}
	case in.Agent && (r.Rung == contract.RepositoryRungGiven || r.Rung == contract.RepositoryRungMap) && !corroborated(in, r.URL):
		return &DeclareError{Code: codeRepositoryUnconfirmed, CLI: cli(r.URL),
			Message: "no signed provenance or GitOps source names " + r.URL + ": an agent's repository needs a person to confirm it with --repository " + r.URL}
	case r.Rung == contract.RepositoryRungImageLabel && (in.Agent || in.Map == nil || in.Map.Repository != r.URL):
		return &DeclareError{Code: codeRepositoryUnconfirmed, CLI: cli(r.URL), Message: "the image label is unsigned: confirm with --repository " + r.URL}
	}
	return nil
}

// corroborated is whether signed provenance or the GitOps source names url.
func corroborated(in repoInputs, url string) bool {
	s, _ := signedRung(in)
	g, _ := gitopsRung(in)
	return (s != nil && s.URL == url) || (g != nil && g.URL == url)
}

// cleanFolder is a GitOps path as a record's folder: "" and "." are the root.
func cleanFolder(p string) string {
	return path.Clean(strings.Trim(p, "/"))
}

// signedRung is the repository a verified CI certificate names. The
// baseline's signer already passed the installation's policy and answers
// alone; the image's CI signers answer only when they name one repository,
// since anyone who can push a referrer can attach a signature of their own.
func signedRung(in repoInputs) (*contract.InferredSourceRepository, string) {
	if r := ciRepository(in.Baseline); r != nil {
		return r, ""
	}
	var found []*contract.InferredSourceRepository
	for i := range in.ImageSigners {
		if r := ciRepository(&in.ImageSigners[i]); r != nil &&
			!slices.ContainsFunc(found, func(f *contract.InferredSourceRepository) bool { return f.URL == r.URL }) {
			found = append(found, r)
		}
	}
	switch {
	case len(found) == 1:
		return found[0], ""
	case len(found) > 1:
		return nil, fmt.Sprintf("%d signed repositories", len(found))
	}
	image := in.ImageWhy
	if image == "" {
		image = "no CI signature names a repository"
	}
	base := "no published baseline"
	if in.Baseline != nil {
		base = "its signer names no repository"
	}
	return nil, "baseline: " + base + "; image: " + image
}

// ciRepository is the repository a CI identity's certificate names; nil for
// a person (whose signature names none) or a value a record cannot hold.
func ciRepository(id *delivery.PublisherIdentity) *contract.InferredSourceRepository {
	if id == nil || !id.CI || id.Subject == "" || len(id.Subject) > 512 || strings.ContainsFunc(id.Subject, isControl) {
		return nil
	}
	u, ok := normalizeRepoURL(id.Repository)
	if !ok {
		return nil
	}
	return &contract.InferredSourceRepository{URL: u, Rung: contract.RepositoryRungSigned, Signer: id.Subject}
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

// gitopsRung is the one repository the workload's GitOps objects name;
// several make it ambiguous.
func gitopsRung(in repoInputs) (*contract.InferredSourceRepository, string) {
	var urls []string
	for _, r := range in.GitOps {
		if u, ok := normalizeRepoURL(r.URL); ok && !slices.Contains(urls, u) {
			urls = append(urls, u)
		}
	}
	switch {
	case len(urls) == 1:
		return &contract.InferredSourceRepository{URL: urls[0], Rung: contract.RepositoryRungGitOps}, ""
	case len(urls) > 1:
		return nil, fmt.Sprintf("%d GitOps repositories", len(urls))
	case in.GitOpsWhy != "":
		return nil, in.GitOpsWhy
	}
	return nil, "no GitOps source lists this workload"
}

// normalizeRepoURL is raw as a record's repository URL: scp-like host:path,
// ssh:// and git:// become https://host/path (userinfo and an ssh port
// dropped), an http(s) URL keeps its port, the host is lowercased and a
// trailing .git and / are trimmed. Anything a record cannot hold is refused.
func normalizeRepoURL(raw string) (string, bool) {
	u, ok := fleetsrc.CleanRepoURL(strings.TrimSpace(raw))
	if !ok {
		return "", false
	}
	scheme, host, p := "https", "", ""
	if s, _, found := strings.Cut(u, "://"); found {
		pu, _ := url.Parse(u) // CleanRepoURL parsed it
		switch s {
		case "https", "http":
			scheme, host = s, pu.Host
		case "ssh", "git", "git+ssh":
			host = pu.Hostname()
		default:
			return "", false
		}
		p = pu.Path
	} else {
		host, p, _ = strings.Cut(u, ":")
	}
	p = strings.TrimSuffix(strings.Trim(p, "/"), ".git")
	out := scheme + "://" + strings.ToLower(host) + "/" + strings.TrimRight(p, "/")
	return out, contract.ValidRepositoryURL(out)
}

// RepositoryText is a plan's repository line: the URL without its scheme,
// how it was found (with the CI workflow for signed provenance) and the
// folder with how it was found.
func RepositoryText(r *contract.InferredSourceRepository) string {
	how := rungWords(r.Rung)
	if r.Signer != "" {
		how += ", " + signerLabel(r.Signer)
	}
	folder := r.Folder
	if folder != "." {
		folder += "/"
	}
	_, host, _ := strings.Cut(r.URL, "://")
	return fmt.Sprintf("%s (%s), folder: %s (%s)", host, how, folder, rungWords(r.FolderRung))
}

// rungWords is a rung in words.
func rungWords(rung string) string {
	return map[string]string{
		contract.RepositoryRungGiven: "given", contract.RepositoryRungSigned: "signed provenance", contract.RepositoryRungGitOps: "GitOps source",
		contract.RepositoryRungImageLabel: "image label", contract.RepositoryRungMap: "repository map",
		contract.FolderRungDefault: "no source names the folder; pass --folder",
	}[rung]
}

// signerLabel is a CI signer's subject as the plan names it: the GitHub
// workflow or the GitLab pipeline file at its ref, else the subject.
func signerLabel(subject string) string {
	if _, wf, ok := strings.Cut(subject, "/.github/workflows/"); ok {
		return "workflow " + wf
	}
	if _, rest, ok := strings.Cut(subject, "://"); ok {
		if _, file, ok := strings.Cut(rest, "//"); ok {
			return "pipeline " + file
		}
	}
	return subject
}

// repository plans the declaration's repository: it reads the person's
// answer and, with --pr, the repository map, collects the ladder's inputs,
// resolves them and writes the answer into the record's declaration, or why
// each rung gave nothing into the plan. With --pr it refuses a repository
// nobody confirmed and binds the target into the intent.
func (pl *declarePlanner) repository(ctx context.Context, dec *contract.InferredDeclaration) error {
	given, err := givenRepository(pl.req)
	if err != nil {
		return err
	}
	var m *repoMapEntry
	mapWhy := ""
	switch {
	case pl.req.RepoMap != "" && pl.req.PR:
		if m, mapWhy, err = readRepoMap(pl.req.RepoMap, pl.plan.Service); err != nil {
			return err
		}
	case pl.req.RepoMap != "":
		mapWhy = mapNotRead
		pl.plan.Notes = append(pl.plan.Notes, mapNotRead)
	}
	in := pl.collectRepoInputs(ctx, given)
	in.Map, in.MapWhy, in.Agent = m, mapWhy, pl.req.Agent
	a := resolveRepository(in)
	pl.plan.Repository, pl.plan.RepositoryTried, dec.Repository = a.Repo, a.Tried, a.Repo
	if r := a.Repo; r != nil && r.FolderRung == contract.FolderRungDefault {
		if f := gitopsFolders(in.GitOps, r.URL); len(f) > 1 {
			pl.plan.Notes = append(pl.plan.Notes, fmt.Sprintf("%d GitOps folders of %s (%s): pass --folder", len(f), r.URL, strings.Join(f, ", ")))
		}
	}
	if !pl.req.PR {
		return nil
	}
	if err := prCheck(in, a, pl.confirmCLI); err != nil {
		return err
	}
	pl.plan.pr = a.Repo.URL + "#" + a.Repo.Folder
	return nil
}

// confirmCLI is the command a person runs to open the pull request to url.
func (pl *declarePlanner) confirmCLI(url string) string {
	r := pl.req
	r.PR, r.Repository, r.RepoMap = true, url, ""
	if r.draft != "" {
		r.Dir, r.FromDraft = "", r.draft
	}
	return r.command("")
}

// dirReadsNoWorkload is why a --dir run's workload rungs give nothing.
const dirReadsNoWorkload = "--dir reads no workload"

// collectRepoInputs reads what the ladder decides from. A --dir run reads
// no workload. An HTTP surface hands in the caller's restricted view
// (req.Observed), so the cluster is never read with the service's own
// identity; the CLI and stdio read it with the person's kubeconfig. Every
// image read shares one imageReadTimeout, and the image's label is read
// only when no earlier rung answers.
func (pl *declarePlanner) collectRepoInputs(ctx context.Context, given givenRepo) repoInputs {
	in := repoInputs{Given: given, Baseline: pl.baseSigner}
	if pl.req.Template != nil {
		in.GitOpsWhy, in.ImageWhy, in.LabelWhy = templateReadsNoWorkload, templateReadsNoWorkload, templateReadsNoWorkload
		return in
	}
	if pl.req.Create != nil {
		in.GitOpsWhy, in.ImageWhy, in.LabelWhy = noWorkloadYet, noWorkloadYet, noWorkloadYet
		return in
	}
	if pl.req.Dir != "" && pl.req.draft == "" {
		in.GitOpsWhy, in.ImageWhy, in.LabelWhy = dirReadsNoWorkload, dirReadsNoWorkload, dirReadsNoWorkload
		return in
	}
	ns, kind, name := splitObject(pl.object)
	if o := pl.req.Observed; o != nil {
		pl.observedInputs(&in, o, ns, kind, name)
		return in
	}
	w, why := pl.readWorkload(ctx, ns, kind, name)
	if w == nil {
		in.GitOpsWhy, in.ImageWhy, in.LabelWhy = why, why, why
		return in
	}
	in.GitOps, in.GitOpsWhy = w.Repositories, cmp.Or(why, gitopsWhy(w))
	var img runningImage
	if w.Image != nil {
		img = runningImage{w.Image.Value, containerOf(w.Image.From)}
	}
	ctx, cancel := context.WithTimeout(ctx, imageReadTimeout)
	defer cancel()
	in.ImageSigners, in.ImageWhy = pl.imageSigners(ctx, img)
	if s, _ := signedRung(in); s == nil && given.URL == "" {
		if g, _ := gitopsRung(in); g == nil {
			in.Label, in.LabelWhy = pl.imageLabel(ctx, img)
		}
	}
	return in
}

// imageNotReadOnServer is why an HTTP surface's image rungs give nothing:
// the caller brings no registry credential of its own, and the service's
// would read images the caller may not pull.
const imageNotReadOnServer = "image not read on the server: run pacto declare locally"

// observedInputs fills in from what an HTTP surface's caller sees of the
// workload: its GitOps repositories alone, none when the view lost some
// (hidden or past the bound) or shows the workload in another cluster than
// the one the plan read. The image is never read on the server.
func (pl *declarePlanner) observedInputs(in *repoInputs, o *DeclareObserved, ns, kind, name string) {
	in.ImageWhy, in.LabelWhy = imageNotReadOnServer, imageNotReadOnServer
	switch {
	case o.Namespace != ns || o.Kind != kind || o.Name != name ||
		o.Cluster != "" && pl.plan.Cluster != "" && o.Cluster != pl.plan.Cluster:
		in.GitOpsWhy = "the workload is not in your fleet view"
	case o.RepositoryUnknown != "":
		in.GitOpsWhy = o.RepositoryUnknown + ": run pacto declare locally"
	default:
		in.GitOps = o.Repositories
	}
}

// runningImage is the workload's image reference and the container that
// runs it, "" when Pacto does not know which.
type runningImage struct{ ref, container string }

// containerOf is the container an image's object path names
// (...containers[name=<c>].image), "" when it names none.
func containerOf(from string) string {
	i := strings.LastIndex(from, "containers[name=")
	if i < 0 {
		return ""
	}
	c, _, ok := strings.Cut(from[i+len("containers[name="):], "]")
	if !ok {
		return ""
	}
	return c
}

// gitopsWhy is why w's GitOps source names no repository, with where it
// was looked for.
func gitopsWhy(w *fleet.RawInferredService) string {
	if w.RepositoryUnknown == "" {
		return ""
	}
	return w.RepositoryUnknown + " (namespaces read: " + strings.Join(gitopsNamespaces(w.Namespace, *w), ", ") + "; pass --repository <url>)"
}

// gitopsUnread is why the GitOps source names no repository when the second
// read failed: only the workload's namespace was read.
func gitopsUnread(w *fleet.RawInferredService, nss []string, why string) string {
	return w.RepositoryUnknown + " (namespaces read: " + w.Namespace + "; " + strings.Join(nss[1:], ", ") + " not read: " + why + "; pass --repository <url>)"
}

// readWorkload reads the drafted workload with the person's kubeconfig, in
// its namespace, then once more over the namespaces its GitOps objects live
// in when the first read could not resolve its repository. With the
// workload read, the reason is the GitOps reason of a second read that
// failed, else "".
func (pl *declarePlanner) readWorkload(ctx context.Context, ns, kind, name string) (*fleet.RawInferredService, string) {
	client, err := pl.s.env().newK8s("")
	if err != nil {
		return nil, "the cluster was not read for the repository: " + err.Error()
	}
	read := func(nss []string) (*fleet.RawInferredService, string) {
		col, err := fleetsrc.NewWorkloadSource("declare", nil, client, nss).Collect(ctx)
		if err != nil {
			return nil, "the workload was not read for the repository: " + err.Error()
		}
		for i, w := range col.InferredServices {
			if w.Namespace == ns && w.Kind == kind && w.Name == name {
				return &col.InferredServices[i], ""
			}
		}
		return nil, "the workload was not read for the repository"
	}
	w, why := read([]string{ns})
	if w != nil && w.Group != nil && w.RepositoryUnknown != "" {
		if nss := gitopsNamespaces(ns, *w); len(nss) > 1 {
			again, why2 := read(nss)
			if again == nil {
				return w, gitopsUnread(w, nss, why2)
			}
			w = again
		}
	}
	return w, why
}

// gitopsNamespaces are the namespaces a second read lists: the workload's,
// those of the GitOps objects its repository reason names and its group's,
// with argocd and flux-system for a group whose namespace is unknown.
func gitopsNamespaces(ns string, w fleet.RawInferredService) []string {
	out := []string{ns}
	add := func(n string) {
		if n != "" && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	for _, s := range w.RepositoryUnknownFrom {
		add(s.Namespace)
	}
	if w.Group != nil {
		add(w.Group.Namespace)
		if w.Group.Namespace == "" {
			add("argocd")
			add("flux-system")
		}
	}
	return out
}

// imageTarget is the running image's repository and the digests its rungs
// read; when none is read, why. An HTTP surface never reads it.
func (pl *declarePlanner) imageTarget(img runningImage) (string, []string, string) {
	if pl.req.Observed != nil {
		return "", nil, imageNotReadOnServer
	}
	if img.ref == "" {
		return "", nil, "the workload names no image"
	}
	ref, err := name.ParseReference(img.ref)
	if err != nil {
		return "", nil, "the image reference is not readable"
	}
	repo := ref.Context().Name()
	digests := pl.imageDigests(ref, img.container)
	if len(digests) == 0 {
		return "", nil, "the running image is not pinned by a digest Pacto read"
	}
	return repo, digests, ""
}

// imageSigners are the verified signers of the running image (signatures
// and signed SLSA provenance); when there are none, why. They answer only
// from a complete read of every digest: a signature left unread may name
// another repository.
func (pl *declarePlanner) imageSigners(ctx context.Context, img runningImage) ([]delivery.PublisherIdentity, string) {
	repo, digests, why := pl.imageTarget(img)
	if why != "" {
		return nil, why
	}
	tm, err := declareRoot(pl.req.Signing.TrustedRoot)
	if err != nil {
		return nil, "the Sigstore trusted root was not read: " + err.Error()
	}
	var signers []delivery.PublisherIdentity
	var whys []string
	partial := false
	for _, d := range digests {
		s, err := publish.ProvenanceSigners(ctx, pl.reg, tm, pl.req.Signing.RekorURL != "", repo, d)
		signers = append(signers, s...)
		if err != nil || len(s) == 0 {
			partial = partial || !errors.Is(err, publish.ErrNoSignature) && !errors.Is(err, publish.ErrSignatureInvalid)
			whys = append(whys, pl.signersWhy(ctx, repo, d, err))
		}
	}
	if len(signers) > 0 && !partial {
		return signers, ""
	}
	return nil, strings.Join(whys, "; ")
}

// imageLabel is the first org.opencontainers.image.source the running
// image's digests name (an index annotation, else one platform's config
// label); when none does, why. A read error is a reason, never an error.
func (pl *declarePlanner) imageLabel(ctx context.Context, img runningImage) (string, string) {
	repo, digests, why := pl.imageTarget(img)
	if why != "" {
		return "", why
	}
	var errs []string
	for _, d := range digests {
		src, err := pl.reg.ImageSource(ctx, repo+"@"+d)
		switch {
		case ctx.Err() != nil:
			return "", "the image read timed out after " + imageReadTimeout.String()
		case err != nil:
			errs = append(errs, "the image was not read: "+err.Error())
		case src != "":
			return src, ""
		}
	}
	if len(errs) > 0 {
		return "", strings.Join(errs, "; ")
	}
	return "", "the image names no org.opencontainers.image.source label"
}

// imageDigests are the image's digests: the one its reference pins, else
// those the cluster read recorded for the container that runs it
// (image.digest[<container>]), at most maxImageDigests. Another
// container's digest is never read against this image's repository.
func (pl *declarePlanner) imageDigests(ref name.Reference, container string) []string {
	if d, ok := ref.(name.Digest); ok {
		return []string{d.DigestStr()}
	}
	var out []string
	for _, f := range pl.rec.Fields {
		if container == "" || f.Field != "image.digest["+container+"]" {
			continue
		}
		for d := range strings.SplitSeq(f.Value, ", ") {
			if len(out) < maxImageDigests && !slices.Contains(out, d) {
				out = append(out, d)
			}
		}
	}
	return out
}

// signersWhy is why repo@digest gave no signer, never read as unsigned
// when it is unknown: a timeout, no referrer (legacy cosign tags and, on a
// registry without the Referrers API, the sha256- tag schema are not read)
// or the read's error.
func (pl *declarePlanner) signersWhy(ctx context.Context, repo, digest string, err error) string {
	switch {
	case ctx.Err() != nil:
		return "the image read timed out after " + imageReadTimeout.String()
	case !errors.Is(err, publish.ErrNoSignature):
		return "the image's signatures were not read: " + err.Error()
	}
	why := "no Sigstore bundle referrer (legacy cosign .sig tags are not read)"
	if mode, err := pl.reg.Mode(ctx, repo, digest); err == nil && mode != capabilities.ReferrersNative {
		why += "; the registry has no Referrers API; attestations under the sha256- tag schema are not read"
	}
	return why
}
