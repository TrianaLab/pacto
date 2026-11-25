package delivery

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// tgz is a gzipped tar of files, in name order.
func tgz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	slices.Sort(names)
	_ = tw.WriteHeader(&tar.Header{Name: "dir/", Typeflag: tar.TypeDir, Mode: 0o755})
	for _, n := range names {
		_ = tw.WriteHeader(&tar.Header{Name: n, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(files[n]))})
		_, _ = tw.Write([]byte(files[n]))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func inspectFlux(t *testing.T, files map[string]string) ManifestInspection {
	t.Helper()
	return InspectArtifact(FluxContentMediaType, tgz(t, files), ManifestTarget{Namespace: "prod"}, BuiltinScope)
}

func inspectChart(t *testing.T, templates map[string]string) ManifestInspection {
	t.Helper()
	files := map[string]string{"app/Chart.yaml": "name: app\n"}
	for n, c := range templates {
		files["app/"+n] = c
	}
	return InspectArtifact(HelmChartMediaType, tgz(t, files), ManifestTarget{Namespace: "prod"}, BuiltinScope)
}

func obj(apiVersion, kind, name, ns string, extra ...string) string {
	s := "apiVersion: " + apiVersion + "\nkind: " + kind + "\nmetadata:\n  name: " + name + "\n"
	if ns != "" {
		s += "  namespace: " + ns + "\n"
	}
	return s + strings.Join(extra, "\n")
}

func wantVerdict(t *testing.T, name string, in ManifestInspection, result Result, msg string) Verdict {
	t.Helper()
	v := in.Verdict()
	if v.Result != result || !strings.Contains(v.Message, msg) {
		t.Errorf("%s: want %s with %q, got %s %q (%+v)", name, result, msg, v.Result, v.Message, in)
	}
	return v
}

func TestInspectPlainManifestsPass(t *testing.T) {
	in := inspectFlux(t, map[string]string{
		"deploy.yaml": obj("apps/v1", "Deployment", "payments", "prod") + "---\n" + obj("v1", "Service", "payments", ""),
		"pacto.yml":   "# only a comment\n---\n" + obj("pacto.trianalab.io/v1", "Pacto", "payments", "prod") + "---\n",
		"README.md":   "not read",
		"svc.json":    `{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": {"name": "payments"}}`,
	})
	v := wantVerdict(t, "plain", in, Pass, "every one of the 4 objects stays in prod")
	if v.Citation["artifact"] != "manifests" || v.Citation["objects"] != "Deployment payments, Service payments, Pacto payments, ServiceAccount payments" {
		t.Fatalf("citation %+v", v.Citation)
	}
}

func TestInspectClusterScopedFails(t *testing.T) {
	in := inspectFlux(t, map[string]string{"crb.yaml": obj("rbac.authorization.k8s.io/v1", "ClusterRoleBinding", "orders-admin", "")})
	v := wantVerdict(t, "crb", in, Fail, "ClusterRoleBinding orders-admin is cluster-scoped")
	if v.Citation["refused"] != "ClusterRoleBinding orders-admin is cluster-scoped" {
		t.Fatalf("citation %+v", v.Citation)
	}
	wantVerdict(t, "namespace", inspectFlux(t, map[string]string{"ns.yaml": obj("v1", "Namespace", "x", "")}), Fail, "Namespace x is cluster-scoped")
}

func TestInspectOtherNamespaceFails(t *testing.T) {
	wantVerdict(t, "other", inspectFlux(t, map[string]string{"a.yaml": obj("v1", "ConfigMap", "c", "kube-system")}),
		Fail, "ConfigMap c is in kube-system, outside prod")
}

func TestInspectRoleBindingToClusterRoleFails(t *testing.T) {
	rb := obj("rbac.authorization.k8s.io/v1", "RoleBinding", "rb", "prod", "roleRef:\n  kind: ClusterRole\n  name: admin\n")
	wantVerdict(t, "rb", inspectFlux(t, map[string]string{"rb.yaml": rb}), Fail, "RoleBinding rb binds ClusterRole admin")
	// Every branch of a template is judged.
	branches := "apiVersion: rbac.authorization.k8s.io/v1\nkind: RoleBinding\nmetadata:\n  name: rb\nroleRef:\n{{- if .Values.wide }}\n  kind: ClusterRole\n" +
		"{{- else }}\n  kind: Role\n{{- end }}\n  name: admin\n"
	wantVerdict(t, "branches", inspectChart(t, map[string]string{"templates/rb.yaml": branches}), Fail, "RoleBinding rb binds ClusterRole admin")
}

