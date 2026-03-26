package dashboard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/trianalab/pacto/internal/oci"
)

// DetectOptions configures source auto-detection.
type DetectOptions struct {
	Dir       string          // working directory for local detection
	Namespace string          // k8s namespace (empty = all namespaces)
	Repos     []string        // OCI repositories to scan
	Store     oci.BundleStore // OCI client (may be nil)
	CacheDir  string          // OCI cache directory (defaults to ~/.cache/pacto/oci)
	NoCache   bool            // disable the cache-based OCI source entirely
}

// DetectResult holds the outcome of source detection.
type DetectResult struct {
	Sources []SourceInfo
	Local   *LocalSource
	OCI     *OCISource
	K8s     *K8sSource
	Cache   *CacheSource // internal: used by OCI for offline access, not a public source

	// Diagnostics collected during detection.
	Diagnostics *SourceDiagnostics
}

// SourceDiagnostics provides detailed diagnostic information about source detection.
type SourceDiagnostics struct {
	K8s   K8sDiagnostics   `json:"k8s"`
	OCI   OCIDiagnostics   `json:"oci"`
	Cache CacheDiagnostics `json:"cache"`
	Local LocalDiagnostics `json:"local"`
}

// K8sDiagnostics contains K8s source detection details.
type K8sDiagnostics struct {
	ClientConfigured bool     `json:"clientConfigured"`
	KubeconfigPath   string   `json:"kubeconfigPath,omitempty"`
	ClusterReachable bool     `json:"clusterReachable"`
	CRDExists        bool     `json:"crdExists"`
	Namespace        string   `json:"namespace"`
	AllNamespaces    bool     `json:"allNamespaces"`
	ResourceCount    int      `json:"resourceCount"`
	DetectedGroup    string   `json:"detectedGroup,omitempty"`
	DetectedVersions []string `json:"detectedVersions,omitempty"`
	ChosenVersion    string   `json:"chosenVersion,omitempty"`
	ResourceName     string   `json:"resourceName,omitempty"`
	Error            string   `json:"error,omitempty"`
}

// OCIDiagnostics contains OCI registry source detection details.
type OCIDiagnostics struct {
	StoreConfigured bool     `json:"storeConfigured"`
	Repos           []string `json:"repos,omitempty"`
	Error           string   `json:"error,omitempty"`
}

// CacheDiagnostics contains OCI disk cache detection details.
type CacheDiagnostics struct {
	CacheDir     string `json:"cacheDir"`
	Exists       bool   `json:"exists"`
	OCIDirExists bool   `json:"ociDirExists"`
	ServiceCount int    `json:"serviceCount"`
	VersionCount int    `json:"versionCount"`
	Error        string `json:"error,omitempty"`
}

// LocalDiagnostics contains local source detection details.
type LocalDiagnostics struct {
	Dir            string `json:"dir"`
	PactoYamlFound bool   `json:"pactoYamlFound"`
	FoundIn        string `json:"foundIn,omitempty"`
	Error          string `json:"error,omitempty"`
}

// DetectSources probes for available data sources and returns all that are reachable.
func DetectSources(ctx context.Context, opts DetectOptions) *DetectResult {
	result := &DetectResult{
		Diagnostics: &SourceDiagnostics{},
	}

	// Local: check if dir contains pacto.yaml (root or subdirectories).
	result.detectLocal(opts.Dir)

	// K8s: check if cluster is reachable using Go client.
	result.detectK8s(ctx, opts.Namespace)

	// OCI registry: check if store is configured and repos are provided.
	result.detectOCI(opts.Store, opts.Repos)

	// OCI disk cache: internal backing store for offline access.
	// Not exposed as a public source in the UI.
	if !opts.NoCache {
		result.detectCache(opts.CacheDir)
	}

	return result
}

// ActiveSources returns the DataSource instances that were successfully detected.
// Cache is NOT included as a separate source — it is an internal implementation
// detail of OCI used for offline access and version history.
func (r *DetectResult) ActiveSources() map[string]DataSource {
	sources := make(map[string]DataSource)
	if r.Local != nil {
		sources["local"] = r.Local
	}
	if r.K8s != nil {
		sources["k8s"] = r.K8s
	}
	// OCI gets the live registry source; cache provides offline backing
	// but is not exposed as a separate public source.
	if r.OCI != nil {
		sources["oci"] = r.OCI
	} else if r.Cache != nil {
		// No live OCI configured, but cache has data — expose cache as "oci"
		// since it provides the same contract data from previously pulled bundles.
		sources["oci"] = r.Cache
	}
	return sources
}

