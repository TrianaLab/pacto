package architecture

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/pkg/workloadshape"
)

// The Kargo promotion gate: one ClusterAnalysisTemplate a platform applies
// once, and the opt-in reader role for targets: k8s. Both are published
// verbatim on kargoPage, so these checks hold the page to its fail-closed exit
// policy, its least-privilege role and the released image.

const (
	kargoPage            = "gitops-kargo.md"
	kargoTemplateFixture = "kargo-cluster-analysis-template.yaml"
	kargoRBACFixture     = "kargo-impact-rbac.yaml"
)

type kargoEnv struct {
	Name  string  `yaml:"name"`
	Value *string `yaml:"value"`
}

type kargoTemplate struct {
	Kind string `yaml:"kind"`
	Spec struct {
		Args []struct {
			Name  string  `yaml:"name"`
			Value *string `yaml:"value"`
		} `yaml:"args"`
		Metrics []struct {
			Name         string `yaml:"name"`
			Count        int    `yaml:"count"`
			FailureLimit *int   `yaml:"failureLimit"`
			Provider     struct {
				Job struct {
					Spec struct {
						BackoffLimit *int `yaml:"backoffLimit"`
						Template     struct {
							Spec struct {
								RestartPolicy      string `yaml:"restartPolicy"`
								ServiceAccountName string `yaml:"serviceAccountName"`
								Containers         []struct {
									Image           string          `yaml:"image"`
									Command         []string        `yaml:"command"`
									Args            []string        `yaml:"args"`
									Env             []kargoEnv      `yaml:"env"`
									SecurityContext securityContext `yaml:"securityContext"`
									VolumeMounts    []struct {
										Name      string `yaml:"name"`
										MountPath string `yaml:"mountPath"`
										ReadOnly  bool   `yaml:"readOnly"`
									} `yaml:"volumeMounts"`
								} `yaml:"containers"`
								Volumes []struct {
									Name   string `yaml:"name"`
									Secret *struct {
										SecretName string `yaml:"secretName"`
										Optional   bool   `yaml:"optional"`
										Items      []struct {
											Key  string `yaml:"key"`
											Path string `yaml:"path"`
										} `yaml:"items"`
									} `yaml:"secret"`
								} `yaml:"volumes"`
							} `yaml:"spec"`
						} `yaml:"template"`
					} `yaml:"spec"`
				} `yaml:"job"`
			} `yaml:"provider"`
		} `yaml:"metrics"`
	} `yaml:"spec"`
}

func loadKargoTemplate(t *testing.T) kargoTemplate {
	t.Helper()
	var tpl kargoTemplate
	if err := yaml.Unmarshal([]byte(readRepoFile(t, gitopsFixture(kargoTemplateFixture))), &tpl); err != nil {
		t.Fatalf("cannot parse %s: %v", kargoTemplateFixture, err)
	}
	if tpl.Kind != "ClusterAnalysisTemplate" || len(tpl.Spec.Metrics) != 1 ||
		len(tpl.Spec.Metrics[0].Provider.Job.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf("%s must be a ClusterAnalysisTemplate with one job metric and one container", kargoTemplateFixture)
	}
	return tpl
}

// kargoScript is the one block the container runs with sh -c.
func kargoScript(t *testing.T, tpl kargoTemplate) string {
	t.Helper()
	c := tpl.Spec.Metrics[0].Provider.Job.Spec.Template.Spec.Containers[0]
	if !slices.Equal(c.Command, []string{"sh", "-c"}) || len(c.Args) != 1 {
		t.Fatalf("the container must run command [sh, -c] with the script as its one arg; got %v and %d args", c.Command, len(c.Args))
	}
	return c.Args[0]
}

func TestKargoPageIncludesTheTestedFixtures(t *testing.T) {
	page := readRepoFile(t, "integrations", "kubernetes", "docs", kargoPage)
	for _, fixture := range []string{kargoTemplateFixture, kargoRBACFixture} {
		include := `--8<-- "tests/acceptance/kind/fixtures/gitops/` + fixture + `"`
		if !strings.Contains(page, include) {
			t.Errorf("%s does not include %s verbatim; expected the line %s", kargoPage, fixture, include)
		}
	}
	if !strings.Contains(readRepoFile(t, "integrations", "kubernetes", "docs", gitopsPage), "("+kargoPage) {
		t.Errorf("%s does not link to %s", gitopsPage, kargoPage)
	}
}

