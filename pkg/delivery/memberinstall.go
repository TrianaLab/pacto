package delivery

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/pkg/detect"
)

// The engines a member install is generated for.
const (
	InstallArgoCD = "argocd"
	InstallFlux   = "flux"
)

const (
	// DefaultMemberChart is the chart a member install pulls.
	DefaultMemberChart = "oci://ghcr.io/trianalab/pacto/charts/pacto-operator"
	// MemberInstallArtifactType is the artifact type of a pushed member install.
	MemberInstallArtifactType = "application/vnd.trianalab.pacto.member-install.v1"
	// MemberInstallLayerType is its one layer: the files as a tar.gz, which
	// Argo CD and Flux OCI sources both extract.
	MemberInstallLayerType = "application/vnd.oci.image.layer.v1.tar+gzip"
	// MemberInstallTagPrefix starts every pushed member install tag; it never
	// starts with TargetSnapshotTagPrefix, so no snapshot reader lists it.
	MemberInstallTagPrefix = "members-"

	// memberNamespace is where the chart is installed in each member.
	memberNamespace = "pacto"
	// memberRelease is the Helm release name in each member.
	memberRelease = "pacto"
	// installOptOut is the cluster Secret label that keeps a cluster out.
	installOptOut = "pacto.trianalab.io/install"
	// helmChartLayer is the layer of a Helm chart pushed to an OCI registry.
	helmChartLayer = "application/vnd.cncf.helm.chart.content.v1.tar+gzip"
	// inClusterServer is the server Argo CD names its own cluster by.
	inClusterServer = "https://kubernetes.default.svc"
	// pushCredentialStep is the one comment a generated install carries.
	pushCredentialStep = "Each member publishes its snapshot once it holds a push credential on %s/pacto-targets: create that Secret in namespace pacto and set registryPullSecret in your own values overlay (https://pacto.run/latest/install-members/)."
)

// ErrNoChartVersion is MemberInstall's answer when no chart version is named
// or advertised.
var ErrNoChartVersion = errors.New("no chart version: pass --chart-version, or read a hub whose operator advertises its chart version")

// MemberInstallOptions are what a member install is generated from. Registry
// is the chart's registry value; HubURL, when set, its hub.url; Chart the
// chart reference (DefaultMemberChart when empty) at ChartVersion. Digest, a
// pushed install's manifest digest, adds the Flux bootstrap that pins it.
// Flux are the Flux candidates (their kubeconfig refs); Argo the Argo CD
// candidates, whose namespace is the ApplicationSet's (argocd when none).
// Matched are the hub's candidates as matched: every matched or same-uid Argo
// CD one already runs Pacto and gets a warning.
type MemberInstallOptions struct {
	Engine, Registry, HubURL, Chart, ChartVersion, Digest string
	InsecureRegistry                                      bool
	Flux, Argo                                            []detect.Candidate
	Matched                                               []ClusterCandidate
}

// InstallFile is one generated file; Content is YAML text.
type InstallFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// MemberInstallResult is the generated files and what the person should know
// before applying them.
type MemberInstallResult struct {
	Files    []InstallFile `json:"files"`
	Warnings []string      `json:"warnings"`
}

var (
	registryPattern  = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?(:[0-9]{1,5})?(/[a-z0-9]([a-z0-9._-]*[a-z0-9])?)*$`)
	hubURLPattern    = regexp.MustCompile(`^(https|wss)://[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?(:[0-9]{1,5})?(/[A-Za-z0-9._~/-]*)?$`)
	chartVerPattern  = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+-]{0,63}$`)
	digestPattern    = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	subdomainPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$`)
)

// MemberInstall generates the files that install the Pacto chart on every
// member through the engine opts names: an Argo CD ApplicationSet, or Flux
// HelmReleases per kubeconfig ref. The values are registry, hub.url when set
// and insecureRegistries only with InsecureRegistry. Removal orphans: deleting
// the generated objects never uninstalls Pacto from a member. Nothing follows
// a tag of pacto-targets.
func MemberInstall(opts MemberInstallOptions) (MemberInstallResult, error) {
	if err := checkMemberInstall(&opts); err != nil {
		return MemberInstallResult{}, err
	}
	var docs []installDoc
	var err error
	if opts.Engine == InstallArgoCD {
		docs, err = argoInstall(opts)
	} else {
		docs, err = fluxInstall(opts)
	}
	if err != nil {
		return MemberInstallResult{}, err
	}
	docs[0].nodes[0].HeadComment = fmt.Sprintf(pushCredentialStep, opts.Registry)
	res := MemberInstallResult{Warnings: argoWarnings(opts.Matched)}
	for _, d := range docs {
		var b bytes.Buffer
		enc := yaml.NewEncoder(&b)
		enc.SetIndent(2)
		for _, n := range d.nodes {
			_ = enc.Encode(n) // ponytail: nodes built here from scalars always encode
		}
		_ = enc.Close()
		res.Files = append(res.Files, InstallFile{Path: d.path, Content: b.String()})
	}
	return res, nil
}

