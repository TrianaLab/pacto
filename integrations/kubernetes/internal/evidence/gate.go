/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package evidence

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

// Gate decides, on every reconcile tick, whether the Evidence Server runs
// under profile idp (--evidence-server-auto): only while the registry serves
// native OCI Referrers, a subject or subject prefix is set and the trust
// Secret holds a public key. A check that cannot tell keeps the current
// state: unknown is never positive, and it never deletes a running server.
type Gate struct {
	// Reader must be uncached (mgr.GetAPIReader()): the trust Secret is read
	// with one Get by name, as the Secret RBAC is get only.
	Reader client.Reader
	// Namespace and TrustSecret name the trust Secret; TrustConfigKey, when
	// set, is a key of it that counts as trust.
	Namespace, TrustSecret, TrustConfigKey string
	// Registry is the installation registry (host/path); empty keeps it off.
	Registry string
	// Scoped is whether subjects or subject prefixes are set. The registry
	// alone never counts as a scope.
	Scoped bool
	// Probe reports what the registry holding the target snapshot supports.
	Probe func(ctx context.Context) (capabilities.Capabilities, error)
	// LastPush is the target snapshot publisher's last attempt, nil before
	// the first.
	LastPush func() *detect.PushResult
	// URL is the managed Evidence Server's internal URL.
	URL string

	state   atomic.Pointer[detect.Entry]
	running atomic.Bool
}

// Check decides the gate once: on, whether the decision is known, and the
// detected row that says why. A known decision is remembered; an unknown one
// leaves the remembered state as it was.
func (g *Gate) Check(ctx context.Context) (on, known bool, e detect.Entry) {
	on, known, e = g.check(ctx)
	g.state.Store(&e)
	if known {
		g.running.Store(on)
	}
	return on, known, e
}

func (g *Gate) check(ctx context.Context) (on, known bool, e detect.Entry) {
	host, _, _ := strings.Cut(g.Registry, "/")
	switch {
	case g.Registry == "":
		return false, true, off("no registry value", nil)
	case !g.Scoped:
		return false, true, off("set evidence.registry.subjectPrefixes", nil)
	}
	push := g.LastPush()
	switch {
	case push == nil:
		return unknown("no target snapshot pushed yet to probe " + host)
	case push.Error != "":
		return unknown("the last target snapshot push failed: " + push.Error)
	}
	caps, err := g.Probe(ctx)
	switch {
	case err != nil:
		return unknown("probing " + host + " failed: " + err.Error())
	case caps.Referrers != capabilities.ReferrersNative:
		return false, true, off(host+" does not serve OCI Referrers", nil)
	}
	secret := []detect.Object{{Kind: "Secret", Namespace: g.Namespace, Name: g.TrustSecret}}
	s := &corev1.Secret{}
	switch err := g.Reader.Get(ctx, client.ObjectKey{Namespace: g.Namespace, Name: g.TrustSecret}, s); {
	case apierrors.IsNotFound(err):
		return false, true, off("the trust Secret does not exist", secret)
	case err != nil:
		return unknown("reading the trust Secret failed: " + reason(err))
	}
	trust := trustHeld(s.Data, g.TrustConfigKey)
	if trust == "" {
		return false, true, off("the trust Secret holds no public key", secret)
	}
	return true, true, detect.EvidencePiece("on", host+" serves OCI Referrers; the trust Secret holds "+trust, secret, detect.Complete, "")
}

// trustHeld says what of data counts as trust: its *.pub keys, else the
// trust config key. Empty when neither is there. Only names are counted,
// never the bytes.
func trustHeld(data map[string][]byte, configKey string) string {
	n := 0
	for k := range data {
		if strings.HasSuffix(k, ".pub") {
			n++
		}
	}
	_, config := data[configKey]
	switch {
	case n == 1:
		return "1 public key"
	case n > 1:
		return fmt.Sprintf("%d public keys", n)
	case configKey != "" && config:
		return "a trust config"
	}
	return ""
}

func off(why string, objects []detect.Object) detect.Entry {
	return detect.EvidencePiece("off", why, objects, detect.Complete, "")
}

func unknown(why string) (bool, bool, detect.Entry) {
	return false, false, detect.EvidencePiece("unknown", "", nil, detect.Unknown, why)
}

// reason is the Kubernetes reason class of err, so a row never carries a
// server address.
func reason(err error) string {
	if r := apierrors.ReasonForError(err); r != "" {
		return string(r)
	}
	return "the API server did not answer"
}

// Entry is the gate's last row, unknown before its first check.
func (g *Gate) Entry() detect.Entry {
	if e := g.state.Load(); e != nil {
		return *e
	}
	return detect.EvidencePiece("unknown", "", nil, detect.Unknown, "the evidence gate has not run yet")
}

// EvidenceURL is URL while the last known decision is on, else empty, so an
// install whose gate is off never wires an unreachable evidence source.
func (g *Gate) EvidenceURL() string {
	if g.running.Load() {
		return g.URL
	}
	return ""
}

// Piece is the profile idp evidence row: the gate's under
// --evidence-server-auto (g set), else what evidence.enabled says.
func Piece(g *Gate, enabled bool) detect.Entry {
	switch {
	case g != nil:
		return g.Entry()
	case enabled:
		return detect.EvidencePiece("on", "evidence.enabled is true", nil, detect.Complete, "")
	}
	return detect.EvidencePiece("off", "evidence.enabled is false", nil, detect.Complete, "")
}
