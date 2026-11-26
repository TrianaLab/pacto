package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/trianalab/pacto/v4/internal/k8sclient"
	"github.com/trianalab/pacto/v4/internal/runtimewatch/kubesink"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

// kubeObj is an object of kind in ns, with fields merged into it.
func kubeObj(apiVersion, kind, ns, name string, fields map[string]any) *unstructured.Unstructured {
	o := map[string]any{"apiVersion": apiVersion, "kind": kind, "metadata": map[string]any{"name": name, "namespace": ns}}
	for k, v := range fields {
		if k == "metadata" {
			for mk, mv := range v.(map[string]any) {
				o["metadata"].(map[string]any)[mk] = mv
			}
			continue
		}
		o[k] = v
	}
	return &unstructured.Unstructured{Object: o}
}

func kubeDeployment(ns, name, team string) *unstructured.Unstructured {
	meta := map[string]any{}
	if team != "" {
		meta["labels"] = map[string]any{"team": team}
	}
	return kubeObj("apps/v1", "Deployment", ns, name, map[string]any{"metadata": meta,
		"spec": map[string]any{"selector": map[string]any{"matchLabels": map[string]any{"app": name}}, "template": map[string]any{
			"metadata": map[string]any{"labels": map[string]any{"app": name}},
			"spec": map[string]any{"containers": []any{map[string]any{"name": name, "image": "reg.io/acme/" + name + ":1.0.0",
				"ports": []any{map[string]any{"containerPort": int64(8080)}},
				"env":   []any{map[string]any{"name": "DB_PASSWORD", "value": "hunter2"}}}}}}}})
}

func kubeService(ns, name, ip string) *unstructured.Unstructured {
	return kubeObj("v1", "Service", ns, name, map[string]any{"spec": map[string]any{
		"clusterIP": ip, "clusterIPs": []any{ip}, "selector": map[string]any{"app": name},
		"ports": []any{map[string]any{"port": int64(80), "targetPort": int64(8080)}}}})
}

func kubePacto(ns, svc, version string) *unstructured.Unstructured {
	ref := "oci://reg.io/acme/contracts/" + svc
	return kubeObj("pacto.trianalab.io/v1", "Pacto", ns, svc, map[string]any{
		"metadata": map[string]any{"generation": int64(1)},
		"spec":     map[string]any{"contractRef": map[string]any{"oci": ref + ":" + version}, "target": map[string]any{"serviceName": svc}},
		"status": map[string]any{"observedGeneration": int64(1),
			"contract": map[string]any{"serviceName": svc, "version": version, "resolvedRef": ref + "@sha256:aa"}}})
}

// billing is a namespace where invoices (team billing) calls ledger, which
// a Pacto describes at 2.3.1, and audit, described at 1.4.0. The kube-system
// namespace has a uid.
func billing(now time.Time) []runtime.Object {
	call := func(ip string) map[string]any {
		return map[string]any{"address": ip, "port": int64(80), "successes": int64(2), "lastSuccessAt": now.UTC().Format(time.RFC3339)}
	}
	rs := kubeObj("apps/v1", "ReplicaSet", "billing", "invoices-7f", map[string]any{"metadata": map[string]any{
		"ownerReferences": []any{map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "invoices", "uid": "x", "controller": true}}}})
	pod := kubeObj("v1", "Pod", "billing", "invoices-7f-a", map[string]any{"metadata": map[string]any{"uid": "u-invoices", "labels": map[string]any{"app": "invoices"},
		"ownerReferences": []any{map[string]any{"apiVersion": "apps/v1", "kind": "ReplicaSet", "name": "invoices-7f", "uid": "x", "controller": true}}}})
	return []runtime.Object{
		kubeDeployment("billing", "invoices", "billing"), kubeService("billing", "invoices", "10.0.0.30"), rs, pod,
		kubeService("billing", "ledger", "10.0.0.60"), kubePacto("billing", "ledger", "2.3.1"),
		kubeService("billing", "audit", "10.0.0.70"), kubePacto("billing", "audit", "1.4.0"),
		kubeObj("v1", "Namespace", "", "kube-system", map[string]any{"metadata": map[string]any{"uid": "3f1c-uid"}}),
		kubeObj("pacto.trianalab.io/v1", "NodeRuntimeObservation", "pacto-system", "node-1", map[string]any{"status": map[string]any{
			"nodeName": "node-1", "pods": []any{map[string]any{"uid": "u-invoices", "connections": []any{call("10.0.0.60"), call("10.0.0.70")}}}}}),
	}
}

// declareLists are the list kinds of the resources a declaration reads.
func declareLists() map[schema.GroupVersionResource]string {
	lists := map[schema.GroupVersionResource]string{k8sclient.PactosGVR: "PactoList", kubesink.GVR: "NodeRuntimeObservationList", gvrConfigMaps: "ConfigMapList"}
	for kind, r := range k8sclient.WorkloadGVRs {
		lists[r] = kind + "List"
	}
	for _, r := range []string{"services", "pods", "replicasets", "jobs", "endpointslices"} {
		lists[schema.GroupVersionResource{Group: map[string]string{"replicasets": "apps", "jobs": "batch", "endpointslices": "discovery.k8s.io"}[r], Version: "v1", Resource: r}] = r + "List"
	}
	return lists
}