// checkMemberInstall refuses what would not install or could reach a file
// as structure, and fills the chart default.
func checkMemberInstall(o *MemberInstallOptions) error {
	if o.Chart == "" {
		o.Chart = DefaultMemberChart
	}
	switch {
	case o.Engine != InstallArgoCD && o.Engine != InstallFlux:
		return fmt.Errorf("engine %q: generate for argocd or flux", o.Engine)
	case !registryPattern.MatchString(o.Registry):
		return fmt.Errorf("registry %q: name it as host[/path], no scheme", o.Registry)
	case o.HubURL != "" && !hubURLPattern.MatchString(o.HubURL):
		return fmt.Errorf("hub URL %q: name it https:// or wss:// with a host", o.HubURL)
	case o.ChartVersion == "":
		return ErrNoChartVersion
	case !chartVerPattern.MatchString(o.ChartVersion):
		return fmt.Errorf("chart version %q is not a version", o.ChartVersion)
	case !strings.HasPrefix(o.Chart, "oci://") || !registryPattern.MatchString(strings.TrimPrefix(o.Chart, "oci://")) || !strings.Contains(o.Chart[len("oci://"):], "/"):
		return fmt.Errorf("chart %q: name it oci://<registry>/<path>/<chart>", o.Chart)
	case o.Digest != "" && !digestPattern.MatchString(o.Digest):
		return fmt.Errorf("digest %q: name it sha256:<64 hex>", o.Digest)
	}
	return nil
}

// installDoc is one file to render.
type installDoc struct {
	path  string
	nodes []*yaml.Node
}

// memberValues are the chart values every member gets.
func memberValues(o MemberInstallOptions) *yaml.Node {
	v := ymap("registry", o.Registry)
	if o.HubURL != "" {
		v.Content = append(v.Content, ystr("hub"), ymap("url", o.HubURL))
	}
	if o.InsecureRegistry {
		host, _, _ := strings.Cut(o.Registry, "/")
		v.Content = append(v.Content, ystr("insecureRegistries"), yseq(ystr(host)))
	}
	return v
}

// argoInstall is the ApplicationSet over every Argo CD cluster but the hub's
// own and the opted-out ones, and the Helm OCI repository Secret.
func argoInstall(o MemberInstallOptions) ([]installDoc, error) {
	ns := "argocd"
	if len(o.Argo) > 0 {
		ns = o.Argo[0].Namespace
	}
	if !dnsLabel(ns) {
		return nil, fmt.Errorf("the Argo CD namespace %q is not a DNS label", ns)
	}
	chartRepo, chart := splitChart(o.Chart)
	data := ymap("name", "pacto-charts", "url", chartRepo, "type", "helm", "enableOCI", "true")
	if o.InsecureRegistry {
		data.Content = append(data.Content, ystr("insecureOCIForceHttp"), ystr("true"))
	}
	generator := ymap(
		"clusters", ymap("selector", ymap(
			"matchLabels", ymap("argocd.argoproj.io/secret-type", "cluster"),
			"matchExpressions", yseq(ymap("key", installOptOut, "operator", "NotIn", "values", yseq(ystr("false")))))),
		"selector", ymap("matchExpressions", yseq(ymap("key", "server", "operator", "NotIn", "values", yseq(ystr(inClusterServer))))))
	template := ymap(
		"metadata", ymap("name", "pacto-{{.nameNormalized}}"),
		"spec", ymap("project", "default",
			"source", ymap("repoURL", chartRepo, "chart", chart, "targetRevision", o.ChartVersion,
				"helm", ymap("releaseName", memberRelease, "valuesObject", memberValues(o))),
			"destination", ymap("server", "{{.server}}", "namespace", memberNamespace),
			"syncPolicy", ymap("automated", ymap(), "syncOptions", yseq(ystr("CreateNamespace=true")))))
	appSet := ymap("apiVersion", "argoproj.io/v1alpha1", "kind", "ApplicationSet",
		"metadata", ymap("name", "pacto-members", "namespace", ns),
		"spec", ymap("goTemplate", true, "goTemplateOptions", yseq(ystr("missingkey=error")), "generators", yseq(generator),
			"syncPolicy", ymap("preserveResourcesOnDeletion", true), "template", template))
	secret := ymap("apiVersion", "v1", "kind", "Secret",
		"metadata", ymap("name", "pacto-charts", "namespace", ns, "labels", ymap("argocd.argoproj.io/secret-type", "repository")),
		"stringData", data)
	return []installDoc{{"pacto-members-applicationset.yaml", []*yaml.Node{appSet}}, {"pacto-charts-repository.yaml", []*yaml.Node{secret}}}, nil
}

