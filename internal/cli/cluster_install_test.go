package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/app"

	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

// installHub is a hub whose pending member eu-prod publishes, and whose
// detection advertises its registry and chart version beside the candidates:
// an Argo CD Secret stating eu-prod's UID and a Flux kubeconfig ref.
func installHub(t *testing.T, chartVersion string) (*clusterEnv, string) {
	t.Helper()
	return installHubWith(t, chartVersion, detect.Clusters{AsOf: time.Now(), Sources: []detect.InventorySource{{Source: detect.SourceArgoCD, Completeness: detect.Complete},
		{Source: detect.SourceFlux, Completeness: detect.Complete}},
		Candidates: []detect.Candidate{{Source: detect.SourceArgoCD, Kind: "Secret", Namespace: "gitops", Name: "eu", UID: euProd, UIDSource: "annotation"},
			{Source: detect.SourceFlux, Kind: "Secret", Namespace: "flux-system", Name: "us", KubeConfig: &detect.KubeConfigRef{Kind: "Secret", Namespace: "flux-system", Name: "us"}}}})
}

// installHubWith is installHub with the candidates of clusters, or no
// clusters.json at all when it has no asOf.
func installHubWith(t *testing.T, chartVersion string, clusters detect.Clusters) (*clusterEnv, string) {
	t.Helper()
	e := newClusterEnv(t, capabilities.ReferrersNative)
	m := newJoinMember(euProd, "eu-prod")
	e.publish(t, m, m.key)
	dir := t.TempDir()
	b, _ := json.Marshal(detect.Result{SchemaVersion: detect.SchemaVersion, AsOf: time.Now(), Cluster: clusterHubUID,
		Installation: &detect.Installation{Registry: "r", Targets: e.repo, ChartVersion: chartVersion}})
	files := map[string][]byte{detect.ConfigMapKey: b}
	if !clusters.AsOf.IsZero() {
		files[detect.ClustersConfigMapKey], _ = detect.EncodeClusters(clusters)
	}
	for name, data := range files {
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
	if !strings.Contains(errOut, "warning: Argo CD cluster Secret gitops/eu states the UID a pending or removed cluster reported about itself") {
		t.Fatalf("stderr %q", errOut)
	}
	// Flux never carries the Argo CD candidates' warnings.
	out, errOut, err = runRoot(t, e.d, nil, "cluster", "install", "--from", "flux", "--detected", det)
	if err != nil || !strings.Contains(out, "kind: HelmRelease\n") || !strings.Contains(errOut, "pass --push") || strings.Contains(out, "bootstrap") ||
		strings.Contains(errOut, "warning") {
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
	// the warnings say no candidate was checked, and Flux has no refs to
	// install through.
	out, errOut, err := runRoot(t, e.d, nil, "cluster", "install", "--from", "argocd", "--registry", e.repo, "--chart-version", "4.6.0")
	if err != nil || !strings.Contains(out, "namespace: argocd\n") || !strings.Contains(errOut, "could not all be ruled out: the hub's candidate clusters were not read (pass --detected)") ||
		!strings.Contains(errOut, "assume namespace argocd") {
		t.Fatalf("flags %v %s %q", err, out, errOut)
	}
	if _, _, err := runRoot(t, e.d, nil, "cluster", "install", "--from", "flux", "--registry", e.repo, "--chart-version", "4.6.0"); err == nil || !strings.Contains(err.Error(), "pass --detected") {
		t.Fatalf("flux without detection: %v", err)
	}
}

// TestClusterInstallSaysWhatItCouldNotRead: an inventory that is off, a Flux
// source that is unknown or cut and a stale read never read as "nothing to
// warn about" or "no ref exists".
func TestClusterInstallSaysWhatItCouldNotRead(t *testing.T) {
	run := func(c detect.Clusters, from string) (memberInstall, error) {
		e, det := installHubWith(t, "4.6.0", c)
		out, _, err := runRoot(t, e.d, nil, "cluster", "install", "--from", from, "--detected", det, "--output-format", "json")
		var res memberInstall
		if err == nil {
			err = json.Unmarshal([]byte(out), &res)
		}
		return res, err
	}
	warned := func(res memberInstall, want string) bool {
		return slices.ContainsFunc(res.Warnings, func(w string) bool { return strings.Contains(w, want) })
	}
	if res, err := run(detect.Clusters{}, "argocd"); err != nil || !warned(res, "runs no cluster inventory") {
		t.Errorf("inventory off, argocd: %v %+v", err, res)
	}
	if _, err := run(detect.Clusters{}, "flux"); err == nil || !strings.Contains(err.Error(), "runs no cluster inventory: set clusterInventory.enabled") {
		t.Errorf("inventory off, flux: %v", err)
	}
	unknown := detect.Clusters{AsOf: time.Now(), Sources: []detect.InventorySource{{Source: detect.SourceFlux, Completeness: detect.Unknown, Reason: "the operator may not list Flux Kustomizations and HelmReleases"}}}
	if _, err := run(unknown, "flux"); err == nil || !strings.Contains(err.Error(), "refs were not read: the operator may not list Flux") {
		t.Errorf("flux unknown: %v", err)
	}
	ref := detect.KubeConfigRef{Kind: "Secret", Namespace: "flux-system", Name: "us"}
	cut := detect.Clusters{AsOf: time.Now(), Sources: []detect.InventorySource{{Source: detect.SourceFlux, Completeness: detect.Partial, Reason: "listed the first 1 of 30"},
		{Source: detect.SourceArgoCD, Completeness: detect.Complete, ControlPlane: []string{"openshift-gitops"}}},
		Candidates: []detect.Candidate{{Source: detect.SourceFlux, Kind: "Secret", Namespace: "flux-system", Name: "us", KubeConfig: &ref}}}
	if res, err := run(cut, "flux"); err != nil || !warned(res, "the candidate clusters from flux are partial: listed the first 1 of 30") {
		t.Errorf("flux cut: %v %+v", err, res)
	}
	if res, err := run(cut, "argocd"); err != nil || !strings.Contains(res.Files[0].Content, "namespace: openshift-gitops\n") {
		t.Errorf("control plane: %v %+v", err, res)
	}
	stale := cut
	stale.AsOf, stale.Candidates = time.Now().Add(-48*time.Hour), nil
	stale.Sources = []detect.InventorySource{{Source: detect.SourceFlux, Completeness: detect.Complete}}
	if _, err := run(stale, "flux"); err == nil || !strings.Contains(err.Error(), "ago: check the operator's detection") {
		t.Errorf("flux stale: %v", err)
	}
}

// TestClusterInstallInsecurePush: --insecure-registry reaches the registry
// over plain HTTP for the push too, not only in the generated values.
func TestClusterInstallInsecurePush(t *testing.T) {
	e, det := installHub(t, "4.6.0")
	var hosts []string
	d := e.d
	d.newRecordRepo = func(path string, h ...string) (*remote.Repository, error) {
		hosts = h
		return nil, errors.New("stop")
	}
	_, _, _ = runRoot(t, d, nil, "cluster", "install", "--from", "argocd", "--detected", det, "--push", "--insecure-registry")
	host, _, _ := strings.Cut(e.repo, "/")
	if !slices.Contains(hosts, host) {
		t.Fatalf("push hosts %q, want %s", hosts, host)
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
		"argo digest": {e.d, []string{"--from", "argocd", "--detected", det, "--digest", "sha256:" + strings.Repeat("0", 64)}, "for Argo CD point an Application"},
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

// TestDashboardInstall: the dashboard hands out what the CLI prints for the
// hub's detection, with no hub URL and the warnings as leading comments, and
// every read that fails is an error rather than a partial install.
func TestDashboardInstall(t *testing.T) {
	expect(t, dashboardInstall(defaultDeps(), &rest.Config{}, "x", nil, nil) == nil, "no joins, no install")
	e, det := installHub(t, "4.6.0")
	d := e.d
	d.dynamicFor = func(*rest.Config) (dynamic.Interface, error) { return e.hub(), nil }
	list := listOf(delivery.ClusterJoinList{Candidates: []delivery.ClusterCandidate{
		{Source: detect.SourceArgoCD, Kind: "Secret", Namespace: "gitops", Name: "eu", Match: delivery.CandidateMatched, Member: &delivery.ClusterRef{Name: "eu"}}}})
	install := func(d deps, det, from string) (string, error) {
		b, err := dashboardInstall(d, &rest.Config{}, det, &app.JoinOptions{}, list)(t.Context(), from)
		return string(b), err
	}
	host := strings.TrimSuffix(e.repo, "/"+delivery.TargetSnapshotRepository)
	out, err := install(d, det, "argocd")
	expect(t, err == nil && strings.HasPrefix(out, "# warning: Argo CD cluster Secret gitops/eu is the approved member eu,") && strings.Contains(out, "kind: ApplicationSet\n") &&
		strings.Contains(out, "registry: "+host+"\n") && strings.Contains(out, "targetRevision: 4.6.0\n") && !strings.Contains(out, "hub:"), "%v %s", err, out)
	out, err = install(d, det, "flux")
	expect(t, err == nil && strings.Contains(out, "kind: HelmRelease\n") && !strings.Contains(out, "bootstrap"), "flux %v %s", err, out)

	dashboardInstallFailures(t, d, det)
}

// TestDashboardInstallPartialList: a partial member list or candidate
// limitation says the warnings may miss some.
func TestDashboardInstallPartialList(t *testing.T) {
	e, det := installHub(t, "4.6.0")
	d := e.d
	d.dynamicFor = func(*rest.Config) (dynamic.Interface, error) { return e.hub(), nil }
	partial := listOf(delivery.ClusterJoinList{Completeness: fleet.CompletenessPartial,
		Limitations: []fleet.Limitation{{Source: delivery.CandidatesID, Message: "the candidate clusters from argocd are unknown: no namespace is granted"}}})
	b, err := dashboardInstall(d, &rest.Config{}, det, &app.JoinOptions{}, partial)(t.Context(), "argocd")
	expect(t, err == nil && strings.Contains(string(b), "# warning: clusters that already run Pacto could not all be ruled out: the member list is partial") &&
		strings.Contains(string(b), "ruled out: the candidate clusters from argocd are unknown"), "partial %v %s", err, b)
}

// dashboardInstallFailures: every read that fails is an error, a hub read
// one the dashboard's 502.
func dashboardInstallFailures(t *testing.T, d deps, det string) {
	t.Helper()
	install := func(d deps, det, from string) (string, error) {
		b, err := dashboardInstall(d, &rest.Config{}, det, &app.JoinOptions{}, listOf(delivery.ClusterJoinList{}))(t.Context(), from)
		return string(b), err
	}
	down := d
	down.dynamicFor = func(*rest.Config) (dynamic.Interface, error) { return nil, errors.New("no client") }
	noHub := d
	noHub.dynamicFor = func(*rest.Config) (dynamic.Interface, error) {
		return dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()), nil
	}
	_, noVersion := installHub(t, "")
	_, broken := installHub(t, "4.6.0")
	_ = os.WriteFile(filepath.Join(filepath.Dir(broken), detect.ClustersConfigMapKey), []byte("{"), 0o600)
	for name, tc := range map[string]struct {
		d         deps
		det, want string
	}{
		"client":      {down, det, "no client"},
		"hub":         {noHub, det, "kube-system"},
		"no registry": {d, detectedFile(t, ""), "advertises no registry"},
		"candidates":  {d, broken, "candidate clusters were not read"},
		"version":     {d, noVersion, "no chart version"},
	} {
		out, err := install(tc.d, tc.det, "argocd")
		if err == nil || !strings.Contains(err.Error(), tc.want) || out != "" {
			t.Errorf("%s: %v %q", name, err, out)
		}
		// A failed read of the hub is the dashboard's 502; the rest are 422.
		if hub := name == "client" || name == "hub" || name == "candidates"; errors.As(err, new(dashboard.HubReadError)) != hub {
			t.Errorf("%s: hub read %v", name, err)
		}
	}
}
