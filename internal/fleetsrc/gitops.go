package fleetsrc

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/trianalab/pacto/v4/internal/k8sclient"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/workloadmanager"
)

// Bounds of the GitOps reads.
const (
	maxGitOpsObjects       = 5000  // objects read per kind
	maxGitOpsStatusEntries = 10000 // status resources or inventory entries kept per object
)

// gitopsKinds are the GitOps kinds Pacto lists, in the order it lists them.
var gitopsKinds = []string{"Application", "Kustomization", "HelmRelease", "GitRepository"}

// discoveryUnknown is the repository reason of every Argo CD or Flux group
// when discovery failed.
const discoveryUnknown = "Pacto could not discover the Argo CD and Flux APIs"

// notServed starts the repository reason of a group whose kind the cluster
// does not serve.
const notServed = "the cluster does not serve "

// The typed items of each GitOps kind hold only the fields Pacto reads: a
// page decodes straight into them, so inline Helm values, post-build
// substitutions and every other field never sit in memory past the page.
type (
	objectMeta struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	}
	objectRef struct {
		Kind      string `json:"kind"`
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	}
	appSource struct {
		RepoURL string `json:"repoURL"`
		Path    string `json:"path"`
		Chart   string `json:"chart"`
	}
	inventoryEntry struct {
		ID string `json:"id"`
	}
	appResource struct {
		Group     string `json:"group"`
		Kind      string `json:"kind"`
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
	}
	application struct {
		Metadata objectMeta `json:"metadata"`
		Spec     struct {
			Source  *appSource  `json:"source"`
			Sources []appSource `json:"sources"`
		} `json:"spec"`
		Status struct {
			Resources []appResource `json:"resources"`
		} `json:"status"`
		truncated bool
	}
	kustomization struct {
		Metadata objectMeta `json:"metadata"`
		Spec     struct {
			SourceRef objectRef `json:"sourceRef"`
			Path      string    `json:"path"`
		} `json:"spec"`
		Status struct {
			Inventory struct {
				Entries []inventoryEntry `json:"entries"`
			} `json:"inventory"`
		} `json:"status"`
		truncated bool
	}
	helmRelease struct {
		Metadata objectMeta `json:"metadata"`
		Spec     struct {
			TargetNamespace string `json:"targetNamespace"`
			Chart           struct {
				Spec struct {
					Chart     string    `json:"chart"`
					SourceRef objectRef `json:"sourceRef"`
				} `json:"spec"`
			} `json:"chart"`
			ChartRef objectRef `json:"chartRef"`
		} `json:"spec"`
	}
	gitRepository struct {
		Metadata objectMeta `json:"metadata"`
		Spec     struct {
			URL string `json:"url"`
		} `json:"spec"`
	}
)

func (m objectMeta) String() string { return qualified(m.Namespace, m.Name) }

// qualified is "ns/name", or name alone with no namespace.
func qualified(ns, name string) string {
	if ns == "" {
		return name
	}
	return ns + "/" + name
}

// gitopsIndex is what the GitOps lists read, keyed for resolveGroup.
type gitopsIndex struct {
	namespaces     []string          // the namespaces the source reads; empty: all
	unknown        map[string]string // kind -> why an object of it Pacto did not read is unknown
	apps           map[objectMeta]*application
	appsNamed      map[string][]*application
	kustomizations map[objectMeta]*kustomization
	helmReleases   map[objectMeta]*helmRelease
	gitRepos       map[objectMeta]*gitRepository
}

func newGitopsIndex(namespaces []string) *gitopsIndex {
	return &gitopsIndex{namespaces: namespaces, unknown: map[string]string{}, apps: map[objectMeta]*application{}, appsNamed: map[string][]*application{},
		kustomizations: map[objectMeta]*kustomization{}, helmReleases: map[objectMeta]*helmRelease{}, gitRepos: map[objectMeta]*gitRepository{}}
}