// AllSources returns all DataSource instances including cache (for version
// history lookups where cache may have richer metadata than live OCI).
func (r *DetectResult) AllSources() map[string]DataSource {
	sources := r.ActiveSources()
	// If cache exists and OCI also exists, add cache separately for version lookups.
	if r.Cache != nil && r.OCI != nil {
		sources["cache"] = r.Cache
	}
	return sources
}

func (r *DetectResult) detectLocal(dir string) {
	if dir == "" {
		dir = "."
	}

	diag := &r.Diagnostics.Local
	diag.Dir = dir
	info := SourceInfo{Type: "local"}

	// Check root for pacto.yaml.
	if _, err := os.Stat(filepath.Join(dir, contractFile)); err == nil {
		info.Enabled = true
		info.Reason = "pacto.yaml found in " + dir
		diag.PactoYamlFound = true
		diag.FoundIn = dir
		r.Local = NewLocalSource(dir)
		r.Sources = append(r.Sources, info)
		return
	}

	// Check subdirectories.
	entries, err := os.ReadDir(dir)
	if err != nil {
		info.Reason = "cannot read directory: " + err.Error()
		diag.Error = err.Error()
		r.Sources = append(r.Sources, info)
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, entry.Name(), contractFile)); err == nil {
			info.Enabled = true
			info.Reason = "pacto.yaml found in subdirectory " + entry.Name()
			diag.PactoYamlFound = true
			diag.FoundIn = filepath.Join(dir, entry.Name())
			r.Local = NewLocalSource(dir)
			r.Sources = append(r.Sources, info)
			return
		}
	}

	info.Reason = "no pacto.yaml found in " + dir
	r.Sources = append(r.Sources, info)
}

func (r *DetectResult) detectK8s(ctx context.Context, namespace string) {
	info := SourceInfo{Type: "k8s"}
	diag := &r.Diagnostics.K8s
	diag.Namespace = namespace
	diag.AllNamespaces = namespace == ""

	detectKubeconfig(diag)

	client, err := newK8sClientFunc()
	if err != nil {
		info.Reason = "Kubernetes client not available: " + err.Error()
		diag.Error = err.Error()
		r.Sources = append(r.Sources, info)
		return
	}
	diag.ClientConfigured = true

	if err := client.Probe(ctx); err != nil {
		info.Reason = "cluster not reachable"
		diag.Error = err.Error()
		r.Sources = append(r.Sources, info)
		return
	}
	diag.ClusterReachable = true

	resourceName := discoverCRD(ctx, client, diag)
	countResources(ctx, client, diag, resourceName, namespace)

	info.Enabled = true
	if diag.CRDExists {
		info.Reason = fmt.Sprintf("cluster reachable, CRD found (%s), %d resources", resourceName, diag.ResourceCount)
	} else {
		info.Reason = "cluster reachable (CRD not detected, may still work)"
	}

	r.K8s = NewK8sSource(client, namespace, resourceName)
	r.Sources = append(r.Sources, info)
}

// detectKubeconfig populates the kubeconfig path in diagnostics.
func detectKubeconfig(diag *K8sDiagnostics) {
	if kc := os.Getenv("KUBECONFIG"); kc != "" {
		diag.KubeconfigPath = kc
	} else if home, err := userHomeDir(); err == nil {
		defaultPath := filepath.Join(home, ".kube", "config")
		if _, err := os.Stat(defaultPath); err == nil {
			diag.KubeconfigPath = defaultPath
		}
	}
}

// discoverCRD dynamically discovers the Pacto CRD resource name and version.
func discoverCRD(ctx context.Context, client K8sClient, diag *K8sDiagnostics) string {
	discovery, err := client.DiscoverCRD(ctx)
	resourceName := "pactos" // fallback
	if err != nil {
		diag.Error = err.Error()
		return resourceName
	}

	if discovery.Found {
		diag.CRDExists = true
		diag.DetectedGroup = discovery.Group
		diag.DetectedVersions = discovery.Versions
		diag.ChosenVersion = discovery.Version
		if discovery.ResourceName != "" {
			resourceName = discovery.ResourceName
		}
		diag.ResourceName = resourceName
	}

	return resourceName
}

// countResources counts how many Pacto resources exist in the cluster.
func countResources(ctx context.Context, client K8sClient, diag *K8sDiagnostics, resourceName, namespace string) {
	count, err := client.CountResources(ctx, resourceName, namespace)
	if err == nil {
		diag.ResourceCount = count
	}
}

