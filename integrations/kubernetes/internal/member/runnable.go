/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package member

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/trianalab/pacto/v4/pkg/kubecluster"
	"github.com/trianalab/pacto/v4/pkg/memberlink"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// Link keeps the member's link to the hub open. It is a plain Runnable, which
// controller-runtime starts on the elected leader only, so one replica dials.
type Link struct{ m *memberlink.Member }

// Start runs the link until ctx ends.
func (l *Link) Start(ctx context.Context) error { return l.m.Run(ctx) }

// New returns the link to hubURL for the cluster ident, proven with its
// installation key and answering with rv. caFile, when set, adds the CAs the
// hub's certificate may chain to beside the system pool. Each missing piece is
// one sentence.
func New(hubURL, caFile string, ident kubecluster.Identity, key ed25519.PrivateKey, rv *Reviewer) (*Link, error) {
	if _, err := memberlink.ParseHubURL(hubURL); err != nil {
		return nil, fmt.Errorf("--hub-url: %w", err)
	}
	if ident.ID == "" {
		return nil, errors.New("--hub-url needs the cluster id (the kube-system namespace UID), which could not be read")
	}
	if key == nil {
		return nil, errors.New("--hub-url needs the installation key: set the registry value so this cluster can join the hub")
	}
	pool, err := rootCAs(caFile)
	if err != nil {
		return nil, err
	}
	return &Link{m: memberlink.NewMember(memberlink.MemberOptions{
		URL: hubURL, RootCAs: pool, Cluster: ident.ID, KeyID: signing.Hint(key.Public()), Key: key,
		Answer: rv.Answer, Logger: slog.New(logr.ToSlogHandler(ctrl.Log.WithName("member"))),
	})}, nil
}

// systemPool is the system CA pool the hub CA file is added to.
var systemPool = x509.SystemCertPool

// rootCAs is the system pool plus the PEM CAs in caFile; nil (the system pool)
// without one.
func rootCAs(caFile string) (*x509.CertPool, error) {
	if caFile == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("--hub-ca-file: %w", err)
	}
	pool, err := systemPool()
	if err != nil {
		return nil, fmt.Errorf("--hub-ca-file: the system CA pool: %w", err)
	}
	if !pool.AppendCertsFromPEM(raw) {
		return nil, errors.New("--hub-ca-file holds no PEM certificate")
	}
	return pool, nil
}
