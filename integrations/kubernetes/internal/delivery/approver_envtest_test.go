/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// sarEnv is a real apiserver that authorizes with RBAC and nobody bound to
// cluster-admin: in payments dana may update every Deployment, frank only the
// one named checkout and erin nothing.
func sarEnv(t *testing.T) client.Client {
	t.Helper()
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS is not set: run make -C integrations/kubernetes ci-test")
	}
	te := &envtest.Environment{CRDDirectoryPaths: []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true}
	cfg, err := te.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = te.Stop() })
	c, err := client.New(cfg, client.Options{Scheme: newScheme()})
	if err != nil {
		t.Fatal(err)
	}
	must(t, grantUpdates(context.Background(), c))
	// The RBAC authorizer reads through an informer: wait until both grants
	// answer, so no review races the bindings just created.
	r := &OperationReconciler{Client: c, Now: time.Now}
	granted := func(user string) bool {
		a, err := r.access(context.Background(), delivery.Identity{User: user, Groups: []string{"system:authenticated"}}, sarStage("checkout"))
		return err == nil && a.Allowed
	}
	for deadline := time.Now().Add(30 * time.Second); !granted("dana@example.com") || !granted("frank@example.com"); time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the RBAC grants never reached the authorizer")
		}
	}
	return c
}

func grantUpdates(ctx context.Context, c client.Client) error {
	role := func(name string, names ...string) *rbacv1.Role {
		return &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Namespace: "payments", Name: name},
			Rules: []rbacv1.PolicyRule{{APIGroups: []string{"apps"}, Resources: []string{"deployments"}, Verbs: []string{"update"}, ResourceNames: names}}}
	}
	bind := func(role, user string) *rbacv1.RoleBinding {
		return &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Namespace: "payments", Name: role + "-" + strings.Split(user, "@")[0]},
			RoleRef:  rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: role},
			Subjects: []rbacv1.Subject{{APIGroup: rbacv1.GroupName, Kind: rbacv1.UserKind, Name: user}}}
	}
	for _, o := range []client.Object{
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "payments"}},
		role("update-deployments"), bind("update-deployments", "dana@example.com"),
		role("update-checkout", "checkout"), bind("update-checkout", "frank@example.com"),
	} {
		if err := c.Create(ctx, o); err != nil {
			return err
		}
	}
	return nil
}

// sarStage is a Stage in payments whose workload is the Deployment named name.
func sarStage(name string) *dv1.Stage {
	return &dv1.Stage{ObjectMeta: metav1.ObjectMeta{Namespace: "payments", Name: name + "-prod"},
		Spec: delivery.StageSpec{Service: delivery.ServiceRef{Name: name}, Environment: "prod"}}
}

// sarAccess asks the promoter's review on c for user on the Stage of name.
func sarAccess(t *testing.T, c client.Client, user, name string) delivery.WorkloadAccess {
	t.Helper()
	at := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	r := &OperationReconciler{Client: c, Now: func() time.Time { return at }}
	a, err := r.access(context.Background(), delivery.Identity{User: user, Groups: []string{"system:authenticated"}}, sarStage(name))
	if a.Unverified != "" || err != nil {
		t.Fatalf("%s on %s: the review was not answered: %s", user, name, a.Unverified)
	}
	if a.Source != delivery.AccessBySubjectReview || a.At != ts(at) {
		t.Fatalf("%s on %s: provenance %q at %q", user, name, a.Source, a.At)
	}
	return a
}

func TestPromoterSARAllowed(t *testing.T) {
	a := sarAccess(t, sarEnv(t), "dana@example.com", "checkout")
	if !a.Allowed || a.Phrase() != "update deployments/checkout in payments" {
		t.Fatalf("dana may update deployments in payments: %+v", a)
	}
}

func TestPromoterSARDenied(t *testing.T) {
	if a := sarAccess(t, sarEnv(t), "erin@example.com", "checkout"); a.Allowed {
		t.Fatalf("erin holds no role in payments: %+v", a)
	}
}

func TestPromoterSARResourceNames(t *testing.T) {
	c := sarEnv(t)
	if a := sarAccess(t, c, "frank@example.com", "checkout"); !a.Allowed {
		t.Fatalf("frank may update deployments/checkout: %+v", a)
	}
	if a := sarAccess(t, c, "frank@example.com", "ledger"); a.Allowed {
		t.Fatalf("frank's role names checkout only: %+v", a)
	}
}