func (r *DetectResult) detectOCI(store oci.BundleStore, repos []string) {
	info := SourceInfo{Type: "oci"}
	diag := &r.Diagnostics.OCI

	if store == nil {
		info.Reason = "OCI registry client not configured"
		r.Sources = append(r.Sources, info)
		return
	}
	diag.StoreConfigured = true

	if len(repos) == 0 {
		info.Reason = "no OCI repositories specified (use --repo)"
		r.Sources = append(r.Sources, info)
		return
	}

	// Strip oci:// prefix from repos — other commands handle this via
	// graph.ParseDependencyRef, but the dashboard receives raw --repo values.
	cleaned := make([]string, len(repos))
	for i, repo := range repos {
		cleaned[i] = strings.TrimPrefix(repo, "oci://")
	}

	diag.Repos = cleaned
	info.Enabled = true
	info.Reason = fmt.Sprintf("OCI client configured with %d repositories", len(cleaned))
	r.OCI = NewOCISource(store, cleaned)
	r.Sources = append(r.Sources, info)
}

func (r *DetectResult) detectCache(cacheDir string) {
	diag := &r.Diagnostics.Cache

	if cacheDir == "" {
		// Determine default OCI cache directory.
		home, err := userHomeDir()
		if err != nil {
			diag.Error = err.Error()
			return
		}
		xdg := os.Getenv("XDG_CACHE_HOME")
		if xdg != "" {
			cacheDir = filepath.Join(xdg, "pacto", "oci")
		} else {
			cacheDir = filepath.Join(home, ".cache", "pacto", "oci")
		}
	}
	diag.CacheDir = cacheDir

	// Check if cache directory exists.
	if fi, err := os.Stat(cacheDir); err != nil || !fi.IsDir() {
		return
	}
	diag.Exists = true
	diag.OCIDirExists = true

	// Scan for cached bundles.
	src := NewCacheSource(cacheDir)
	diag.ServiceCount = src.ServiceCount()
	diag.VersionCount = src.VersionCount()

	if src.ServiceCount() == 0 {
		return
	}

	r.Cache = src
}

// EnrichFromK8s discovers OCI repository references from K8s service statuses
// and creates an OCI source when no explicit OCI repos were configured.
// This enables the dashboard to load full contract bundles from OCI even when
// started in K8s-only mode (e.g. operator-served dashboard).
//
// It also ensures a CacheSource exists so that OCI-pulled bundles can be
// rescanned at runtime (post-resolve, post-fetch-all).
func (r *DetectResult) EnrichFromK8s(ctx context.Context, store oci.BundleStore, cacheDir string) {
	if r.K8s == nil || r.OCI != nil || store == nil {
		return
	}

	repos := r.discoverOCIReposFromK8s(ctx)
	if len(repos) == 0 {
		return
	}

	r.detectOCI(store, repos)

	// Ensure a CacheSource exists for post-resolve rescan, even if the cache
	// directory was empty at detection time.
	if r.OCI != nil && r.Cache == nil {
		r.ensureCacheSource(cacheDir)
	}
}

// discoverOCIReposFromK8s queries K8s services and extracts unique OCI
// repository references from their imageRef fields.
func (r *DetectResult) discoverOCIReposFromK8s(ctx context.Context) []string {
	services, err := r.K8s.ListServices(ctx)
	if err != nil {
		return nil
	}

	seen := make(map[string]bool)
	var repos []string

	for _, svc := range services {
		d, err := r.K8s.GetService(ctx, svc.Name)
		if err != nil || d == nil || d.ImageRef == "" {
			continue
		}
		repo := stripTag(d.ImageRef)
		if repo != "" && !seen[repo] {
			seen[repo] = true
			repos = append(repos, repo)
		}
	}
	return repos
}

// ensureCacheSource creates a CacheSource if one doesn't exist, creating the
// cache directory if needed. This is required so that bundles pulled by the
// OCI source can be rescanned at runtime.
func (r *DetectResult) ensureCacheSource(cacheDir string) {
	if cacheDir == "" {
		home, err := userHomeDir()
		if err != nil {
			return
		}
		xdg := os.Getenv("XDG_CACHE_HOME")
		if xdg != "" {
			cacheDir = filepath.Join(xdg, "pacto", "oci")
		} else {
			cacheDir = filepath.Join(home, ".cache", "pacto", "oci")
		}
	}
	_ = os.MkdirAll(cacheDir, 0o755)
	r.Cache = NewCacheSource(cacheDir)

	if r.Diagnostics != nil {
		r.Diagnostics.Cache.CacheDir = cacheDir
		r.Diagnostics.Cache.Exists = true
		r.Diagnostics.Cache.OCIDirExists = true
	}
}

// splitNonEmpty splits a string by newlines and returns non-empty trimmed lines.
func splitNonEmpty(s string) []string {
	var result []string
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