// TestKargoTemplateExitPolicy runs the published script under a POSIX sh with
// a stub pacto: exit 0 passes, exit 2 passes only with allow-unverified
// exactly "true", anything else fails, and a bad targets value fails before
// pacto runs.
func TestKargoTemplateExitPolicy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the template runs under a POSIX sh")
	}
	script := kargoScript(t, loadKargoTemplate(t))
	bin := t.TempDir()
	calls := filepath.Join(bin, "calls")
	stub := "#!/bin/sh\nprintf '%s|' \"$@\" > \"$CALLS\"\nexit \"$STUB_RC\"\n"
	if err := os.WriteFile(filepath.Join(bin, "pacto"), []byte(stub), 0o755); err != nil { //nolint:gosec // an executable test stub
		t.Fatal(err)
	}
	const newRef = "oci://reg.example/acme/payments@sha256:abc"
	for _, tc := range []struct {
		name, rc, targets, allow string
		pass                     bool
		args                     string // what the stub received, "" for no call
	}{
		{"clear passes", "0", "snapshot", "false", true, "impact|--running|" + newRef + "|--local||"},
		{"blocked fails", "1", "snapshot", "false", false, "impact|--running|" + newRef + "|--local||"},
		{"unverified fails", "2", "snapshot", "false", false, "impact|--running|" + newRef + "|--local||"},
		{"unverified allowed passes", "2", "snapshot", "true", true, "impact|--running|" + newRef + "|--local||"},
		{"unverified with yes fails", "2", "snapshot", "yes", false, "impact|--running|" + newRef + "|--local||"},
		{"missing binary fails", "127", "snapshot", "true", false, "impact|--running|" + newRef + "|--local||"},
		{"signal fails", "137", "snapshot", "true", false, "impact|--running|" + newRef + "|--local||"},
		{"k8s adds the flag", "0", "k8s", "false", true, "impact|--running|" + newRef + "|--local||--k8s|"},
		{"bogus targets fails", "0", "bogus", "true", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_ = os.Remove(calls)
			cmd := exec.Command("sh", "-c", script)
			cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "CALLS=" + calls, "STUB_RC=" + tc.rc,
				"NEW=" + newRef, "TARGETS=" + tc.targets, "ALLOW_UNVERIFIED=" + tc.allow}
			out, err := cmd.CombinedOutput()
			if (err == nil) != tc.pass {
				t.Fatalf("pass = %v, want %v; output:\n%s", err == nil, tc.pass, out)
			}
			if err != nil && cmd.ProcessState.ExitCode() != 1 {
				t.Errorf("a failure exits %d, want 1", cmd.ProcessState.ExitCode())
			}
			got, _ := os.ReadFile(calls) //nolint:gosec // the stub's own output file
			if string(got) != tc.args {
				t.Errorf("pacto received %q, want %q", got, tc.args)
			}
		})
	}
}