func (idx *gitopsIndex) addApplication(a application) string {
	a.truncated = len(a.Status.Resources) > maxGitOpsStatusEntries
	a.Status.Resources = slices.Clip(a.Status.Resources[:min(len(a.Status.Resources), maxGitOpsStatusEntries)])
	idx.apps[a.Metadata] = &a
	idx.appsNamed[a.Metadata.Name] = append(idx.appsNamed[a.Metadata.Name], &a)
	return a.Metadata.Namespace
}

func (idx *gitopsIndex) addKustomization(k kustomization) string {
	e := k.Status.Inventory.Entries
	k.truncated = len(e) > maxGitOpsStatusEntries
	k.Status.Inventory.Entries = slices.Clip(e[:min(len(e), maxGitOpsStatusEntries)])
	idx.kustomizations[k.Metadata] = &k
	return k.Metadata.Namespace
}

func (idx *gitopsIndex) addHelmRelease(h helmRelease) string {
	idx.helmReleases[h.Metadata] = &h
	return h.Metadata.Namespace
}

func (idx *gitopsIndex) addGitRepository(r gitRepository) string {
	idx.gitRepos[r.Metadata] = &r
	return r.Metadata.Namespace
}

// kindRead is what listing one GitOps kind gave.
type kindRead struct {
	counts      map[string]int // objects per namespace
	limitations []fleet.Limitation
	why         string // why an object Pacto did not read is unknown; "" when every list was whole
	unserved    bool   // the group is served but not this resource (Argo Rollouts without Argo CD)
}

func (r *kindRead) fail(l fleet.Limitation, why string) {
	r.limitations = append(r.limitations, l)
	r.why = cmp.Or(r.why, why)
}

// gitopsReaders list each GitOps kind into the index.
var gitopsReaders = map[string]func(context.Context, *WorkloadSource, string, schema.GroupVersionResource, *gitopsIndex) kindRead{
	"Application": func(ctx context.Context, s *WorkloadSource, kind string, gvr schema.GroupVersionResource, idx *gitopsIndex) kindRead {
		return readKind(ctx, s, kind, gvr, idx.addApplication)
	},
	"Kustomization": func(ctx context.Context, s *WorkloadSource, kind string, gvr schema.GroupVersionResource, idx *gitopsIndex) kindRead {
		return readKind(ctx, s, kind, gvr, idx.addKustomization)
	},
	"HelmRelease": func(ctx context.Context, s *WorkloadSource, kind string, gvr schema.GroupVersionResource, idx *gitopsIndex) kindRead {
		return readKind(ctx, s, kind, gvr, idx.addHelmRelease)
	},
	"GitRepository": func(ctx context.Context, s *WorkloadSource, kind string, gvr schema.GroupVersionResource, idx *gitopsIndex) kindRead {
		return readKind(ctx, s, kind, gvr, idx.addGitRepository)
	},
}

// readKind pages through gvr in each namespace s reads (or once across all of
// them), hands every object to add, which returns its namespace, and stops
// at maxGitOpsObjects. A failed list or the cap goes on the result, never on
// the workload source.
func readKind[T any](ctx context.Context, s *WorkloadSource, kind string, gvr schema.GroupVersionResource, add func(T) string) kindRead {
	r := kindRead{counts: map[string]int{}}
	total := 0
	for _, ns := range readNamespaces(s.namespaces) {
		for token := ""; ; {
			items, next, err := listItems[T](ctx, s.client, gvr, ns, token)
			switch {
			case apierrors.IsNotFound(err):
				return kindRead{unserved: true}
			case err != nil:
				r.fail(kindUnreadable(s.id, ns, kind, err), fleet.PluralKind(kind)+" unreadable ("+reasonClass(err)+")")
				next = ""
			}
			for _, it := range items {
				if total == maxGitOpsObjects {
					why := "Pacto read only the first " + strconv.Itoa(maxGitOpsObjects) + " " + fleet.PluralKind(kind)
					r.fail(fleet.Limitation{Code: fleet.LimitationSourcePartial, Source: s.id, Message: why}, why)
					return r
				}
				r.counts[add(it)]++
				total++
			}
			if next == "" {
				break
			}
			token = next
		}
	}
	return r
}

