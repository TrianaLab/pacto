package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/name"

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

// repoInputs are what the repository ladder decides from: the identity the
// baseline counts by, the running image's verified signers and the GitOps
// repositories of the workload, each with why it is empty.
type repoInputs struct {
	Baseline     *delivery.PublisherIdentity
	ImageSigners []delivery.PublisherIdentity
	ImageWhy     string
	GitOps       []fleet.InferredRepository
	GitOpsWhy    string
}

// repoAnswer is the ladder's answer: the repository (nil when no rung gave
// one) and why each rung tried gave nothing.
type repoAnswer struct {
	Repo  *contract.InferredSourceRepository
	Tried []RungNote
}

// resolveRepository is the first rung that names one repository: signed
// provenance (the baseline's CI signer alone, else the running image's CI
// signers when they agree), then the GitOps source. The folder comes from a
// GitOps source of the same repository, else it is the root. An answer is
// inferred: it never changes a verdict, a pin or a gate.
func resolveRepository(in repoInputs) repoAnswer {
	var a repoAnswer
	repo, why := signedRung(in)
	if repo == nil {
		a.Tried = append(a.Tried, RungNote{Rung: contract.RepositoryRungSigned, Reason: why})
		repo, why = gitopsRung(in)
	}
	if repo == nil {
		a.Tried = append(a.Tried, RungNote{Rung: contract.RepositoryRungGitOps, Reason: why})
		return a
	}
	repo.Folder, repo.FolderRung = ".", contract.FolderRungDefault
	for _, r := range in.GitOps {
		if u, ok := normalizeRepoURL(r.URL); ok && u == repo.URL {
			if f := cleanFolder(r.Path); contract.ValidRepositoryFolder(f) {
				repo.Folder, repo.FolderRung = f, contract.RepositoryRungGitOps
			}
			break
		}
	}
	a.Repo = repo
	return a
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

// repository plans the declaration's repository: it collects the ladder's
// inputs, resolves them and writes the answer into the record's
// declaration, or why each rung gave nothing into the plan.
func (pl *declarePlanner) repository(ctx context.Context, dec *contract.InferredDeclaration) {
	a := resolveRepository(pl.collectRepoInputs(ctx))
	pl.plan.Repository, pl.plan.RepositoryTried, dec.Repository = a.Repo, a.Tried, a.Repo
}

// dirReadsNoWorkload is why a --dir run's workload rungs give nothing.
const dirReadsNoWorkload = "--dir reads no workload"

// collectRepoInputs reads what the ladder decides from. A --dir run reads
// no workload. An HTTP surface hands in the caller's restricted view
// (req.Observed), so the cluster is never read with the service's own
// identity; the CLI and stdio read it with the person's kubeconfig.
func (pl *declarePlanner) collectRepoInputs(ctx context.Context) repoInputs {
	in := repoInputs{Baseline: pl.baseSigner}
	if pl.req.Dir != "" {
		in.GitOpsWhy, in.ImageWhy = dirReadsNoWorkload, dirReadsNoWorkload
		return in
	}
	ns, kind, name := splitObject(pl.object)
	var image string
	if o := pl.req.Observed; o != nil {
		if o.Namespace != ns || o.Kind != kind || o.Name != name {
			in.GitOpsWhy, in.ImageWhy = "the workload is not in your fleet view", "the workload is not in your fleet view"
			return in
		}
		in.GitOps, image = o.Repositories, o.Image
	} else {
		w, why := pl.readWorkload(ctx, ns, kind, name)
		if w == nil {
			in.GitOpsWhy, in.ImageWhy = why, why
			return in
		}
		in.GitOps, in.GitOpsWhy = w.Repositories, gitopsWhy(w)
		if w.Image != nil {
			image = w.Image.Value
		}
	}
	in.ImageSigners, in.ImageWhy = pl.imageSigners(ctx, image)
	return in
}

// gitopsWhy is why w's GitOps source names no repository, with where it
// was looked for.
func gitopsWhy(w *fleet.RawInferredService) string {
	if w.RepositoryUnknown == "" {
		return ""
	}
	return w.RepositoryUnknown + " (namespaces read: " + strings.Join(gitopsNamespaces(w.Namespace, *w), ", ") + "; pass --repository <url>)"
}

// readWorkload reads the drafted workload with the person's kubeconfig, in
// its namespace, then once more over the namespaces its GitOps objects live
// in when the first read could not resolve its repository.
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
			if again, _ := read(nss); again != nil {
				w = again
			}
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

// imageSigners are the verified signers of the running image (signatures
// and signed SLSA provenance), read under imageReadTimeout; when there are
// none, why. On an HTTP surface an image outside the installation's
// registry is never read.
func (pl *declarePlanner) imageSigners(ctx context.Context, image string) ([]delivery.PublisherIdentity, string) {
	if image == "" {
		return nil, "the workload names no image"
	}
	ref, err := name.ParseReference(image)
	if err != nil {
		return nil, "the image reference is not readable"
	}
	repo := ref.Context().Name()
	if pl.req.Observed != nil && repo != pl.req.Registry && !strings.HasPrefix(repo, pl.req.Registry+"/") {
		return nil, "image not read on the server: run pacto declare locally"
	}
	digests := pl.imageDigests(ref)
	if len(digests) == 0 {
		return nil, "the running image is not pinned by a digest Pacto read"
	}
	tm, err := declareRoot(pl.req.Signing.TrustedRoot)
	if err != nil {
		return nil, "the Sigstore trusted root was not read: " + err.Error()
	}
	ctx, cancel := context.WithTimeout(ctx, imageReadTimeout)
	defer cancel()
	var signers []delivery.PublisherIdentity
	var why []string
	for _, d := range digests {
		s, err := publish.ProvenanceSigners(ctx, pl.reg, tm, pl.req.Signing.RekorURL != "", repo, d)
		signers = append(signers, s...)
		if len(s) == 0 {
			why = append(why, pl.signersWhy(ctx, repo, d, err))
		}
	}
	if len(signers) > 0 {
		return signers, ""
	}
	return nil, strings.Join(why, "; ")
}

// imageDigests are the image's digests: the one its reference pins, else
// those the cluster read recorded (image.digest[<container>]), at most
// maxImageDigests.
func (pl *declarePlanner) imageDigests(ref name.Reference) []string {
	if d, ok := ref.(name.Digest); ok {
		return []string{d.DigestStr()}
	}
	var out []string
	for _, f := range pl.rec.Fields {
		if !strings.HasPrefix(f.Field, "image.digest[") {
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
