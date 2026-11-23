package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

// installHub is a hub whose pending member eu-prod publishes, and whose
// detection advertises its registry and chart version beside the candidates:
// an Argo CD Secret stating eu-prod's UID and a Flux kubeconfig ref.
func installHub(t *testing.T, chartVersion string) (*clusterEnv, string) {
	t.Helper()
	e := newClusterEnv(t, capabilities.ReferrersNative)
	m := newJoinMember(euProd, "eu-prod")
	e.publish(t, m, m.key)
	dir := t.TempDir()
	b, _ := json.Marshal(detect.Result{SchemaVersion: detect.SchemaVersion, AsOf: time.Now(), Cluster: clusterHubUID,
		Installation: &detect.Installation{Registry: "r", Targets: e.repo, ChartVersion: chartVersion}})
	c, _ := detect.EncodeClusters(detect.Clusters{AsOf: time.Now(), Sources: []detect.InventorySource{{Source: detect.SourceArgoCD, Completeness: detect.Complete},
		{Source: detect.SourceFlux, Completeness: detect.Complete}},
		Candidates: []detect.Candidate{{Source: detect.SourceArgoCD, Kind: "Secret", Namespace: "gitops", Name: "eu", UID: euProd, UIDSource: "annotation"},
			{Source: detect.SourceFlux, Kind: "Secret", Namespace: "flux-system", Name: "us", KubeConfig: &detect.Object{Kind: "Secret", Namespace: "flux-system", Name: "us"}}}})
	for name, data := range map[string][]byte{detect.ConfigMapKey: b, detect.ClustersConfigMapKey: c} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return e, filepath.Join(dir, detect.ConfigMapKey)
}

func TestClusterInstallPrintsYAML(t *testing.T) {
	e, det := installHub(t, "4.6.0")
	out, errOut, err := runRoot(t, e.d, nil, "cluster", "install", "--from", "argocd", "--detected", det, "--hub-url", "https://hub.acme.example")
	if err != nil {
		t.Fatal(err)
	}
	host := strings.TrimSuffix(e.repo, "/"+delivery.TargetSnapshotRepository)
	if !strings.Contains(out, "kind: ApplicationSet\n") || !strings.Contains(out, "namespace: gitops\n") || !strings.Contains(out, "registry: "+host+"\n") ||
		!strings.Contains(out, "url: https://hub.acme.example\n") || !strings.Contains(out, "targetRevision: 4.6.0\n") || strings.Contains(out, "warning") {
		t.Fatalf("stdout %s", out)
	}
	if !strings.Contains(errOut, "warning: Argo CD cluster Secret gitops/eu states the UID of a member that runs Pacto") {
		t.Fatalf("stderr %q", errOut)
	}
	out, errOut, err = runRoot(t, e.d, nil, "cluster", "install", "--from", "flux", "--detected", det)
	if err != nil || !strings.Contains(out, "kind: HelmRelease\n") || !strings.Contains(errOut, "pass --push") || strings.Contains(out, "bootstrap") {
		t.Fatalf("flux %v %q %q", err, out, errOut)
	}
}

func TestClusterInstallJSONOneDocument(t *testing.T) {
	e, det := installHub(t, "4.6.0")
	out, _, err := runRoot(t, e.d, nil, "cluster", "install", "--from", "argocd", "--detected", det, "--output-format", "json")
	var res memberInstall
	if err != nil || json.Unmarshal([]byte(out), &res) != nil {
		t.Fatalf("%v %q", err, out)
	}
	if res.Engine != "argocd" || len(res.Files) != 2 || len(res.Warnings) != 1 || res.Reference != "" {
		t.Fatalf("%+v", res)
	}
}

// TestClusterInstallPushPrintsDigest: the push carries exactly the files the
// command prints without it, and the bootstrap pins its digest.
func TestClusterInstallPushPrintsDigest(t *testing.T) {
	e, det := installHub(t, "4.6.0")
	plain, _, err := runRoot(t, e.d, nil, "cluster", "install", "--from", "flux", "--detected", det, "--output-format", "json")
	var want memberInstall
	if err != nil || json.Unmarshal([]byte(plain), &want) != nil {
		t.Fatalf("%v %q", err, plain)
	}
	out, errOut, err := runRoot(t, e.d, nil, "cluster", "install", "--from", "flux", "--detected", det, "--push")
	if err != nil {
		t.Fatal(err)
	}
	desc, err := e.registry().Resolve(t.Context(), deliveryrecord.MemberInstallTag("flux", mustDigest(t, errOut)))
	if err != nil {
		t.Fatalf("%v; stderr %q", err, errOut)
	}
	if !strings.Contains(out, "digest: "+desc.Digest.String()+"\n") || !strings.Contains(out, "kind: Kustomization\n") || strings.Contains(out, "kind: HelmRelease") {
		t.Fatalf("stdout %s", out)
	}
	if desc.Digest.String() != pushedDigest(t, e.registry(), want.Files) {
		t.Fatal("the pushed files are not the printed ones")
	}
	out, errOut, err = runRoot(t, e.d, nil, "cluster", "install", "--from", "argocd", "--detected", det, "--push")
	if err != nil || !strings.HasPrefix(out, e.repo+"@sha256:") || !strings.Contains(errOut, "point an Argo CD Application at it") {
		t.Fatalf("argocd %v %q %q", err, out, errOut)
	}
}