// declareEnv is a cluster, a registry and a private Sigstore to plan
// declarations against.
type declareEnv struct {
	host string
	reg  publish.Registry
	ss   *testutil.Sigstore
	root string
	svc  *Service
	dyn  *dynamicfake.FakeDynamicClient
}

// identities is the installation's delivery identity ConfigMap: people of
// acme.com may sign, and acme's GitHub Actions on main publish.
func identities() *unstructured.Unstructured {
	return policyConfigMap(map[string]any{
		delivery.PolicyKey:     `{"signers":[{"issuer":"` + personIssuer + `","subjects":".*@acme\\.com"}]}`,
		delivery.PublishersKey: `{"registry":"ghcr.io/acme"}`,
	})
}

func newDeclareEnv(t *testing.T, objs ...runtime.Object) *declareEnv {
	t.Helper()
	host, reg := publishRegistry(t)
	ss := testutil.New(t)
	rootPath := filepath.Join(t.TempDir(), "trusted_root.json")
	if err := os.WriteFile(rootPath, ss.RootJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	if objs == nil {
		objs = append(billing(time.Now()), identities())
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), declareLists(), objs...)
	return &declareEnv{host: host, reg: reg, ss: ss, root: rootPath, dyn: dyn,
		svc: &Service{sys: sysDeps{publishRegistry: func() publish.Registry { return reg },
			newK8s: func(string) (k8sclient.K8sClient, error) { return nil, errors.New("no kubeconfig") }}}}
}

func (e *declareEnv) req(accept ...string) DeclareRequest {
	return DeclareRequest{Service: "invoices", Namespace: "billing", Registry: e.host + "/acme", Accept: accept, Dynamic: e.dyn,
		Signing: declareSigning{TrustedRoot: e.root, FulcioURL: e.ss.Fulcio, TimestampURL: e.ss.TSA}}
}

func (e *declareEnv) plan(t *testing.T, req DeclareRequest) (*DeclarePlan, *declaration) {
	t.Helper()
	p, d, err := e.svc.PlanDeclare(t.Context(), req)
	if err != nil {
		t.Fatalf("%v\n%+v", err, p)
	}
	return p, d
}

// signer signs keyless as the identity tok names.
func (e *declareEnv) signer(t *testing.T, tok string) signing.Signer {
	t.Helper()
	s, err := signing.Keyless(e.ss.Fulcio, e.ss.TSA, "", func(context.Context) (string, error) { return tok, nil }, e.ss.Root)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// acmeCIToken is acme's release workflow, which the installation's derived
// publisher rule accepts.
func acmeCIToken() string {
	return testutil.CIToken("https://github.com/acme/invoices/.github/workflows/release.yml@refs/heads/main",
		"https://token.actions.githubusercontent.com", "https://github.com/acme/invoices", "refs/heads/main")
}

// publishBundle pushes files as a bundle tagged tag in repo and signs it
// with s when s is not nil; it returns the digest.
func (e *declareEnv) publishBundle(t *testing.T, repo, tag string, files map[string]string, s *signing.Signer) string {
	t.Helper()
	fsys := fstest.MapFS{}
	for p, c := range files {
		fsys[p] = &fstest.MapFile{Data: []byte(c), Mode: 0o644}
	}
	c, err := contract.Parse(strings.NewReader(files["pacto.yaml"]))
	if err != nil {
		t.Fatal(err)
	}
	b := &contract.Bundle{Contract: c, RawYAML: []byte(files["pacto.yaml"]), FS: fsys}
	img, err := oci.BundleImage(b)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := oci.BundleDigest(b)
	if err != nil {
		t.Fatal(err)
	}
	h, _ := v1.NewHash(digest)
	ctx := t.Context()
	d, err := e.reg.Write(ctx, repo, digested{img, h})
	if err == nil {
		err = e.reg.Tag(ctx, repo, d, tag)
	}
	if err == nil && s != nil {
		err = publish.SignDigest(ctx, e.reg, *s, repo, d)
	}
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// pushImage pushes a random image tagged tag in repo and returns its digest.
func (e *declareEnv) pushImage(t *testing.T, repo, tag string) string {
	t.Helper()
	img, _ := random.Image(16, 1)
	d, err := e.reg.Write(t.Context(), repo, img)
	if err == nil {
		err = e.reg.Tag(t.Context(), repo, d, tag)
	}
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// invoicesBaseline is a published invoices contract at version, its bundle
// in the declaration's repository and a manifests deploy artifact.
func (e *declareEnv) invoicesBaseline(version, extra string) map[string]string {
	return map[string]string{"pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: invoices\n  version: " + version + "\n  owner:\n    team: billing\n" +
		"workload: service\n" + extra + "publish:\n  bundle: oci://" + e.host + "/acme/invoices\n  deploy: manifests\n" +
		"  artifacts:\n    - {name: manifests, type: manifests, ref: \"oci://" + e.host + "/acme/deploy/invoices\"}\n"}
}