func TestInspectRoleBindingOutsideSubjectsUnknown(t *testing.T) {
	rb := func(subject string) map[string]string {
		return map[string]string{"rb.yaml": obj("rbac.authorization.k8s.io/v1", "RoleBinding", "rb", "",
			"roleRef:\n  kind: Role\n  name: r\nsubjects:\n"+subject)}
	}
	for name, c := range map[string]struct{ subject, msg string }{
		"group": {"- kind: Group\n  name: system:authenticated\n", "grants Group system:authenticated, outside prod"},
		"user":  {"- kind: User\n  name: dana\n", "grants User dana, outside prod"},
		"sa":    {"- kind: ServiceAccount\n  name: x\n  namespace: other\n", "grants ServiceAccount x of other, outside prod"},
	} {
		wantVerdict(t, name, inspectFlux(t, rb(c.subject)), Unknown, c.msg)
	}
	wantVerdict(t, "own sa", inspectFlux(t, rb("- kind: ServiceAccount\n  name: x\n  namespace: prod\n- kind: ServiceAccount\n  name: y\n")), Pass, "")
	noRef := map[string]string{"rb.yaml": obj("rbac.authorization.k8s.io/v1", "RoleBinding", "rb", "")}
	wantVerdict(t, "no roleRef", inspectFlux(t, noRef), Unknown, "Pacto cannot read the roleRef of RoleBinding rb")
}

func TestInspectNestedApplierFails(t *testing.T) {
	for _, o := range [][2]string{
		{"kustomize.toolkit.fluxcd.io/v1", "Kustomization"}, {"helm.toolkit.fluxcd.io/v2", "HelmRelease"},
		{"source.toolkit.fluxcd.io/v1", "OCIRepository"}, {"delivery.pacto.trianalab.io/v1", "Stage"},
		{"delivery.pacto.trianalab.io/v1", "Operation"}, {"argoproj.io/v1alpha1", "Application"},
	} {
		in := inspectFlux(t, map[string]string{"x.yaml": obj(o[0], o[1], "n", "prod")})
		wantVerdict(t, o[1], in, Fail, o[1]+" n is a delivery or applier object")
	}
}

func TestInspectKustomizationNamespaceOverride(t *testing.T) {
	files := map[string]string{"kustomization.yaml": "resources:\n- a.yaml\nnamespace: other\n", "a.yaml": obj("v1", "ConfigMap", "c", "")}
	wantVerdict(t, "other", inspectFlux(t, files), Fail, "the kustomization moves every object to other")
	files = map[string]string{"kustomization.yml": "resources:\n- ./a.yaml\nnamespace: prod\n", "a.yaml": obj("v1", "ConfigMap", "c", "elsewhere")}
	v := wantVerdict(t, "same", inspectFlux(t, files), Pass, "")
	if v.Citation["artifact"] != "kustomize" {
		t.Fatalf("citation %+v", v.Citation)
	}
}

func TestInspectKustomizationTransformsUnknown(t *testing.T) {
	cases := map[string]string{
		"remote":                "resources:\n- https://github.com/x/y//base\n",
		"nested":                "resources:\n- base\n",
		"patches":               "patches: []\n",
		"patchesJson6902":       "patchesJson6902: []\n",
		"patchesStrategicMerge": "patchesStrategicMerge: []\n",
		"replacements":          "replacements: []\n",
		"components":            "components: []\n",
		"transformers":          "transformers: []\n",
		"generators":            "generators: []\n",
		"crds":                  "crds: []\n",
		"helmCharts":            "helmCharts: []\n",
	}
	for name, k := range cases {
		files := map[string]string{"Kustomization": k, "base/kustomization.yaml": "resources: []\n"}
		in := inspectFlux(t, files)
		wantVerdict(t, name, in, Unknown, "Pacto reads plain manifests, and this kustomization transforms them")
	}
	wantVerdict(t, "unreadable", inspectFlux(t, map[string]string{"kustomization.yaml": ":\n- [\n"}), Unknown, "kustomization.yaml is not readable YAML")
}

