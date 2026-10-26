/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package admission runs the chart's delivery admission policies on a real
// apiserver (envtest), so every rule is proved by a request it refuses and
// one it lets through, not by the text of its CEL.
package admission

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

const (
	ns         = "prod"
	controller = "system:serviceaccount:pacto-delivery:d-pacto-operator-delivery"
	recorder   = "system:serviceaccount:pacto-delivery:d-pacto-operator-delivery-recorder"
	nsCtrl     = "system:serviceaccount:kube-system:namespace-controller"
	breakGlass = "pacto:break-glass"
	agents     = "pacto:agents"
	applyRef   = "delivery.pacto.trianalab.io/apply-ref"
	digestA    = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digestB    = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

var (
	cfg   *rest.Config
	admin client.Client
)

// Flux and Argo CD are not installed in envtest: these stand-ins carry the
// group, version and plural the policies match, and accept any fields.
const thirdPartyCRDs = `
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: ocirepositories.source.toolkit.fluxcd.io}
spec:
  group: source.toolkit.fluxcd.io
  names: {kind: OCIRepository, plural: ocirepositories, singular: ocirepository, listKind: OCIRepositoryList}
  scope: Namespaced
  versions:
  - name: v1
    served: true
    storage: true
    subresources: {status: {}}
    schema: {openAPIV3Schema: {type: object, x-kubernetes-preserve-unknown-fields: true}}
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: kustomizations.kustomize.toolkit.fluxcd.io}
spec:
  group: kustomize.toolkit.fluxcd.io
  names: {kind: Kustomization, plural: kustomizations, singular: kustomization, listKind: KustomizationList}
  scope: Namespaced
  versions:
  - name: v1
    served: true
    storage: true
    subresources: {status: {}}
    schema: {openAPIV3Schema: {type: object, x-kubernetes-preserve-unknown-fields: true}}
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: applications.argoproj.io}
spec:
  group: argoproj.io
  names: {kind: Application, plural: applications, singular: application, listKind: ApplicationList}
  scope: Namespaced
  versions:
  - name: v1alpha1
    served: true
    storage: true
    schema: {openAPIV3Schema: {type: object, x-kubernetes-preserve-unknown-fields: true}}
`

// everyoneMayWrite lets every authenticated user do anything: RBAC is not
// what these tests are about, admission is.
const everyoneMayWrite = `
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata: {name: everyone-admin}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: cluster-admin}
subjects:
- {apiGroup: rbac.authorization.k8s.io, kind: Group, name: "system:authenticated"}
---
apiVersion: v1
kind: Namespace
metadata: {name: prod}
`

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	dir, err := os.MkdirTemp("", "pacto-admission-crds")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := os.WriteFile(filepath.Join(dir, "thirdparty.yaml"), []byte(thirdPartyCRDs), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases"), dir},
		ErrorIfCRDPathMissing: true,
	}
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		env.BinaryAssetsDirectory = newestAssets()
	}
	if cfg, err = env.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "envtest:", err)
		return 1
	}
	defer func() { _ = env.Stop() }()
	if admin, err = client.New(cfg, client.Options{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := setup(); err != nil {
		fmt.Fprintln(os.Stderr, "setup:", err)
		return 1
	}
	return m.Run()
}

// newestAssets is the newest envtest control plane under bin/k8s, for a run
// outside make; "" lets envtest look where it always does.
func newestAssets() string {
	dirs, _ := filepath.Glob(filepath.Join("..", "..", "bin", "k8s", "*"))
	sort.Strings(dirs)
	if len(dirs) == 0 {
		return ""
	}
	return dirs[len(dirs)-1]
}

// setup grants every user write access, renders the chart's admission
// policies (Argo CD and the recorder on) and waits until they deny.
func setup() error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := apply(ctx, []byte(everyoneMayWrite)); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "helm", "template", "d", filepath.Join("..", "..", "charts", "pacto-operator"),
		"-n", "pacto-system", "-s", "templates/delivery/admission.yaml",
		"--set", "delivery.enabled=true", "--set", "delivery.argocd.enabled=true", "--set", "delivery.identities.allowEmptyHumanGroups=true",
		"--set", "delivery.recorder.enabled=true", "--set", "delivery.recorder.streamsRepository=reg.example/streams",
		"--set", "delivery.recorder.signing.kms=awskms://k",
		"--api-versions", "admissionregistration.k8s.io/v1/ValidatingAdmissionPolicy")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("helm template: %w: %s", err, stderr.String())
	}
	if err := apply(ctx, out); err != nil {
		return err
	}
	// The policies reach the admission plugin through an informer: wait for
	// one that denies, then give the rest the same moment.
	for {
		op := operation("probe-"+fmt.Sprint(time.Now().UnixNano()), "alice", nil)
		op.Object["spec"].(map[string]any)["actor"] = map[string]any{"user": "mallory"}
		err := as("alice").Create(ctx, op)
		if apierrors.IsForbidden(err) || apierrors.IsInvalid(err) {
			time.Sleep(2 * time.Second)
			return nil
		}
		if ctx.Err() != nil {
			return errors.New("the admission policies never started denying")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func apply(ctx context.Context, docs []byte) error {
	dec := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(docs), 4096)
	for {
		u := &unstructured.Unstructured{}
		if err := dec.Decode(&u.Object); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if len(u.Object) == 0 {
			continue
		}
		if err := admin.Create(ctx, u); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("%s %s: %w", u.GetKind(), u.GetName(), err)
		}
	}
}