// listItems is one page of gvr decoded into T. A decode error names the
// resource only, never the page.
func listItems[T any](ctx context.Context, c k8sclient.K8sClient, gvr schema.GroupVersionResource, ns, token string) ([]T, string, error) {
	data, next, err := c.ListPage(ctx, gvr, ns, token)
	if err != nil {
		return nil, "", fmt.Errorf("listing %s: %w", gvr.Resource, err)
	}
	var page struct {
		Items []T `json:"items"`
	}
	if json.Unmarshal(data, &page) != nil {
		return nil, "", fmt.Errorf("decoding %s list", gvr.Resource)
	}
	return page.Items, next, nil
}

// gitopsGroups are the API groups of the GitOps kinds.
func gitopsGroups() []string {
	var out []string
	for _, kind := range gitopsKinds {
		out = append(out, k8sclient.GitOpsResources[kind].Group)
	}
	return out
}

// readGitOps lists every GitOps kind the cluster serves (versions, from
// discovery) into an index and returns it with the Argo CD and Flux rows.
func (s *WorkloadSource) readGitOps(ctx context.Context, versions map[string]string) (*gitopsIndex, []fleet.Provider) {
	idx := newGitopsIndex(s.namespaces)
	reads := map[string]kindRead{}
	for _, kind := range gitopsKinds {
		gr := k8sclient.GitOpsResources[kind]
		v, ok := versions[gr.Group]
		var r kindRead
		if ok {
			r = gitopsReaders[kind](ctx, s, kind, gr.WithVersion(v), idx)
		}
		if !ok || r.unserved {
			idx.unknown[kind] = notServed + fleet.PluralKind(kind)
			continue
		}
		if r.why != "" {
			idx.unknown[kind] = r.why
		}
		reads[kind] = r
	}
	return idx, gitopsRows(reads)
}

// discoveryFailedIndex is the index when discovery failed: nothing was read
// and every kind says why.
func discoveryFailedIndex(namespaces []string) *gitopsIndex {
	idx := newGitopsIndex(namespaces)
	for _, kind := range gitopsKinds {
		idx.unknown[kind] = discoveryUnknown
	}
	return idx
}

// gitopsRows are the Argo CD row when Applications are served and the Flux
// row when any Flux kind is: one CRD object per served kind and its counts per
// namespace, with a zero count per kind so a served kind with no object reads
// "no Applications found". A failed or capped list makes its kind unreadable
// and the row partial.
func gitopsRows(reads map[string]kindRead) []fleet.Provider {
	var rows []fleet.Provider
	for _, row := range []struct {
		id, name string
		kinds    []string
	}{
		{fleet.ProviderArgoCD, "Argo CD", []string{"Application"}},
		{fleet.ProviderFlux, "Flux", []string{"Kustomization", "HelmRelease", "GitRepository"}},
	} {
		p := fleet.Provider{ID: row.id, Name: row.name, UsedFor: []string{fleet.UseServiceGrouping, fleet.UseRepositoryProvenance}, Counts: []fleet.ProviderCount{}}
		for _, kind := range row.kinds {
			r, ok := reads[kind]
			if !ok {
				continue
			}
			gr := k8sclient.GitOpsResources[kind]
			p.Objects = append(p.Objects, fleet.ProviderObject{Kind: "CustomResourceDefinition", Name: gr.Resource + "." + gr.Group})
			p.Counts = append(p.Counts, fleet.ProviderCount{Kind: kind})
			for _, ns := range slices.Sorted(maps.Keys(r.counts)) {
				p.Counts = append(p.Counts, fleet.ProviderCount{Kind: kind, Namespace: ns, N: r.counts[ns]})
			}
			if r.why != "" {
				p.UnreadableKinds = append(p.UnreadableKinds, kind)
				p.Limitations = append(p.Limitations, r.limitations...)
				p.Status = fleet.SourcePartial
			}
		}
		if len(p.Objects) > 0 {
			rows = append(rows, p)
		}
	}
	return rows
}

