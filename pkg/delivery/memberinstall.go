package delivery

import (
	"archive/tar"
	"bytes"
	"cmp"
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
// candidates. ArgoNamespaces are where Argo CD's controllers run, the
// ApplicationSet's namespace (else the candidates', else an assumed argocd,
// with a warning). Matched are the hub's candidates as matched and HubUID the
// hub's UID: a candidate of the engine that is matched, states the same UID
// or states the hub's UID may already run Pacto and gets a warning. Warnings
// lead the result's: what the inputs could not rule out.
type MemberInstallOptions struct {
	Engine, Registry, HubURL, Chart, ChartVersion, Digest string
	InsecureRegistry                                      bool
	Flux, Argo                                            []detect.Candidate
	ArgoNamespaces                                        []string
	Matched                                               []ClusterCandidate
	HubUID                                                string
	Warnings                                              []string
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
	registryPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?(:[0-9]{1,5})?(/[a-z0-9]([a-z0-9._-]*[a-z0-9])?)*$`)
	hubURLPattern   = regexp.MustCompile(`^(https|wss)://[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?(:[0-9]{1,5})?(/[A-Za-z0-9._~/-]*)?$`)
	chartVerPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+-]{0,63}$`)
	digestPattern   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	// dataKey is a Secret or ConfigMap data key.
	dataKey          = regexp.MustCompile(`^[-._a-zA-Z0-9]{1,253}$`)
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
	var warns []string
	var err error
	if opts.Engine == InstallArgoCD {
		docs, warns, err = argoInstall(opts)
	} else {
		docs, warns, err = fluxInstall(opts)
	}
	if err != nil {
		return MemberInstallResult{}, err
	}
	docs[0].nodes[0].HeadComment = fmt.Sprintf(pushCredentialStep, opts.Registry)
	res := MemberInstallResult{Warnings: append([]string{}, slices.Concat(opts.Warnings, warns, candidateWarnings(opts))...)}
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
func argoInstall(o MemberInstallOptions) ([]installDoc, []string, error) {
	if o.Digest != "" {
		return nil, nil, errors.New("a digest pins the Flux bootstrap; for Argo CD point an Application at the pushed reference")
	}
	ns, warns := argoNamespace(o)
	if !dnsLabel(ns) {
		return nil, nil, fmt.Errorf("the Argo CD namespace %q is not a DNS label", ns)
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
	return []installDoc{{"pacto-members-applicationset.yaml", []*yaml.Node{appSet}}, {"pacto-charts-repository.yaml", []*yaml.Node{secret}}}, warns, nil
}

// argoNamespace is the namespace the ApplicationSet goes in: Argo CD's
// control plane, else its candidates', else an assumed argocd. Several, or
// the assumption, give a warning.
func argoNamespace(o MemberInstallOptions) (string, []string) {
	nss := slices.Clone(o.ArgoNamespaces)
	if len(nss) == 0 {
		for _, c := range o.Argo {
			nss = append(nss, c.Namespace)
		}
	}
	slices.Sort(nss)
	switch nss = slices.Compact(nss); len(nss) {
	case 0:
		return "argocd", []string{"no Argo CD namespace was detected, so the ApplicationSet and its repository Secret assume namespace argocd: change it where Argo CD runs elsewhere"}
	case 1:
		return nss[0], nil
	}
	return nss[0], []string{fmt.Sprintf("Argo CD runs in %d namespaces (%s): the ApplicationSet in %s reaches only the clusters of the Argo CD there; apply a copy in each other namespace",
		len(nss), strings.Join(nss, ", "), nss[0])}
}

// splitChart is an oci:// chart reference as an Argo CD repository URL (no
// scheme) and chart name.
func splitChart(ref string) (string, string) {
	i := strings.LastIndex(ref, "/")
	return strings.TrimPrefix(ref[:i], "oci://"), ref[i+1:]
}

// candidateWarnings name every candidate of the engine that may already run
// Pacto, which the install would give a second release, and how to keep it
// out: matched to an approved member, stating the UID a pending or removed
// cluster reported, or stating the hub's own UID.
func candidateWarnings(o MemberInstallOptions) []string {
	src, object := detect.SourceArgoCD, "Argo CD cluster Secret"
	if o.Engine == InstallFlux {
		src, object = detect.SourceFlux, "Flux kubeconfig"
	}
	var out []string
	for _, c := range o.Matched {
		if c.Source != src {
			continue
		}
		var why string
		switch {
		case c.Match == CandidateMatched && c.Member != nil:
			why = "is the approved member " + cmp.Or(c.Member.Name, shortClusterUID(c.Member.UID)) + ", which runs Pacto; keep it out"
		case c.Match == CandidateMatched:
			why = "is an approved member, which runs Pacto; keep it out"
		case c.Match == CandidateSameUID:
			why = "states the UID a pending or removed cluster reported about itself; if that cluster still runs Pacto, keep it out"
		case c.UID != "" && c.UID == o.HubUID:
			why = "states the hub's own UID; keep it out"
		default:
			continue
		}
		how := fmt.Sprintf("of the ApplicationSet: kubectl -n %s label secret %s %s=false", c.Namespace, c.Name, installOptOut)
		if o.Engine == InstallFlux {
			how = fmt.Sprintf("by leaving out %s/pacto-%s.yaml", c.Namespace, c.Name)
		}
		out = append(out, fmt.Sprintf("%s %s/%s %s %s", object, c.Namespace, c.Name, why, how))
	}
	return out
}

// fluxInstall is, per kubeconfig namespace, one chart OCIRepository and one
// HelmRelease per ref, plus the digest-pinned bootstrap when a digest is known.
func fluxInstall(o MemberInstallOptions) ([]installDoc, []string, error) {
	refs, warns, err := fluxRefs(o.Flux)
	if err != nil {
		return nil, nil, err
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
		kubeConfig := ymap("name", ref.Name)
		if ref.Key != "" {
			kubeConfig.Content = append(kubeConfig.Content, ystr("key"), ystr(ref.Key))
		}
		docs = append(docs, installDoc{ref.Namespace + "/pacto-" + ref.Name + ".yaml", []*yaml.Node{ymap("apiVersion", "helm.toolkit.fluxcd.io/v2", "kind", "HelmRelease",
			"metadata", ymap("name", "pacto-"+ref.Name, "namespace", ref.Namespace),
			"spec", ymap("interval", "1h", "releaseName", memberRelease, "targetNamespace", memberNamespace, "storageNamespace", memberNamespace,
				"chartRef", ymap("kind", "OCIRepository", "name", "pacto-chart"),
				"kubeConfig", ymap(field, kubeConfig),
				"install", ymap("createNamespace", true),
				"values", memberValues(o)))}})
	}
	if o.Digest != "" {
		docs = append(docs, fluxBootstrap(o))
	}
	return docs, warns, nil
}

// fluxRefs are the distinct kubeconfig refs of cands, sorted, each validated,
// leaving out the Secrets labelled pacto.trianalab.io/install=false: none is
// an error, as is a name that is not a DNS name or a key that is not a data
// key. A name given twice (as a Secret and a ConfigMap, or with two keys)
// keeps its first ref and warns.
func fluxRefs(cands []detect.Candidate) ([]detect.KubeConfigRef, []string, error) {
	var refs []detect.KubeConfigRef
	optedOut := 0
	for _, c := range cands {
		if c.KubeConfig == nil {
			continue
		}
		r := *c.KubeConfig
		if err := checkKubeConfigRef(r); err != nil {
			return nil, nil, err
		}
		if c.Labels[installOptOut] == "false" {
			optedOut++
			continue
		}
		refs = append(refs, r)
	}
	switch {
	case len(refs) == 0 && optedOut > 0:
		return nil, nil, fmt.Errorf("every Flux kubeconfig the hub names is labelled %s=false", installOptOut)
	case len(refs) == 0:
		return nil, nil, errors.New("no Flux kubeconfig ref was detected on the hub: Flux installs a member through a Kustomization or HelmRelease that sets spec.kubeConfig")
	}
	slices.SortFunc(refs, func(a, b detect.KubeConfigRef) int {
		return strings.Compare(a.Namespace+"/"+a.Name+"/"+a.Kind+"/"+a.Key, b.Namespace+"/"+b.Name+"/"+b.Kind+"/"+b.Key)
	})
	var out []detect.KubeConfigRef
	var warns []string
	for _, r := range slices.Compact(refs) {
		// Sorted, so a name given twice sits next to its first ref.
		if n := len(out); n > 0 && out[n-1].Namespace == r.Namespace && out[n-1].Name == r.Name {
			warns = append(warns, fmt.Sprintf("the Flux kubeconfig %s/%s is named twice (%s, %s); its HelmRelease uses the first: check it reaches the cluster each Flux object does",
				r.Namespace, r.Name, kubeConfigOf(out[n-1]), kubeConfigOf(r)))
			continue
		}
		out = append(out, r)
	}
	return out, warns, nil
}

// checkKubeConfigRef refuses a ref that is not a Secret or ConfigMap with a
// DNS name, or names a key that is not a data key.
func checkKubeConfigRef(r detect.KubeConfigRef) error {
	if (r.Kind != "Secret" && r.Kind != "ConfigMap") || !dnsLabel(r.Namespace) || !subdomainPattern.MatchString(r.Name) || len(r.Name) > 63-len("pacto-") {
		return fmt.Errorf("the Flux kubeconfig %s %q in namespace %q: a Secret or ConfigMap with a DNS name of at most 57 characters", r.Kind, r.Name, r.Namespace)
	}
	if r.Key != "" && !dataKey.MatchString(r.Key) {
		return fmt.Errorf("the Flux kubeconfig %s/%s names key %q, which is not a data key", r.Namespace, r.Name, r.Key)
	}
	return nil
}

// kubeConfigOf words r's kind and key.
func kubeConfigOf(r detect.KubeConfigRef) string {
	return r.Kind + " key " + cmp.Or(r.Key, "(default)")
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