// as is a client that acts as user in groups, through impersonation.
func as(user string, groups ...string) client.Client {
	return asExtra(user, nil, groups...)
}

func asExtra(user string, extra map[string][]string, groups ...string) client.Client {
	c := rest.CopyConfig(cfg)
	c.Impersonate = rest.ImpersonationConfig{UserName: user, Groups: groups, Extra: extra}
	cl, err := client.New(c, client.Options{})
	if err != nil {
		panic(err)
	}
	return cl
}

func obj(apiVersion, kind, name string, labels map[string]string, spec map[string]any) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{"apiVersion": apiVersion, "kind": kind, "spec": spec}}
	u.SetNamespace(ns)
	u.SetName(name)
	if labels != nil {
		u.SetLabels(labels)
	}
	return u
}

// operation is an Operation user may create: actor is user, with groups
// and the system:authenticated group the apiserver adds to every user.
func operation(name, user string, groups []string) *unstructured.Unstructured {
	g := []any{"system:authenticated"}
	for _, x := range groups {
		g = append(g, x)
	}
	actor := map[string]any{"user": user, "groups": g}
	u := obj("delivery.pacto.trianalab.io/v1alpha1", "Operation", name, nil, map[string]any{
		"type": "delivery.pacto.trianalab.io/promote", "stage": "payments", "facet": "pointer",
		"params": map[string]any{"from": digestA, "to": digestB}, "actor": actor,
	})
	u.SetFinalizers([]string{"delivery.pacto.trianalab.io/record"})
	return u
}

// mustCreate creates u as c or fails the test.
func mustCreate(t *testing.T, c client.Client, u *unstructured.Unstructured) *unstructured.Unstructured {
	t.Helper()
	if err := c.Create(context.Background(), u); err != nil {
		t.Fatalf("create %s %s: %v", u.GetKind(), u.GetName(), err)
	}
	return u
}

// denied fails the test unless err is an admission refusal naming want.
func denied(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("allowed, want a refusal naming %q", want)
	}
	if !apierrors.IsForbidden(err) && !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), want) {
		t.Fatalf("refused with %v, want a refusal naming %q", err, want)
	}
}

// allowed fails the test on any error.
func allowed(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("refused: %v", err)
	}
}

// setStatus writes status on u as c, through the status subresource.
func setStatus(c client.Client, u *unstructured.Unstructured, status map[string]any) error {
	u.Object["status"] = status
	return c.Status().Update(context.Background(), u)
}

func uniq(prefix string) string { return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano()) }