// resolveGroup is the group of w with the repositories a GitOps object names
// for it, or why Pacto could not read them. A repository attaches only when
// the object's own status lists the workload: the group comes from metadata
// the workload's writer controls. The group stays as its metadata says,
// except an unprefixed Argo CD name that matches several Applications, which
// leaves the workload ungrouped.
func (idx *gitopsIndex) resolveGroup(w fleet.RawInferredService) (*fleet.InferredGroup, []fleet.InferredRepository, string) {
	if w.Group == nil {
		return nil, nil, ""
	}
	var repos []fleet.InferredRepository
	var why string
	switch w.Group.Rule {
	case workloadmanager.ArgoCD:
		return idx.resolveArgo(w)
	case workloadmanager.FluxKustomization:
		repos, why = idx.resolveKustomization(w)
	case workloadmanager.FluxHelmRelease:
		repos, why = idx.resolveHelmRelease(w)
	}
	return w.Group, repos, why
}

func (idx *gitopsIndex) resolveArgo(w fleet.RawInferredService) (*fleet.InferredGroup, []fleet.InferredRepository, string) {
	g := w.Group
	a := idx.apps[objectMeta{Name: g.Name, Namespace: g.Namespace}]
	if named := idx.appsNamed[g.Name]; g.Namespace == "" && len(named) > 1 {
		// Two Argo CD instances may each hold a cart. The reason names neither
		// namespace nor how many match: some may be in namespaces the viewer
		// may not list Applications in.
		return &fleet.InferredGroup{Rule: workloadmanager.Workload,
			Ambiguous: "Argo CD Application " + g.Name + " matches more than one Application"}, nil, ""
	} else if g.Namespace == "" && len(named) == 1 {
		a = named[0]
	}
	switch {
	case a == nil:
		return g, nil, idx.missing("Application", g.Namespace, g.Name)
	case !a.lists(w):
		return g, nil, notListed("Application", a.Metadata, a.truncated)
	}
	repos, why := a.repositories()
	return g, repos, why
}

func (idx *gitopsIndex) resolveKustomization(w fleet.RawInferredService) ([]fleet.InferredRepository, string) {
	g := w.Group
	k := idx.kustomizations[objectMeta{Name: g.Name, Namespace: g.Namespace}]
	switch {
	case k == nil:
		return nil, idx.missing("Kustomization", g.Namespace, g.Name)
	case !k.lists(w):
		return nil, notListed("Kustomization", k.Metadata, k.truncated)
	}
	return idx.gitRepository(k.Spec.SourceRef, k.Metadata.Namespace, k.Spec.Path)
}

func (idx *gitopsIndex) resolveHelmRelease(w fleet.RawInferredService) ([]fleet.InferredRepository, string) {
	g := w.Group
	h := idx.helmReleases[objectMeta{Name: g.Name, Namespace: g.Namespace}]
	switch {
	case h == nil:
		return nil, idx.missing("HelmRelease", g.Namespace, g.Name)
	case cmp.Or(h.Spec.TargetNamespace, h.Metadata.Namespace) != w.Namespace:
		return nil, "HelmRelease " + h.Metadata.String() + " installs into another namespace"
	case h.Spec.Chart.Spec.SourceRef.Kind == "" && h.Spec.ChartRef.Kind != "":
		return idx.gitRepository(h.Spec.ChartRef, h.Metadata.Namespace, "")
	}
	return idx.gitRepository(h.Spec.Chart.Spec.SourceRef, h.Metadata.Namespace, h.Spec.Chart.Spec.Chart)
}