// mustDigest is the digest a push line on stderr names.
func mustDigest(t *testing.T, stderr string) digest.Digest {
	t.Helper()
	_, after, _ := strings.Cut(stderr, "@")
	d, err := digest.Parse(after[:min(len(after), 71)])
	if err != nil {
		t.Fatalf("no digest in %q: %v", stderr, err)
	}
	return d
}

// pushedDigest is the digest the member install of files has when pushed.
func pushedDigest(t *testing.T, repo *remote.Repository, files []delivery.InstallFile) string {
	t.Helper()
	desc, err := deliveryrecord.PushMemberInstall(t.Context(), repo, "flux", delivery.InstallArchive(files))
	if err != nil {
		t.Fatal(err)
	}
	return desc.Digest.String()
}

func TestClusterInstallDefaultsFromDetection(t *testing.T) {
	e, det := installHub(t, "4.6.0")
	out, _, err := runRoot(t, e.d, nil, "cluster", "install", "--from", "argocd", "--detected", det, "--chart-version", "4.7.1")
	if err != nil || !strings.Contains(out, "targetRevision: 4.7.1\n") {
		t.Fatalf("override %v %s", err, out)
	}
	e, det = installHub(t, "")
	if _, _, err := runRoot(t, e.d, nil, "cluster", "install", "--from", "argocd", "--detected", det); err == nil || !strings.Contains(err.Error(), "--chart-version") {
		t.Fatalf("no advertised version: %v", err)
	}
	// Without a detection, the registry and version come from the flags,
	// and Flux has no candidate to install through.
	out, _, err = runRoot(t, e.d, nil, "cluster", "install", "--from", "argocd", "--registry", e.repo, "--chart-version", "4.6.0")
	if err != nil || !strings.Contains(out, "namespace: argocd\n") {
		t.Fatalf("flags %v %s", err, out)
	}
	if _, _, err := runRoot(t, e.d, nil, "cluster", "install", "--from", "flux", "--registry", e.repo, "--chart-version", "4.6.0"); err == nil || !strings.Contains(err.Error(), "no Flux kubeconfig ref") {
		t.Fatalf("flux without detection: %v", err)
	}
}

// TestClusterInstallRefusals: no hub, an unreadable detection or candidates,
// a refused registry and bad flags all fail before anything is printed.
func TestClusterInstallRefusals(t *testing.T) {
	e, det := installHub(t, "4.6.0")
	dir := filepath.Dir(det)
	run := func(d deps, args ...string) (string, error) {
		out, _, err := runRoot(t, d, nil, append([]string{"cluster", "install"}, args...)...)
		return out, err
	}
	stale, _ := json.Marshal(detect.Result{SchemaVersion: detect.SchemaVersion, AsOf: time.Now().Add(-48 * time.Hour), Cluster: clusterHubUID})
	staleDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(staleDir, detect.ConfigMapKey), stale, 0o600)
	badRepo, downRepo := e.d, e.d
	badRepo.newRecordRepo = func(string, ...string) (*remote.Repository, error) { return nil, errors.New("no keychain") }
	downRepo.newRecordRepo = func(string, ...string) (*remote.Repository, error) {
		return remote.NewRepository("127.0.0.1:1/acme/pacto-targets")
	}
	for name, tc := range map[string]struct {
		d    deps
		args []string
		want string
	}{
		"no registry": {e.d, []string{"--from", "argocd"}, "name the registry"},
		"no from":     {e.d, []string{"--registry", e.repo}, `"from" not set`},
		"bad engine":  {e.d, []string{"--from", "helm", "--registry", e.repo, "--chart-version", "1.0.0"}, "argocd or flux"},
		"stale":       {e.d, []string{"--from", "argocd", "--registry", e.repo, "--detected", filepath.Join(staleDir, detect.ConfigMapKey)}, "names no chart version"},
		"push digest": {e.d, []string{"--from", "flux", "--detected", det, "--push", "--digest", "sha256:x"}, "none of the others can be"},
		"repo":        {badRepo, []string{"--from", "argocd", "--detected", det, "--push"}, "no keychain"},
		"push fails":  {downRepo, []string{"--from", "argocd", "--detected", det, "--push"}, "pushing the member install"},
	} {
		if out, err := run(tc.d, tc.args...); err == nil || !strings.Contains(err.Error(), tc.want) || out != "" {
			t.Errorf("%s: %v %q", name, err, out)
		}
	}
	_ = os.WriteFile(filepath.Join(dir, detect.ClustersConfigMapKey), []byte("{"), 0o600)
	if out, err := run(e.d, "--from", "argocd", "--detected", det); err == nil || !strings.Contains(err.Error(), "candidate clusters were not read") || out != "" {
		t.Errorf("no candidates: %v %q", err, out)
	}
}