// splitChart is an oci:// chart reference as an Argo CD repository URL (no
// scheme) and chart name.
func splitChart(ref string) (string, string) {
	i := strings.LastIndex(ref, "/")
	return strings.TrimPrefix(ref[:i], "oci://"), ref[i+1:]
}

// argoWarnings name every Argo CD candidate that already runs Pacto, which
// the ApplicationSet would give a second release, and how to keep it out.
func argoWarnings(cands []ClusterCandidate) []string {
	out := []string{}
	for _, c := range cands {
		if c.Source != detect.SourceArgoCD || c.Match == CandidateUnmatched {
			continue
		}
		out = append(out, fmt.Sprintf("Argo CD cluster Secret %s/%s states the UID of a member that runs Pacto; keep it out of the ApplicationSet: kubectl -n %s label secret %s %s=false",
			c.Namespace, c.Name, c.Namespace, c.Name, installOptOut))
	}
	return out
}

// fluxInstall is, per kubeconfig namespace, one chart OCIRepository and one
// HelmRelease per ref, plus the digest-pinned bootstrap when a digest is known.
func fluxInstall(o MemberInstallOptions) ([]installDoc, error) {
	refs, err := fluxRefs(o.Flux)
	if err != nil {
		return nil, err
	}
	var docs []installDoc
	for i, ref := range refs {
		if i == 0 || refs[i-1].Namespace != ref.Namespace {
			repo := ymap("interval", "1h", "url", o.Chart, "ref", ymap("tag", o.ChartVersion),
				"layerSelector", ymap("mediaType", helmChartLayer, "operation", "copy"))
			if o.InsecureRegistry {
				repo.Content = append(repo.Content, ystr("insecure"), ybool(true))
			}
			docs = append(docs, installDoc{ref.Namespace + "/pacto-chart.yaml", []*yaml.Node{ymap("apiVersion", "source.toolkit.fluxcd.io/v1", "kind", "OCIRepository",
				"metadata", ymap("name", "pacto-chart", "namespace", ref.Namespace), "spec", repo)}})
		}
		field := "secretRef"
		if ref.Kind == "ConfigMap" {
			field = "configMapRef"
		}
		docs = append(docs, installDoc{ref.Namespace + "/pacto-" + ref.Name + ".yaml", []*yaml.Node{ymap("apiVersion", "helm.toolkit.fluxcd.io/v2", "kind", "HelmRelease",
			"metadata", ymap("name", "pacto-"+ref.Name, "namespace", ref.Namespace),
			"spec", ymap("interval", "1h", "releaseName", memberRelease, "targetNamespace", memberNamespace, "storageNamespace", memberNamespace,
				"chartRef", ymap("kind", "OCIRepository", "name", "pacto-chart"),
				"kubeConfig", ymap(field, ymap("name", ref.Name)),
				"install", ymap("createNamespace", true),
				"values", memberValues(o)))}})
	}
	if o.Digest != "" {
		docs = append(docs, fluxBootstrap(o))
	}
	return docs, nil
}