// gitRepository is the repository of the GitRepository ref names (its
// namespace defaulting to ns), deploying from path.
func (idx *gitopsIndex) gitRepository(ref objectRef, ns, path string) ([]fleet.InferredRepository, string) {
	switch ref.Kind {
	case "GitRepository":
	case "":
		return nil, "no source reference was read"
	default:
		return nil, "the source is " + ref.Kind + " " + ref.Name + ", not a GitRepository"
	}
	m := objectMeta{Name: ref.Name, Namespace: cmp.Or(ref.Namespace, ns)}
	r := idx.gitRepos[m]
	if r == nil {
		return nil, idx.missing("GitRepository", m.Namespace, m.Name)
	}
	u, ok := cleanRepoURL(r.Spec.URL)
	if !ok {
		return nil, "GitRepository " + m.String() + " names no URL Pacto can show"
	}
	return []fleet.InferredRepository{{URL: u, Path: path, From: "GitRepository " + m.String() + " spec.url", Namespace: m.Namespace, Kind: "GitRepository"}}, ""
}

// missing is why an object of kind named ns/name was not found: its kind was
// not read whole, it is outside the namespaces the source reads, or no such
// object was read. An object with no namespace (an unprefixed Argo CD name)
// may live in any namespace, so a scoped source only says it read none.
func (idx *gitopsIndex) missing(kind, ns, name string) string {
	if why := idx.unknown[kind]; why != "" {
		return why
	}
	switch {
	case len(idx.namespaces) == 0:
	case ns == "":
		return "no " + kind + " named " + name + " was read in the namespaces this source reads"
	case !slices.Contains(idx.namespaces, ns):
		return fleet.PluralKind(kind) + " outside the namespaces this source reads"
	}
	return "no " + kind + " named " + qualified(ns, name) + " was read"
}

// unknownFrom is the GitOps objects the repository reason why of w was read
// from: the group's object and, for a Flux group, the GitRepository its
// source reference names. A restricted view shows the reason only to a viewer
// allowed to list each of them. An object with no namespace to check (an
// unprefixed Argo CD name Pacto did not resolve) is kept with an empty
// namespace, which no viewer passes. A reason that names no object, the
// cluster not serving the group's kind or discovery failing, needs none.
func (idx *gitopsIndex) unknownFrom(w fleet.RawInferredService, why string) []fleet.WorkloadScope {
	if why == "" || w.Group == nil {
		return nil
	}
	g := w.Group
	kind := map[string]string{workloadmanager.ArgoCD: "Application", workloadmanager.FluxKustomization: "Kustomization", workloadmanager.FluxHelmRelease: "HelmRelease"}[g.Rule]
	if u := idx.unknown[kind]; why == u && (u == discoveryUnknown || strings.HasPrefix(u, notServed)) {
		return nil
	}
	ns := g.Namespace
	if named := idx.appsNamed[g.Name]; kind == "Application" && ns == "" && len(named) == 1 {
		ns = named[0].Metadata.Namespace
	}
	out := []fleet.WorkloadScope{{Namespace: ns, Kind: kind}}
	m := objectMeta{Name: g.Name, Namespace: g.Namespace}
	var ref objectRef
	switch kind {
	case "Kustomization":
		if k := idx.kustomizations[m]; k != nil {
			ref = k.Spec.SourceRef
		}
	case "HelmRelease":
		if h := idx.helmReleases[m]; h != nil {
			ref = cmp.Or(h.Spec.Chart.Spec.SourceRef, h.Spec.ChartRef)
		}
	}
	if ref.Kind == "GitRepository" {
		out = append(out, fleet.WorkloadScope{Namespace: cmp.Or(ref.Namespace, g.Namespace), Kind: "GitRepository"})
	}
	return out
}

