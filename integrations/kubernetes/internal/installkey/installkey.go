/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package installkey holds the installation's Ed25519 key: the operator
// creates it once, at runtime, in a Secret in its own namespace, and signs the
// cluster's target snapshot with it. A chart-generated key would change on
// every GitOps render (lookup returns nothing there) and break every reader's
// trust entry, so the chart never renders it.
package installkey

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// KeyName is the Secret key that holds the PKCS#8 PEM private key.
const KeyName = "key.pem"

// PublicKeyName is the Secret key that holds PublicKeyBase64 of the key, so
// the chart's NOTES print the trust entry from the Secret only the operator
// writes, never from the detection ConfigMap a ConfigMap writer can forge.
const PublicKeyName = "public.b64"

// SecretName is the installation key Secret of the operator Deployment
// fullname.
func SecretName(fullname string) string { return fullname + "-installation-key" }

// Ensure returns the installation key in Secret namespace/name, creating the
// Secret with a fresh key when it is absent. Two replicas starting together
// both read the one that won the create. A Secret whose key is missing or
// malformed is refused and never overwritten: the Secret is never updated or
// deleted, so rotation is a person deleting it and restarting the operator.
func Ensure(ctx context.Context, r client.Reader, w client.Client, namespace, name string) (ed25519.PrivateKey, error) {
	if namespace == "" || name == "" || name == SecretName("") {
		return nil, errors.New("the installation key needs OPERATOR_DEPLOYMENT_NAME and the release namespace")
	}
	key := client.ObjectKey{Namespace: namespace, Name: name}
	k, err := read(ctx, r, key)
	if !apierrors.IsNotFound(err) {
		return k, err
	}
	_, fresh, _ := ed25519.GenerateKey(rand.Reader) // crypto/rand never fails
	der, _ := x509.MarshalPKCS8PrivateKey(fresh)    // an Ed25519 key always marshals
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name, Labels: map[string]string{
			"app.kubernetes.io/part-of":    "pacto",
			"app.kubernetes.io/component":  "installation-key",
			"app.kubernetes.io/managed-by": "pacto-operator",
		}},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{KeyName: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), PublicKeyName: []byte(PublicKeyBase64(fresh))},
	}
	switch err := w.Create(ctx, s); {
	case err == nil:
		return fresh, nil
	case apierrors.IsAlreadyExists(err):
		return read(ctx, r, key)
	default:
		return nil, fmt.Errorf("create the installation key Secret %s: %w", key, err)
	}
}

// read loads and parses the key in Secret key. A missing Secret returns the
// API's NotFound unwrapped, so Ensure can create it.
func read(ctx context.Context, r client.Reader, key client.ObjectKey) (ed25519.PrivateKey, error) {
	s := &corev1.Secret{}
	if err := r.Get(ctx, key, s); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, err
		}
		return nil, fmt.Errorf("read the installation key Secret %s: %w", key, err)
	}
	block, _ := pem.Decode(s.Data[KeyName])
	if block == nil {
		return nil, fmt.Errorf("the installation key Secret %s has no PEM %s: delete it and restart the operator to create a new key", key, KeyName)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	k, ok := parsed.(ed25519.PrivateKey)
	if err != nil || !ok {
		return nil, fmt.Errorf("the installation key Secret %s holds no PKCS#8 Ed25519 key: delete it and restart the operator to create a new key", key)
	}
	return k, nil
}

// PublicKeyBase64 is the base64 of k's public key in DER (PKIX) form: the
// snapshot key the installation advertises, on one line.
func PublicKeyBase64(k ed25519.PrivateKey) string {
	der, _ := x509.MarshalPKIXPublicKey(k.Public()) // an Ed25519 key always marshals
	return base64.StdEncoding.EncodeToString(der)
}