// fluxRefs are the distinct kubeconfig refs of cands, sorted, each validated:
// none is an error, as is a name that is not a DNS name.
func fluxRefs(cands []detect.Candidate) ([]detect.Object, error) {
	var refs []detect.Object
	for _, c := range cands {
		if c.KubeConfig == nil {
			continue
		}
		r := *c.KubeConfig
		if (r.Kind != "Secret" && r.Kind != "ConfigMap") || !dnsLabel(r.Namespace) || !subdomainPattern.MatchString(r.Name) || len(r.Name) > 63-len("pacto-") {
			return nil, fmt.Errorf("the Flux kubeconfig %s %q in namespace %q: a Secret or ConfigMap with a DNS name of at most 57 characters", r.Kind, r.Name, r.Namespace)
		}
		refs = append(refs, r)
	}
	if len(refs) == 0 {
		return nil, errors.New("no Flux kubeconfig ref was detected on the hub: Flux installs a member through a Kustomization or HelmRelease that sets spec.kubeConfig")
	}
	slices.SortFunc(refs, func(a, b detect.Object) int {
		return strings.Compare(a.Namespace+"/"+a.Name+"/"+a.Kind, b.Namespace+"/"+b.Name+"/"+b.Kind)
	})
	refs = slices.Compact(refs)
	for i := 1; i < len(refs); i++ {
		if refs[i].Namespace == refs[i-1].Namespace && refs[i].Name == refs[i-1].Name {
			return nil, fmt.Errorf("the Flux kubeconfig %s/%s names both a Secret and a ConfigMap; one HelmRelease per name is generated", refs[i].Namespace, refs[i].Name)
		}
	}
	return refs, nil
}

// fluxBootstrap is the OCIRepository that pins the pushed install by digest
// and the Kustomization that applies it, orphaning on deletion.
func fluxBootstrap(o MemberInstallOptions) installDoc {
	src := ymap("apiVersion", "source.toolkit.fluxcd.io/v1", "kind", "OCIRepository",
		"metadata", ymap("name", "pacto-members", "namespace", "flux-system"),
		"spec", ymap("interval", "1h", "url", "oci://"+o.Registry+"/"+TargetSnapshotRepository, "ref", ymap("digest", o.Digest),
			"layerSelector", ymap("mediaType", MemberInstallLayerType)))
	if o.InsecureRegistry {
		src.Content[len(src.Content)-1].Content = append(src.Content[len(src.Content)-1].Content, ystr("insecure"), ybool(true))
	}
	ks := ymap("apiVersion", "kustomize.toolkit.fluxcd.io/v1", "kind", "Kustomization",
		"metadata", ymap("name", "pacto-members", "namespace", "flux-system"),
		"spec", ymap("interval", "1h", "sourceRef", ymap("kind", "OCIRepository", "name", "pacto-members"), "path", "./",
			"prune", false, "deletionPolicy", "Orphan"))
	return installDoc{"bootstrap.yaml", []*yaml.Node{src, ks}}
}

// InstallArchive is files as the gzip tar a pushed member install carries,
// the same bytes for the same files.
func InstallArchive(files []InstallFile) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		// ponytail: writes to a bytes.Buffer never fail
		_ = tw.WriteHeader(&tar.Header{Name: f.Path, Mode: 0o644, Size: int64(len(f.Content)), Typeflag: tar.TypeReg, Format: tar.FormatPAX})
		_, _ = tw.Write([]byte(f.Content))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

// MemberInstallDownload is res as a download hands it out: each warning as a
// leading comment line, then the files as one YAML stream.
func MemberInstallDownload(res MemberInstallResult) []byte {
	var b bytes.Buffer
	for _, w := range res.Warnings {
		b.WriteString("# warning: " + strings.ReplaceAll(w, "\n", " ") + "\n")
	}
	b.Write(MemberInstallYAML(res.Files))
	return b.Bytes()
}

// MemberInstallYAML is files as one YAML stream, the way a person applies it.
func MemberInstallYAML(files []InstallFile) []byte {
	var b bytes.Buffer
	for i, f := range files {
		if i > 0 {
			b.WriteString("---\n")
		}
		b.WriteString(f.Content)
	}
	return b.Bytes()
}

// ymap is a mapping node of key, value pairs: a value is a node, a string or
// a bool. Every input lands as a scalar, never as structure.
func ymap(kv ...any) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode}
	for i := 0; i < len(kv); i += 2 {
		n.Content = append(n.Content, ystr(kv[i].(string)), ynode(kv[i+1]))
	}
	return n
}

func ynode(v any) *yaml.Node {
	switch v := v.(type) {
	case string:
		return ystr(v)
	case bool:
		return ybool(v)
	}
	return v.(*yaml.Node)
}

func ystr(s string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s} }

func ybool(b bool) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: fmt.Sprint(b)}
}

func yseq(items ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.SequenceNode, Content: items}
}
