package delivery

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"strings"
	"testing"
)

// orderedTgz is a gzipped tar of the name, content pairs in order, repeats
// included.
func orderedTgz(pairs ...string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for i := 0; i+1 < len(pairs); i += 2 {
		_ = tw.WriteHeader(&tar.Header{Name: pairs[i], Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(pairs[i+1]))})
		_, _ = tw.Write([]byte(pairs[i+1]))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

const crbDoc = "apiVersion: rbac.authorization.k8s.io/v1\nkind: ClusterRoleBinding\nmetadata:\n  name: crb\n"

// TestInspectChartOutputActionsAreInjections: a line-only action that is
// not control flow renders content, so a template made of one is never Pass.
func TestInspectChartOutputActionsAreInjections(t *testing.T) {
	for name, tpl := range map[string]string{
		"files":  "{{ .Files.Get \"extra/crb.yaml\" }}\n",
		"values": "{{ .Values.raw }}\n",
		"toJson": "{{ toJson .Values.obj }}\n",
		"printf": "{{ printf \"%s\" .Values.raw }}\n",
	} {
		in := inspectChart(t, map[string]string{"templates/x.yaml": tpl, "extra/crb.yaml": crbDoc, "templates/y.yaml": obj("v1", "ConfigMap", "c", "")})
		wantVerdict(t, name, in, Unknown, "is built by a template Pacto cannot read")
	}
	// Control flow, comments and assignments render nothing.
	in := inspectChart(t, map[string]string{"templates/x.yaml": "{{- /* a comment */ -}}\n{{- $x := .Values.a }}\n{{- if $x }}\n" +
		obj("v1", "ConfigMap", "c", "") + "{{- else if .Values.b }}\n{{- end }}\n{{- range .Values.l }}\n{{- end }}\n{{- with .Values.w }}\n{{- end }}\n"})
	wantVerdict(t, "control", in, Pass, "every one of the 1 objects")
}

// TestInspectScalarWhereAMappingBelongs: an action beside metadata, roleRef
// or subjects was replaced by a scalar, so what it renders is unknown.
func TestInspectScalarWhereAMappingBelongs(t *testing.T) {
	in := inspectChart(t, map[string]string{"templates/x.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata: {{ include \"meta\" . | nindent 2 }}\n"})
	wantVerdict(t, "metadata", in, Unknown, "is built by a template Pacto cannot read")
	in = inspectChart(t, map[string]string{"templates/x.yaml": "apiVersion: rbac.authorization.k8s.io/v1\nkind: RoleBinding\nmetadata:\n  name: rb\n" +
		"roleRef:\n  kind: Role\n  name: r\nsubjects: {{ toYaml .Values.subjects | nindent 2 }}\n"})
	wantVerdict(t, "subjects", in, Unknown, "is built by a template Pacto cannot read")
	in = inspectFlux(t, map[string]string{"a.yaml": "apiVersion: rbac.authorization.k8s.io/v1\nkind: RoleBinding\nmetadata:\n  name: rb\n" +
		"roleRef:\n  kind: Role\n  name: r\nsubjects:\n"})
	wantVerdict(t, "no subjects", in, Pass, "every one of the 1 objects")
}

// TestInspectAliasesAndMergeKeysUnknown: an alias or a merge key hides the
// namespace or subjects the applier resolves.
func TestInspectAliasesAndMergeKeysUnknown(t *testing.T) {
	for name, doc := range map[string]string{
		"alias":     "apiVersion: v1\nkind: ConfigMap\nx: &m {name: a, namespace: kube-system}\nmetadata: *m\n",
		"merge":     "apiVersion: v1\nkind: ConfigMap\nx: &m {namespace: kube-system}\nmetadata: {<<: *m, name: a}\n",
		"top merge": "apiVersion: v1\nkind: ConfigMap\n<<: {metadata: {namespace: kube-system}}\nmetadata: {name: a}\n",
		"subjects": "apiVersion: rbac.authorization.k8s.io/v1\nkind: RoleBinding\nx: &s [{kind: Group, name: system:authenticated}]\n" +
			"metadata: {name: rb}\nroleRef: {kind: Role, name: r}\nsubjects: *s\n",
	} {
		wantVerdict(t, name, inspectFlux(t, map[string]string{"a.yaml": doc}), Unknown, "is built by a template Pacto cannot read")
	}
}

// TestInspectDuplicateTarEntriesUnknown: extracting keeps the last entry of
// a name, so a repeated name is never judged on its first.
func TestInspectDuplicateTarEntriesUnknown(t *testing.T) {
	layer := orderedTgz("kustomization.yaml", "resources: [a.yaml]\n", "a.yaml", obj("v1", "ConfigMap", "c", ""), "./a.yaml", crbDoc)
	in := InspectArtifact(FluxContentMediaType, layer, ManifestTarget{Namespace: "prod"}, BuiltinScope)
	wantVerdict(t, "duplicate", in, Unknown, "the artifact holds a.yaml twice")
}

// TestInspectBuildsTheApplierPath: the inspector judges the directory the
// Kustomization builds, not a decoy at the artifact root.
func TestInspectBuildsTheApplierPath(t *testing.T) {
	stage := StageObject{Metadata: ObjectMeta{Namespace: "prod"}, Spec: StageSpec{ApplyRef: ApplyRef{Name: "payments"}}}
	k := kustomizationOf("prod", "payments", ApplierFlux, "", "payments")
	k.Spec.Path = "./deploy"
	tg := ManifestTargetOf(stage, nil, []KustomizationView{k}, nil)
	files := map[string]string{"kustomization.yaml": "resources: [ok.yaml]\n", "ok.yaml": obj("v1", "ConfigMap", "c", ""), "deploy/crb.yaml": crbDoc}
	wantVerdict(t, "path", InspectArtifact(FluxContentMediaType, tgz(t, files), tg, BuiltinScope), Fail, "ClusterRoleBinding crb is cluster-scoped")
	tg.Path = "missing"
	wantVerdict(t, "missing path", InspectArtifact(FluxContentMediaType, tgz(t, files), tg, BuiltinScope), Unknown, "holds nothing under missing")
	other := kustomizationOf("prod", "other", ApplierFlux, "", "payments")
	tg = ManifestTargetOf(stage, nil, []KustomizationView{k, other}, nil)
	if !strings.Contains(strings.Join(tg.Unread, "|"), "the Kustomizations build different paths of the artifact: , deploy") {
		t.Fatalf("two paths: %+v", tg)
	}
	stage.Spec.ApplyRef = ApplyRef{Kind: ApplierArgoCD, Name: "payments"}
	tg = ManifestTargetOf(stage, map[string]any{"spec": map[string]any{"source": map[string]any{"path": "deploy/",
		"directory": map[string]any{"jsonnet": map[string]any{}}}}}, nil, nil)
	if tg.Path != "deploy" || strings.Join(tg.Unread, "|") != "Application payments transforms the artifact: spec.source.directory.jsonnet" {
		t.Fatalf("argo path: %+v", tg)
	}
}

// TestInspectForeignKustomizationIgnored: a Kustomization in another
// namespace naming the Stage's source leaves the verdict alone.
func TestInspectForeignKustomizationIgnored(t *testing.T) {
	stage := StageObject{Metadata: ObjectMeta{Namespace: "team-b"}, Spec: StageSpec{ApplyRef: ApplyRef{Name: "payments"}}}
	evil := kustomizationOf("team-a", "evil", ApplierFlux, "team-b", "payments")
	evil.Spec.Patches = []byte(`[]`)
	tg := ManifestTargetOf(stage, nil, []KustomizationView{evil}, nil)
	in := InspectArtifact(FluxContentMediaType, tgz(t, map[string]string{"a.yaml": obj("v1", "ConfigMap", "c", "")}), tg, BuiltinScope)
	wantVerdict(t, "foreign", in, Pass, "every one of the 1 objects stays in team-b")
}

// TestInspectNestedKustomizationAndJsonnetUnknown: with no root
// kustomization, a nested one and a jsonnet file are built by the applier
// in ways Pacto does not read.
func TestInspectNestedKustomizationAndJsonnetUnknown(t *testing.T) {
	for _, name := range []string{"sub/Kustomization", "sub/kustomization.yaml"} {
		in := inspectFlux(t, map[string]string{name: "namespace: kube-system\n", "sub/cm.yaml": obj("v1", "ConfigMap", "c", "")})
		wantVerdict(t, name, in, Unknown, "sub holds a kustomization, which Pacto does not read")
	}
	wantVerdict(t, "jsonnet", inspectFlux(t, map[string]string{"b.jsonnet": "{}"}), Unknown, "b.jsonnet is jsonnet, which Pacto does not read")
}

// TestInspectUnreadBounded: unreadable documents count toward the object
// bound, and the unread list a verdict carries is bounded.
func TestInspectUnreadBounded(t *testing.T) {
	in := inspectFlux(t, map[string]string{"a.yaml": strings.Repeat("---\nfoo: 1\n", 5000)})
	v := in.Verdict()
	if v.Result != Unknown || len(v.Message) > 8000 || len(v.Citation["unread"]) > 8000 ||
		!strings.Contains(v.Citation["unread"], fmt.Sprintf("and %d more", MaxManifestObjects+1-MaxListedObjects)) {
		t.Fatalf("unread %d bytes: %q", len(v.Citation["unread"]), v.Citation["unread"][max(0, len(v.Citation["unread"])-200):])
	}
}

// TestInspectSentencesNeverHoldTheSeparator: readers split a citation on
// "; ", so an artifact name holding one stays one sentence.
func TestInspectSentencesNeverHoldTheSeparator(t *testing.T) {
	in := inspectChart(t, map[string]string{"crds/a; b.yaml": "x"})
	if v := in.Verdict(); v.Citation["refused"] != "CustomResourceDefinition crds/a, b.yaml is cluster-scoped" {
		t.Fatalf("refused %q", v.Citation["refused"])
	}
}

// TestInspectNeutralRefusalLead: a delivery object in the Stage's own
// namespace is refused without claiming it reaches outside.
func TestInspectNeutralRefusalLead(t *testing.T) {
	in := inspectFlux(t, map[string]string{"a.yaml": obj("helm.toolkit.fluxcd.io/v2", "HelmRelease", "x", "prod")})
	v := wantVerdict(t, "delivery object", in, Fail, "the manifest inspector refuses what the version applies: HelmRelease x is a delivery or applier object")
	if strings.Contains(v.Message, "reaches outside") {
		t.Fatalf("message %q", v.Message)
	}
}