// notListed is why a repository does not attach: the object's status does
// not list the workload, or lists more than Pacto kept.
func notListed(kind string, m objectMeta, truncated bool) string {
	if truncated {
		return "Pacto read only the first " + strconv.Itoa(maxGitOpsStatusEntries) + " entries " + kind + " " + m.String() + " lists"
	}
	return kind + " " + m.String() + " does not list this workload"
}

// lists reports whether the Application's status.resources holds w.
func (a *application) lists(w fleet.RawInferredService) bool {
	return slices.Contains(a.Status.Resources, appResource{Group: k8sclient.WorkloadGVRs[w.Kind].Group, Kind: w.Kind, Namespace: w.Namespace, Name: w.Name})
}

// lists reports whether the Kustomization's inventory holds w.
func (k *kustomization) lists(w fleet.RawInferredService) bool {
	id := strings.Join([]string{w.Namespace, w.Name, k8sclient.WorkloadGVRs[w.Kind].Group, w.Kind}, "_")
	return slices.Contains(k.Status.Inventory.Entries, inventoryEntry{ID: id})
}

// repositories are the Git repositories of the Application's spec.source and
// spec.sources, deduplicated in order. A source naming a chart is a Helm or
// OCI chart repository, never a Git one.
func (a *application) repositories() ([]fleet.InferredRepository, string) {
	type field struct {
		src  appSource
		name string
	}
	var fields []field
	if a.Spec.Source != nil {
		fields = append(fields, field{*a.Spec.Source, "spec.source"})
	}
	for i, src := range a.Spec.Sources {
		fields = append(fields, field{src, "spec.sources[" + strconv.Itoa(i) + "]"})
	}
	var repos []fleet.InferredRepository
	chart := ""
	for _, f := range fields {
		u, ok := cleanRepoURL(f.src.RepoURL)
		switch {
		case f.src.Chart != "":
			chart = cmp.Or(chart, f.src.Chart)
		// pkg/fleet keeps the first fleet.MaxInferredRepositories and records
		// every later one as dropped, so the source sends them all.
		case !ok || slices.ContainsFunc(repos, func(r fleet.InferredRepository) bool { return r.URL == u && r.Path == f.src.Path }):
		default:
			repos = append(repos, fleet.InferredRepository{URL: u, Path: f.src.Path, From: "Application " + a.Metadata.String() + " " + f.name + ".repoURL",
				Namespace: a.Metadata.Namespace, Kind: "Application"})
		}
	}
	switch {
	case len(repos) > 0:
		return repos, ""
	case chart != "":
		return nil, "the source is Helm chart " + chart + ", not a Git repository"
	}
	return nil, "Application " + a.Metadata.String() + " names no Git repository Pacto can show"
}

// cleanRepoURL is raw without credentials, query or fragment: a URL with a
// scheme keeps scheme, host and path; an scp-like user@host:path keeps
// host:path. Anything else, a control character included, is refused.
func cleanRepoURL(raw string) (string, bool) {
	if raw == "" || strings.ContainsFunc(raw, unicode.IsControl) {
		return "", false
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return "", false
		}
		u.User, u.RawQuery, u.ForceQuery, u.Fragment, u.RawFragment = nil, "", false, "", ""
		return u.String(), true
	}
	// The userinfo ends at the last '@' before the path's first '/', a
	// user:password@ included, so the host:path split never sees it.
	rest, end := raw, strings.IndexByte(raw, '/')
	if end < 0 {
		end = len(raw)
	}
	if i := strings.LastIndex(raw[:end], "@"); i >= 0 {
		rest = rest[i+1:]
	}
	host, path, ok := strings.Cut(rest, ":")
	path, _, _ = strings.Cut(path, "?")
	path, _, _ = strings.Cut(path, "#")
	if !ok || host == "" || path == "" || strings.ContainsAny(host, "/ @") {
		return "", false
	}
	return host + ":" + path, true
}