func TestInspectKustomizationListsOnlyItsResources(t *testing.T) {
	files := map[string]string{"kustomization.yaml": "resources:\n- a.yaml\n", "a.yaml": obj("v1", "ConfigMap", "c", ""),
		"unlisted.yaml": obj("rbac.authorization.k8s.io/v1", "ClusterRole", "x", "")}
	v := wantVerdict(t, "listed", inspectFlux(t, files), Pass, "every one of the 1 objects")
	if v.Citation["objects"] != "ConfigMap c" {
		t.Fatalf("citation %+v", v.Citation)
	}
}

func TestInspectSubstitutionUnknown(t *testing.T) {
	wantVerdict(t, "kind", inspectFlux(t, map[string]string{"a.yaml": obj("v1", "${KIND}", "c", "")}), Unknown, "holds a substitution")
	wantVerdict(t, "ns", inspectFlux(t, map[string]string{"a.yaml": obj("v1", "ConfigMap", "c", "${NS}")}), Unknown, "ConfigMap c holds a substitution")
}

func TestInspectTargetNamespaceAndApplierTransforms(t *testing.T) {
	stage := StageObject{Metadata: ObjectMeta{Namespace: "prod"}, Spec: StageSpec{ApplyRef: ApplyRef{Name: "payments"}}}
	k := func(name, targetNS string, field string) KustomizationView {
		kv := kustomizationOf("prod", name, ApplierFlux, "", "payments")
		kv.Spec.TargetNamespace = targetNS
		switch field {
		case "patches":
			kv.Spec.Patches = json.RawMessage(`[]`)
		case "postBuild":
			kv.Spec.PostBuild = json.RawMessage(`{}`)
		case "components":
			kv.Spec.Components = json.RawMessage(`[]`)
		}
		return kv
	}
	ks := []KustomizationView{k("elsewhere", "other", ""), k("p", "", "patches"), k("b", "prod", "postBuild"), k("c", "", "components"),
		kustomizationOf("prod", "foreign", ApplierFlux, "", "another"), kustomizationOf("ops", "remote", ApplierFlux, "prod", "payments")}
	tg := ManifestTargetOf(stage, nil, ks, errors.New("forbidden"))
	// A Kustomization in another namespace (remote) applies as that
	// namespace's identity and is never judged for this Stage.
	if strings.Join(tg.Refused, "|") != "Kustomization elsewhere applies into other, outside prod" ||
		strings.Join(tg.Unread, "|") != "the Kustomizations: forbidden|Kustomization p transforms the artifact: spec.patches|"+
			"Kustomization b transforms the artifact: spec.postBuild|Kustomization c transforms the artifact: spec.components" {
		t.Fatalf("flux target %+v", tg)
	}
	in := InspectArtifact(FluxContentMediaType, tgz(t, map[string]string{"a.yaml": obj("v1", "ConfigMap", "c", "")}), tg, BuiltinScope)
	wantVerdict(t, "target refusal", in, Fail, "Kustomization elsewhere applies into other")
	stage.Spec.ApplyRef = ApplyRef{Kind: ApplierHelmRelease, Name: "payments"}
	tg = ManifestTargetOf(stage, map[string]any{"spec": map[string]any{"postRenderers": []any{}, "targetNamespace": "prod"}}, nil, nil)
	if len(tg.Refused) != 0 || strings.Join(tg.Unread, "|") != "HelmRelease payments transforms the artifact: spec.postRenderers" {
		t.Fatalf("helm target %+v", tg)
	}
	stage.Spec.ApplyRef = ApplyRef{Kind: ApplierArgoCD, Name: "payments"}
	tg = ManifestTargetOf(stage, map[string]any{"spec": map[string]any{"destination": map[string]any{"namespace": "x"},
		"source": map[string]any{"helm": map[string]any{}}}}, nil, nil)
	if strings.Join(tg.Refused, "|") != "Application payments applies into x, outside prod" ||
		strings.Join(tg.Unread, "|") != "Application payments transforms the artifact: spec.source.helm" {
		t.Fatalf("argo target %+v", tg)
	}
}

func TestInspectChartCRDsFail(t *testing.T) {
	in := inspectChart(t, map[string]string{"crds/x.yaml": "kind: CustomResourceDefinition\n"})
	wantVerdict(t, "crds", in, Fail, "CustomResourceDefinition crds/x.yaml is cluster-scoped")
}

