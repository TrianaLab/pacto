/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package installkey

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

const (
	ns   = "pacto-system"
	name = "pacto-operator-installation-key"
)

var gr = schema.GroupResource{Resource: "secrets"}

func secretOf(t *testing.T, c client.Client) *corev1.Secret {
	t.Helper()
	s := &corev1.Secret{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: name}, s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestEnsureCreatesKeyOnce(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	k1, err := Ensure(context.Background(), c, c, ns, name)
	if err != nil || len(k1) != ed25519.PrivateKeySize {
		t.Fatalf("Ensure: %v", err)
	}
	s := secretOf(t, c)
	if s.Labels["app.kubernetes.io/component"] != "installation-key" || s.Labels["app.kubernetes.io/managed-by"] != "pacto-operator" ||
		s.Labels["app.kubernetes.io/part-of"] != "pacto" || !strings.Contains(string(s.Data[KeyName]), "BEGIN PRIVATE KEY") ||
		string(s.Data[PublicKeyName]) != PublicKeyBase64(k1) {
		t.Fatalf("secret %+v", s)
	}
	k2, err := Ensure(context.Background(), c, c, ns, name)
	if err != nil || !k1.Equal(k2) {
		t.Fatalf("second Ensure made another key: %v", err)
	}
}

func TestEnsureReadsExistingKey(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	k1, _ := Ensure(context.Background(), c, c, ns, name)
	creates := 0
	w := interceptor.NewClient(fake.NewClientBuilder().WithObjects(secretOf(t, c)).Build(), interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			creates++
			return c.Create(ctx, obj, opts...)
		}})
	k2, err := Ensure(context.Background(), w, w, ns, name)
	if err != nil || !k1.Equal(k2) || creates != 0 {
		t.Fatalf("Ensure: %v, %d creates", err, creates)
	}
}

// Two replicas start together: this one's create loses, and it reads the key
// the other one wrote.
func TestEnsureRaceAlreadyExists(t *testing.T) {
	winner := fake.NewClientBuilder().Build()
	want, _ := Ensure(context.Background(), winner, winner, ns, name)
	gets := 0
	c := interceptor.NewClient(fake.NewClientBuilder().Build(), interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			gets++
			if gets == 1 {
				return apierrors.NewNotFound(gr, key.Name)
			}
			secretOf(t, winner).DeepCopyInto(obj.(*corev1.Secret))
			return nil
		},
		Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error {
			return apierrors.NewAlreadyExists(gr, name)
		}})
	got, err := Ensure(context.Background(), c, c, ns, name)
	if err != nil || !want.Equal(got) {
		t.Fatalf("Ensure: %v", err)
	}
}

func TestEnsureRefusesMalformedKeyWithoutOverwriting(t *testing.T) {
	notEd, _ := x509.MarshalPKCS8PrivateKey(mustRSA(t))
	for label, data := range map[string][]byte{
		"empty":   nil,
		"not pem": []byte("garbage"),
		"not ed":  pemOf(notEd),
		"bad der": pemOf([]byte("garbage")),
	} {
		old := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}, Data: map[string][]byte{KeyName: data}}
		writes := 0
		c := interceptor.NewClient(fake.NewClientBuilder().WithObjects(old).Build(), interceptor.Funcs{
			Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error {
				writes++
				return nil
			},
			Update: func(context.Context, client.WithWatch, client.Object, ...client.UpdateOption) error {
				writes++
				return nil
			},
			Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
				writes++
				return nil
			},
		})
		_, err := Ensure(context.Background(), c, c, ns, name)
		if err == nil || !strings.Contains(err.Error(), "delete it and restart the operator") || writes != 0 {
			t.Errorf("%s: %v, %d writes", label, err, writes)
		}
	}
}

func TestEnsureForbiddenIsAnError(t *testing.T) {
	forbidden := apierrors.NewForbidden(gr, name, errors.New("rbac"))
	c := interceptor.NewClient(fake.NewClientBuilder().Build(), interceptor.Funcs{
		Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
			return forbidden
		}})
	if _, err := Ensure(context.Background(), c, c, ns, name); !apierrors.IsForbidden(err) || !strings.Contains(err.Error(), "read the installation key") {
		t.Fatalf("read: %v", err)
	}
	c = interceptor.NewClient(fake.NewClientBuilder().Build(), interceptor.Funcs{
		Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error { return forbidden }})
	if _, err := Ensure(context.Background(), c, c, ns, name); !apierrors.IsForbidden(err) || !strings.Contains(err.Error(), "create the installation key") {
		t.Fatalf("create: %v", err)
	}
}

func TestEnsureEmptyNameIsAnError(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	for _, tc := range [][2]string{{ns, ""}, {"", name}, {ns, SecretName("")}} {
		if _, err := Ensure(context.Background(), c, c, tc[0], tc[1]); err == nil || !strings.Contains(err.Error(), "OPERATOR_DEPLOYMENT_NAME") {
			t.Errorf("%q: %v", tc, err)
		}
	}
	if SecretName("pacto-operator") != name {
		t.Fatal(SecretName("pacto-operator"))
	}
}

func TestPublicKeyBase64RoundTrips(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	k, _ := Ensure(context.Background(), c, c, ns, name)
	der, err := base64.StdEncoding.DecodeString(PublicKeyBase64(k))
	if err != nil {
		t.Fatal(err)
	}
	pub, err := x509.ParsePKIXPublicKey(der)
	if err != nil || !k.Public().(ed25519.PublicKey).Equal(pub) {
		t.Fatalf("public key: %v", err)
	}
}

func pemOf(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func mustRSA(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}