// TestKargoTemplateArgsOnlyInEnv: Rollouts interpolates {{args.*}} into the
// Job spec, so an arg spliced into the script would be shell text a Stage
// writes. Every arg reaches the container as an env value, and the env a
// Stage cannot touch comes after it, so a duplicated name never wins.
func TestKargoTemplateArgsOnlyInEnv(t *testing.T) {
	tpl := loadKargoTemplate(t)
	script := kargoScript(t, tpl)
	if strings.Contains(script, "{{") {
		t.Fatal("the script interpolates a template arg; read it from an env value instead")
	}
	c := tpl.Spec.Metrics[0].Provider.Job.Spec.Template.Spec.Containers[0]
	want := map[string]string{"NEW": "{{args.new}}", "TARGETS": "{{args.targets}}", "ALLOW_UNVERIFIED": "{{args.allow-unverified}}"}
	lastArg, firstLiteral := -1, len(c.Env)
	for i, e := range c.Env {
		v := ""
		if e.Value != nil {
			v = *e.Value
		}
		if strings.Contains(v, "{{") {
			if want[e.Name] != v {
				t.Errorf("env %s = %q; only NEW, TARGETS and ALLOW_UNVERIFIED read args", e.Name, v)
			}
			delete(want, e.Name)
			lastArg = i
		} else if firstLiteral == len(c.Env) {
			firstLiteral = i
		}
	}
	if len(want) != 0 {
		t.Errorf("args missing from env: %v", want)
	}
	if lastArg > firstLiteral {
		t.Error("an env value fed by an arg follows a literal one; list the args first so the literals win")
	}
	var declared []string
	for _, a := range tpl.Spec.Args {
		declared = append(declared, a.Name)
	}
	if !slices.Equal(declared, []string{"new", "targets", "allow-unverified"}) {
		t.Errorf("template args = %v, want [new targets allow-unverified]", declared)
	}
}

// TestKargoTemplateNoStaticRegistryCredentials: env credentials are sent to
// every host Pacto pulls from, and a Stage picks the host. Credentials come
// only from a mounted Docker config, matched per host, and no arg can mark a
// registry insecure.
func TestKargoTemplateNoStaticRegistryCredentials(t *testing.T) {
	tpl := loadKargoTemplate(t)
	pod := tpl.Spec.Metrics[0].Provider.Job.Spec.Template.Spec
	c := pod.Containers[0]
	env := map[string]string{}
	for _, e := range c.Env {
		if e.Value != nil {
			env[e.Name] = *e.Value
		}
		if strings.HasPrefix(e.Name, "PACTO_REGISTRY_") {
			t.Errorf("env %s carries registry credentials to any host", e.Name)
		}
	}
	if v, ok := env["PACTO_INSECURE_REGISTRIES"]; !ok || v != "" {
		t.Errorf("PACTO_INSECURE_REGISTRIES must be a literal empty value, got %q (set %v)", v, ok)
	}
	mount := ""
	for _, m := range c.VolumeMounts {
		if m.Name == "pacto-registry" && m.ReadOnly {
			mount = m.MountPath
		}
	}
	if mount == "" || env["DOCKER_CONFIG"] != mount {
		t.Errorf("DOCKER_CONFIG %q must point at the read-only pacto-registry mount %q", env["DOCKER_CONFIG"], mount)
	}
	for _, v := range pod.Volumes {
		if v.Name != "pacto-registry" {
			continue
		}
		s := v.Secret
		if s == nil || s.SecretName != "pacto-registry" || !s.Optional || len(s.Items) != 1 ||
			s.Items[0].Key != ".dockerconfigjson" || s.Items[0].Path != "config.json" {
			t.Errorf("pacto-registry must be the optional Secret pacto-registry with .dockerconfigjson as config.json")
		}
		return
	}
	t.Error("no pacto-registry volume")
}

// TestKargoTemplateFailsClosed: one try, no retry that could turn a failure
// into a pass, and a hardened container.
func TestKargoTemplateFailsClosed(t *testing.T) {
	tpl := loadKargoTemplate(t)
	m := tpl.Spec.Metrics[0]
	job := m.Provider.Job.Spec
	if m.Count != 1 || m.FailureLimit == nil || *m.FailureLimit != 0 || job.BackoffLimit == nil || *job.BackoffLimit != 0 ||
		job.Template.Spec.RestartPolicy != "Never" || job.Template.Spec.ServiceAccountName != "pacto-impact" {
		t.Error("the metric must run once (count 1, failureLimit 0, backoffLimit 0, restartPolicy Never) as ServiceAccount pacto-impact")
	}
	sc := job.Template.Spec.Containers[0].SecurityContext
	if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot || sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem ||
		sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation || !slices.Equal(sc.Capabilities.Drop, []string{"ALL"}) {
		t.Error("the container must be hardened: runAsNonRoot, readOnlyRootFilesystem, no privilege escalation, drop ALL")
	}
	for _, a := range tpl.Spec.Args {
		switch {
		case a.Name == "targets" && (a.Value == nil || *a.Value != "snapshot"),
			a.Name == "allow-unverified" && (a.Value == nil || *a.Value != "false"),
			a.Name == "new" && a.Value != nil:
			t.Errorf("arg %s has the wrong default", a.Name)
		}
	}
}