func TestInspectChartTemplatedKindUnknown(t *testing.T) {
	in := inspectChart(t, map[string]string{"templates/x.yaml": "apiVersion: v1\nkind: {{ .Values.kind }}\nmetadata:\n  name: x\n"})
	wantVerdict(t, "kind", in, Unknown, "a document in templates/x.yaml names no literal kind")
	in = inspectChart(t, map[string]string{"templates/x.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\n  namespace: {{ .Values.ns }}\n"})
	wantVerdict(t, "ns", in, Unknown, "ConfigMap x takes its namespace from a template value")
	in = inspectChart(t, map[string]string{"templates/x.yaml": "{{- if .Values.a }}\nkind: ConfigMap\n{{- else }}\nkind: Secret\n{{- end }}\napiVersion: v1\n"})
	wantVerdict(t, "branches", in, Unknown, "is built by a template Pacto cannot read")
	in = inspectChart(t, map[string]string{"templates/x.yaml": "apiVersion: rbac.authorization.k8s.io/v1\nkind: RoleBinding\nmetadata:\n  name: rb\n" +
		"roleRef:\n  kind: {{ .Values.kind }}\n  name: r\n"})
	wantVerdict(t, "roleRef", in, Unknown, "Pacto cannot read the roleRef of RoleBinding rb")
	in = inspectChart(t, map[string]string{"templates/x.yaml": "apiVersion: rbac.authorization.k8s.io/v1\nkind: RoleBinding\nmetadata:\n  name: rb\n" +
		"roleRef:\n  kind: Role\n  name: r\nsubjects:\n- kind: ServiceAccount\n  name: {{ .Values.sa }}\n  namespace: {{ .Values.ns }}\n"})
	wantVerdict(t, "subject", in, Unknown, "grants ServiceAccount (templated) of (templated), outside prod")
}

