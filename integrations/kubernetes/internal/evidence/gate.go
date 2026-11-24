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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

// Gate decides, on every reconcile tick, whether the Evidence Server runs
// under profile idp (--evidence-server-auto): only while the registry serves
// native OCI Referrers, a subject or subject prefix is set on that registry
// and the trust Secret holds what the server reads as trust. A check that
// cannot tell keeps the current state: unknown is never positive, and it
// never deletes a running server. Before the first known decision the current
// state is whether the managed Deployment exists, so an operator restart
// keeps the URL of a server that still runs.
type Gate struct {
	// Reader must be uncached (mgr.GetAPIReader()): the trust Secret is read
	// with one Get by name, as the Secret RBAC is get only.
	Reader client.Reader
	// Namespace and TrustSecret name the trust Secret; TrustConfigKey, when
	// set, is the one key of it the server reads as trust.
	Namespace, TrustSecret, TrustConfigKey string
	// Registry is the installation registry (host/path); empty keeps it off.
	Registry string
	// Subjects are the subjects and subject prefixes (oci://host/...). The
	// registry alone never counts as a scope.
	Subjects []string
	// Probe reports what the registry holding the target snapshot supports.
	Probe func(ctx context.Context) (capabilities.Capabilities, error)
	// LastPush is the target snapshot publisher's last attempt, nil before
	// the first.
	LastPush func() *detect.PushResult
	// URL is the managed Evidence Server's internal URL.
	URL string

	// FirstPush, when set, is closed by the first target snapshot push, so
	// the reconciler checks the gate then instead of on its next tick.
	FirstPush <-chan struct{}

	state   atomic.Pointer[detect.Entry]
	running atomic.Bool
	decided atomic.Bool
}

// Check decides the gate once: on, whether the decision is known, and the
// detected row that says why. A known decision is remembered; an unknown one
// leaves the remembered state as it was, or, before any known decision, takes
// it from whether the managed Deployment exists.
func (g *Gate) Check(ctx context.Context) (on, known bool, e detect.Entry) {
	on, known, e = g.check(ctx)
	g.state.Store(&e)
	switch {
	case known:
		g.running.Store(on)
		g.decided.Store(true)
	case !g.decided.Load():
		g.running.Store(g.Reader.Get(ctx, client.ObjectKey{Namespace: g.Namespace, Name: Name}, &appsv1.Deployment{}) == nil)
	}
	return on, known, e
}

func (g *Gate) check(ctx context.Context) (on, known bool, e detect.Entry) {
	host, _, _ := strings.Cut(g.Registry, "/")
	switch {
	case g.Registry == "":
		return false, true, off("no registry value", nil)
	case len(g.Subjects) == 0:
		return false, true, off("set evidence.registry.subjectPrefixes", nil)
	}
	if other := otherHost(g.Subjects, host); other != "" {
		return unknown("subjects on " + other + ", which is not the registry, so Pacto cannot probe its OCI Referrers")
	}
	// Neither a push error nor a probe error enters the row: each can name
	// the registry repository and the cluster-<id> tag of this cluster.
	push := g.LastPush()
	switch {
	case push == nil:
		return unknown("no target snapshot pushed yet to probe " + host)
	case push.Error != "":
		return unknown("the last target snapshot push failed; see the operator log")
	}
	caps, err := g.Probe(ctx)
	switch {
	case err != nil:
		return unknown("probing " + host + " failed; see the operator log")
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
	switch {
	case trust == "" && g.TrustConfigKey != "":
		return false, true, off("the trust Secret holds no "+g.TrustConfigKey, secret)
	case trust == "":
		return false, true, off("the trust Secret holds no public key", secret)
	}
	return true, true, detect.EvidencePiece("on", host+" serves OCI Referrers; the trust Secret holds "+trust, secret, detect.Complete, "")
}

// trustHeld says what of data the server reads as trust: the trust config
// key alone when configKey is set (the server then reads only that file),
// else its non-empty *.pub keys. Empty when there is none. Only names and
// lengths are read, never the bytes.
func trustHeld(data map[string][]byte, configKey string) string {
	if configKey != "" {
		if len(data[configKey]) > 0 {
			return "a trust config"
		}
		return ""
	}
	n := 0
	for k, v := range data {
		if strings.HasSuffix(k, ".pub") && len(v) > 0 {
			n++
		}
	}
	switch n {
	case 0:
		return ""
	case 1:
		return "1 public key"
	}
	return fmt.Sprintf("%d public keys", n)
}

// otherHost is the first host of subjects (oci://host/... refs) that is not
// host, empty when every one is.
func otherHost(subjects []string, host string) string {
	for _, s := range subjects {
		if h, _, _ := strings.Cut(strings.TrimPrefix(s, "oci://"), "/"); h != host {
			return h
		}
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