type rbacRule struct {
	APIGroups     []string `yaml:"apiGroups"`
	Resources     []string `yaml:"resources"`
	ResourceNames []string `yaml:"resourceNames"`
	Verbs         []string `yaml:"verbs"`
}

// TestKargoReaderRoleMatchesK8sCalls: the opt-in role is exactly what
// `pacto impact --k8s` reads. The cluster identity (namespaces/kube-system),
// the Pacto CRs, and the workload kinds, whose unread lists keep a --running
// answer from clearing. Discovery needs no rule and a Forbidden node list is
// tolerated, so neither is granted.
func TestKargoReaderRoleMatchesK8sCalls(t *testing.T) {
	var role struct {
		Kind  string     `yaml:"kind"`
		Rules []rbacRule `yaml:"rules"`
	}
	if err := yaml.Unmarshal([]byte(readRepoFile(t, gitopsFixture(kargoRBACFixture))), &role); err != nil {
		t.Fatalf("cannot parse %s: %v", kargoRBACFixture, err)
	}
	workloads := map[string][2]string{
		"Deployment": {"apps", "deployments"}, "StatefulSet": {"apps", "statefulsets"},
		"DaemonSet": {"apps", "daemonsets"}, "CronJob": {"batch", "cronjobs"},
	}
	want := []rbacRule{
		{APIGroups: []string{""}, Resources: []string{"namespaces"}, ResourceNames: []string{"kube-system"}, Verbs: []string{"get"}},
		{APIGroups: []string{"pacto.trianalab.io"}, Resources: []string{"pactos"}, Verbs: []string{"get", "list"}},
	}
	for _, kind := range workloadshape.WorkloadKinds {
		gr, ok := workloads[kind]
		if !ok {
			t.Fatalf("the workload source reads %s, which the reader role does not map", kind)
		}
		want = append(want, rbacRule{APIGroups: []string{gr[0]}, Resources: []string{gr[1]}, Verbs: []string{"list"}})
	}
	var got []rbacRule
	for _, r := range role.Rules { // one rule per group and resource, so the comparison is exact
		for _, res := range r.Resources {
			got = append(got, rbacRule{APIGroups: r.APIGroups, Resources: []string{res}, ResourceNames: r.ResourceNames, Verbs: r.Verbs})
		}
	}
	gj, _ := json.Marshal(got)
	wj, _ := json.Marshal(want)
	if role.Kind != "ClusterRole" || string(gj) != string(wj) {
		t.Errorf("%s rules\n got %s\nwant %s", kargoRBACFixture, gj, wj)
	}
}

// TestKargoTemplatePinsTheReleasedImage: the fixture is YAML, so docs_check
// (h), which scans Markdown, never sees its tag; apply-release-plan rewrites
// it and this test holds it to the release manifest.
func TestKargoTemplatePinsTheReleasedImage(t *testing.T) {
	var manifest struct {
		Units map[string]struct {
			Coordinate string `json:"coordinate"`
			Tag        string `json:"tag"`
		} `json:"units"`
	}
	if err := json.Unmarshal([]byte(readRepoFile(t, "release", "release-manifest.json")), &manifest); err != nil {
		t.Fatal(err)
	}
	img := manifest.Units["dashboard-image"]
	want := img.Coordinate + ":" + img.Tag
	if got := loadKargoTemplate(t).Spec.Metrics[0].Provider.Job.Spec.Template.Spec.Containers[0].Image; got != want {
		t.Errorf("%s runs %s, want the released %s", kargoTemplateFixture, got, want)
	}
	if !strings.Contains(readRepoFile(t, "release", "scripts", "apply-release-plan.mjs"), gitopsFixture(kargoTemplateFixture)) {
		t.Errorf("apply-release-plan.mjs does not rewrite the image tag in %s", kargoTemplateFixture)
	}
}