func TestInspectChartIncludeOnlyDocUnknown(t *testing.T) {
	for name, tpl := range map[string]string{
		"no kind":  "metadata:\n  name: x\n",
		"include":  "{{ include \"app.object\" . }}\n",
		"template": "{{- template \"app.object\" . }}\n",
		"tpl":      "{{ tpl .Values.raw . | nindent 0 }}\nkind: ConfigMap\napiVersion: v1\n",
		"metadata": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  {{- include \"app.ns\" . | nindent 2 }}\n",
	} {
		in := inspectChart(t, map[string]string{"templates/x.yaml": tpl})
		if v := in.Verdict(); v.Result != Unknown {
			t.Errorf("%s: want Unknown, got %+v", name, in)
		}
	}
	// Any injection on a RoleBinding may set a subject.
	in := inspectChart(t, map[string]string{"templates/x.yaml": "apiVersion: rbac.authorization.k8s.io/v1\nkind: RoleBinding\nmetadata:\n  name: rb\n" +
		"  labels:\n    {{- include \"app.labels\" . | nindent 4 }}\nroleRef:\n  kind: Role\n  name: r\n"})
	wantVerdict(t, "binding", in, Unknown, "is built by a template Pacto cannot read")
	// A whole-template include with content beside it is unknown.
	in = inspectChart(t, map[string]string{"templates/x.yaml": "apiVersion: v1\nkind: ConfigMap\n{{ include \"app.extra\" . }}\n"})
	wantVerdict(t, "beside", in, Unknown, "is built by a template Pacto cannot read")
	// Deeper injections (labels) leave the object judged.
	in = inspectChart(t, map[string]string{"templates/x.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ include \"app.fullname\" . }}\n" +
		"  labels:\n    {{- include \"app.labels\" . | nindent 4 }}\n"})
	if v := wantVerdict(t, "labels", in, Pass, ""); v.Citation["objects"] != "ConfigMap in templates/x.yaml" {
		t.Fatalf("citation %+v", v.Citation)
	}
}

func TestInspectChartSubdirAndJSON(t *testing.T) {
	crb := "apiVersion: rbac.authorization.k8s.io/v1\nkind: ClusterRoleBinding\nmetadata:\n  name: crb\n"
	in := inspectChart(t, map[string]string{
		"templates/rbac/crb.yaml": crb,
		"templates/crb.json":      `{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRoleBinding", "metadata": {"name": "crb2"}}`,
		"templates/_helpers.tpl":  "{{ define \"x\" }}kind: ClusterRole{{ end }}",
		"templates/NOTES.txt":     "kind: ClusterRole\n",
	})
	v := wantVerdict(t, "subdir", in, Fail, "ClusterRoleBinding crb is cluster-scoped")
	if v.Citation["refused"] != "ClusterRoleBinding crb2 is cluster-scoped; ClusterRoleBinding crb is cluster-scoped" {
		t.Fatalf("refused %+v", v.Citation)
	}
}

func TestInspectChartReleaseNamespacePass(t *testing.T) {
	for _, ns := range []string{"{{ .Release.Namespace }}", "{{- .Release.Namespace -}}", "{{ .Release.Namespace | quote }}"} {
		in := inspectChart(t, map[string]string{"templates/cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: c\n  namespace: " + ns + "\n" +
			"---\napiVersion: rbac.authorization.k8s.io/v1\nkind: RoleBinding\nmetadata:\n  name: rb\nroleRef:\n  kind: Role\n  name: r\n" +
			"subjects:\n- kind: ServiceAccount\n  name: sa\n  namespace: " + ns + "\n"})
		if in.Verdict().Result != Pass || in.Objects[0].Namespace != "prod" {
			t.Errorf("%s: %+v", ns, in)
		}
	}
}

func TestInspectChartSubchartUnknown(t *testing.T) {
	in := inspectChart(t, map[string]string{"charts/sub-1.0.0.tgz": "x"})
	wantVerdict(t, "subchart", in, Unknown, "the chart carries a subchart: charts/sub-1.0.0.tgz")
	in = inspectChart(t, map[string]string{"templates/x.yaml": "kind: [\n"})
	wantVerdict(t, "unparsable", in, Unknown, "templates/x.yaml is not readable YAML")
}

func TestInspectUnknownKindUnknown(t *testing.T) {
	in := inspectFlux(t, map[string]string{"a.yaml": obj("example.com/v1", "Widget", "w", "")})
	wantVerdict(t, "unknown", in, Unknown, "Widget w: Pacto cannot tell whether kind example.com/Widget is namespaced")
	scope := ChainScope(BuiltinScope, func(group, kind string) (bool, bool) { return true, group == "example.com" })
	in = InspectArtifact(FluxContentMediaType, tgz(t, map[string]string{"a.yaml": obj("example.com/v1", "Widget", "w", "")}), ManifestTarget{Namespace: "prod"}, scope)
	wantVerdict(t, "served", in, Pass, "")
	in = inspectFlux(t, map[string]string{"a.yaml": "- just\n- a list\n"})
	wantVerdict(t, "no kind", in, Unknown, "names no literal kind")
}

func TestInspectBounds(t *testing.T) {
	big := strings.Repeat("#", MaxManifestBytes+1)
	in := inspectFlux(t, map[string]string{"a.yaml": big})
	wantVerdict(t, "bytes", in, Unknown, "more than 16 MiB uncompressed")
	var docs []string
	for i := range MaxManifestObjects + 1 {
		docs = append(docs, obj("v1", "ConfigMap", fmt.Sprint("c", i), ""))
	}
	in = inspectFlux(t, map[string]string{"a.yaml": strings.Join(docs, "---\n")})
	wantVerdict(t, "objects", in, Unknown, "more than 1000 objects")
	in = InspectArtifact(FluxContentMediaType, []byte("not gzip"), ManifestTarget{Namespace: "prod"}, BuiltinScope)
	wantVerdict(t, "not gzip", in, Unknown, "not a gzipped tar")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write([]byte("not a tar archive at all, but long enough to read a header from it............................................................................................................................................................................................................................................................................................................................................................................................................................................................................................"))
	_ = gz.Close()
	in = InspectArtifact(FluxContentMediaType, buf.Bytes(), ManifestTarget{Namespace: "prod"}, BuiltinScope)
	wantVerdict(t, "not tar", in, Unknown, "not a readable tar")
}

func TestInspectOtherMediaTypeUnknown(t *testing.T) {
	in := InspectArtifact("application/vnd.oci.image.layer.v1.tar", nil, ManifestTarget{Namespace: "prod"}, BuiltinScope)
	wantVerdict(t, "other", in, Unknown, "Pacto reads Flux, Helm and Argo CD OCI artifacts only")
}

// An Argo CD OCI source's default layer is read like Flux content: a
// Deployment and its Pacto in the Stage namespace pass, a Namespace is refused.
func TestInspectArgoCDLayer(t *testing.T) {
	ok := map[string]string{"deployment.yaml": obj("apps/v1", "Deployment", "payments", ""), "pacto.yaml": obj("pacto.trianalab.io/v1", "Pacto", "payments", "")}
	in := InspectArtifact(ArgoCDContentMediaType, tgz(t, ok), ManifestTarget{Namespace: "prod"}, BuiltinScope)
	wantVerdict(t, "argo pass", in, Pass, "")
	in = InspectArtifact(ArgoCDContentMediaType, tgz(t, map[string]string{"ns.yaml": obj("v1", "Namespace", "n", "")}), ManifestTarget{Namespace: "prod"}, BuiltinScope)
	wantVerdict(t, "argo refused", in, Fail, "Namespace n")
}

func TestInspectCitationBounded(t *testing.T) {
	var docs []string
	for i := range 60 {
		docs = append(docs, obj("v1", "Namespace", fmt.Sprint("n", i), ""))
	}
	v := inspectFlux(t, map[string]string{"a.yaml": strings.Join(docs, "---\n")}).Verdict()
	if !strings.HasSuffix(v.Citation["objects"], "ConfigMap n49 and 10 more") && !strings.HasSuffix(v.Citation["objects"], "Namespace n49 and 10 more") {
		t.Fatalf("objects %q", v.Citation["objects"])
	}
	if n := strings.Count(v.Citation["refused"], "; ") + 1; n != MaxListedObjects+1 || !strings.HasSuffix(v.Citation["refused"], "; and 10 more") {
		t.Fatalf("refused lists %d", n)
	}
}

func TestChainScopeBuiltinFirst(t *testing.T) {
	wrong := func(string, string) (bool, bool) { return true, true }
	if ns, known := ChainScope(BuiltinScope, wrong)("rbac.authorization.k8s.io", "ClusterRoleBinding"); ns || !known {
		t.Fatal("the builtin table wins")
	}
	if ns, known := ChainScope(BuiltinScope, wrong)("example.com", "Widget"); !ns || !known {
		t.Fatal("the mapper answers what the table does not know")
	}
}

// layerRegistry serves one layer, or an error.
type layerRegistry struct {
	mapRegistry
	mediaType string
	data      []byte
	err       error
	reads     *int
}

func (l layerRegistry) Layer(context.Context, string, int64) (string, []byte, error) {
	if l.reads != nil {
		*l.reads++
	}
	return l.mediaType, l.data, l.err
}

func TestInspectRevision(t *testing.T) {
	ctx := context.Background()
	reg := layerRegistry{mediaType: FluxContentMediaType, data: tgz(t, map[string]string{"a.yaml": obj("v1", "ConfigMap", "c", "")})}
	if in := InspectRevision(ctx, reg, "r/a", dA, ManifestTarget{Namespace: "prod"}, BuiltinScope); in.Verdict().Result != Pass {
		t.Fatalf("read: %+v", in)
	}
	in := InspectRevision(ctx, layerRegistry{err: errors.New("down")}, "r/a", dA, ManifestTarget{Namespace: "prod", Unread: []string{"x"}}, BuiltinScope)
	wantVerdict(t, "down", in, Unknown, "x; Pacto could not read the artifact sha256:aaaaaaaaaaaa: down")
	old := registryTimeout
	registryTimeout = 0
	defer func() { registryTimeout = old }()
	in = InspectRevision(ctx, hangingRegistry{}, "r/a", dA, ManifestTarget{Namespace: "prod"}, BuiltinScope)
	wantVerdict(t, "timeout", in, Unknown, "the registry did not answer within 0s")
}

// TestInspectRawInfra: every Crossplane kind is raw infrastructure, a
// refusal with RawInfraInArtifact when the inspection refuses it (profile
// idp) and a question for a person otherwise.
func TestInspectRawInfra(t *testing.T) {
	served := ChainScope(BuiltinScope, func(group, kind string) (bool, bool) { return kind != "Composition", group != "unknown.example.org" })
	infra := func(group, kind string) (bool, bool) {
		return kind == "PostgresClaim" || kind == "XPostgres" || kind == "Bucket", group != "hidden.example.org"
	}
	for _, c := range []struct{ name, doc, sentence string }{
		{"claim", obj("platform.example.org/v1", "PostgresClaim", "db", ""), "PostgresClaim db is raw infrastructure: declare a dependency on an offering"},
		{"composite", obj("platform.example.org/v1", "XPostgres", "db", ""), "XPostgres db is raw infrastructure"},
		{"managed resource", obj("s3.aws.example.io/v1beta1", "Bucket", "b", ""), "Bucket b is raw infrastructure"},
		{"XRD", obj("apiextensions.crossplane.io/v1", "CompositeResourceDefinition", "x", ""), "CompositeResourceDefinition x is raw infrastructure"},
		{"upbound managed resource", obj("rds.aws.upbound.io/v1beta1", "Instance", "i", ""), "Instance i is raw infrastructure"},
	} {
		in := InspectArtifact(FluxContentMediaType, tgz(t, map[string]string{"a.yaml": c.doc}), ManifestTarget{Namespace: "prod", Infra: infra}, served)
		if v := wantVerdict(t, c.name+" default", in, Unknown, c.sentence); v.Citation["code"] != "" {
			t.Errorf("%s: no code outside idp: %+v", c.name, v.Citation)
		}
		in.RefuseInfra = true
		if v := wantVerdict(t, c.name+" idp", in, Fail, c.sentence); v.Citation["code"] != CodeRawInfraInArtifact || !strings.Contains(v.Citation["refused"], c.sentence) {
			t.Errorf("%s: idp refuses with the code: %+v", c.name, v.Citation)
		}
	}
	// A group the inspector cannot classify waits for a person, under idp
	// too; a kind discovery serves but lists outside every infrastructure
	// category passes.
	for _, c := range []struct {
		name, doc string
		result    Result
		msg       string
	}{
		{"unserved", obj("unknown.example.org/v1", "Thing", "t", ""), Unknown, "cannot tell whether kind unknown.example.org/Thing is namespaced"},
		{"unclassified", obj("hidden.example.org/v1", "Thing", "t", ""), Unknown, "Thing t: Pacto cannot tell whether kind hidden.example.org/Thing is infrastructure"},
		{"plain", obj("platform.example.org/v1", "Settings", "s", ""), Pass, ""},
		{"core", obj("v1", "ConfigMap", "c", ""), Pass, ""},
	} {
		in := InspectArtifact(FluxContentMediaType, tgz(t, map[string]string{"a.yaml": c.doc}), ManifestTarget{Namespace: "prod", Infra: infra}, served)
		in.RefuseInfra = true
		wantVerdict(t, c.name, in, c.result, c.msg)
	}
	// A cluster-scoped Composition stays refused outside idp, and idp
	// refuses it as raw infrastructure too.
	comp := InspectArtifact(FluxContentMediaType, tgz(t, map[string]string{"a.yaml": obj("apiextensions.crossplane.io/v1", "Composition", "c", "")}),
		ManifestTarget{Namespace: "prod", Infra: infra}, served)
	wantVerdict(t, "composition default", comp, Fail, "the manifest inspector refuses what the version applies: Composition c is cluster-scoped")
	comp.RefuseInfra = true
	if v := wantVerdict(t, "composition idp", comp, Fail, "Composition c is cluster-scoped; Composition c is raw infrastructure"); v.Citation["code"] != CodeRawInfraInArtifact {
		t.Errorf("composition idp: %+v", v.Citation)
	}
	// Without discovery, Crossplane groups still count.
	in := inspectFlux(t, map[string]string{"a.yaml": obj("apiextensions.crossplane.io/v1", "Composition", "c", "")})
	in.RefuseInfra = true
	wantVerdict(t, "by group", in, Fail, "the version carries raw infrastructure: Composition c is raw infrastructure")
	// A delivery object is refused as one, never as infrastructure.
	in = InspectArtifact(FluxContentMediaType, tgz(t, map[string]string{"a.yaml": obj("kustomize.toolkit.fluxcd.io/v1", "Kustomization", "k", "")}),
		ManifestTarget{Namespace: "prod", Infra: infra}, served)
	if len(in.Infra) != 0 {
		t.Fatalf("delivery object: %+v", in)
	}
}
