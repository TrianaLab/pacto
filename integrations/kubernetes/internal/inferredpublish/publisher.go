/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package inferredpublish publishes the inferred contract of every external
// system the cluster's Pactos name to <registry>/pacto-inferred, from the
// operator, whenever the installation registry is set. Delivery is not
// needed: the operator signs with the installation key.
package inferredpublish

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/inferredcontract"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// InferredInterval is how often the operator publishes inferred contracts.
const InferredInterval = 5 * time.Minute

// pactoPage is how many Pactos one list request asks for.
const pactoPage = 500

// gvkPactoList is the Pacto list kind, read unstructured.
var gvkPactoList = schema.GroupVersionKind{Group: "pacto.trianalab.io", Version: "v1", Kind: "PactoList"}

// Config is what the operator hands the publisher.
type Config struct {
	// Registry is the installation registry (--registry); empty publishes
	// nothing.
	Registry string
	// Key is the installation key (installkey.Ensure).
	Key ed25519.PrivateKey
	// Keychain answers only under the push prefixes (credentials.OperatorKeychain
	// with allowHostOnly).
	Keychain authn.Keychain
	Insecure []string
	Reader   client.Reader
	// Watched are the namespaces a scoped operator reads; empty reads all.
	Watched []string
	// Pacto is the operator version the records name.
	Pacto string
	// Detected is the detector's last Result (Detector.Last), read in
	// process; nil when no detector runs.
	Detected func() *detect.Result
}

// ForOperator is the operator's inferred contract publisher, or nil when no
// registry is set. Every write (image, tag and signature) goes through the
// installation push scope, and every contract is signed with the
// installation key.
func ForOperator(cfg Config) (manager.Runnable, error) {
	if cfg.Registry == "" {
		return nil, nil
	}
	if len(cfg.Key) != ed25519.PrivateKeySize {
		return nil, errors.New("the inferred contracts need the installation key")
	}
	kp, _ := signing.FromPrivateKey(cfg.Key) // a full-size Ed25519 key always loads
	reg := publish.NewScopedClient(cfg.Keychain, oci.NewPushScope(cfg.Registry), remote.DefaultTransport, cfg.Insecure...)
	return &InferredPublisher{Reader: cfg.Reader, Root: oci.InstallationRepository(cfg.Registry, oci.InferredRepository),
		Namespaces: cfg.Watched, Interval: InferredInterval, Log: ctrl.Log.WithName("inferred"), Detected: cfg.Detected,
		Deps: inferredcontract.Deps{Registry: reg, Signer: signing.Keyed(kp, ""),
			Verify: publish.PolicyVerifier(signing.Policy{Key: cfg.Key.Public()}), Pacto: cfg.Pacto, Now: time.Now}}, nil
}

// InferredPublisher publishes the inferred contracts at once and then every
// Interval. It reads only Pactos: their status names the external systems.
type InferredPublisher struct {
	Reader client.Reader
	Root   string
	Deps   inferredcontract.Deps
	// Namespaces, when set, are the only namespaces read (a scoped operator).
	Namespaces []string
	Interval   time.Duration
	// Tick, when set, replaces the ticker (tests).
	Tick func(time.Duration) <-chan time.Time
	Log  logr.Logger
	// Detected, when set, is the detector's last Result: its managed
	// resources and Secret refs join the snapshot.
	Detected func() *detect.Result
}

// Start publishes at once, then on every tick until ctx ends. The manager runs
// a plain Runnable only on the elected leader, so one operator per cluster
// writes the inferred contracts.
func (p *InferredPublisher) Start(ctx context.Context) error {
	tick := p.Tick
	if tick == nil {
		t := time.NewTicker(p.Interval)
		defer t.Stop()
		tick = func(time.Duration) <-chan time.Time { return t.C }
	}
	c := tick(p.Interval)
	for {
		p.run(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-c:
		}
	}
}

// run lists the Pactos, builds their snapshot and publishes what changed. A
// list error, or an operator that watches only some namespaces, makes the
// snapshot partial, so no published value is removed and every surface says
// partial.
func (p *InferredPublisher) run(ctx context.Context) {
	pactos, listErr := p.pactos(ctx)
	extra, undetected := p.detected()
	snap, err := delivery.Snapshot(ctx, pactos, listErr, nil, p.Deps.Now(), delivery.Members{}, extra...)
	var rep inferredcontract.Report
	if err == nil && len(p.Namespaces) > 0 {
		partial(snap, fleet.Limitation{Code: fleet.LimitationTargetSnapshotScoped,
			Message: "read only namespaces " + strings.Join(p.Namespaces, ", ")})
	}
	if err == nil && undetected {
		partial(snap, fleet.Limitation{Code: fleet.LimitationSourceUnavailable, Source: detectionSource,
			Message: "the detector has not run yet, so the managed resources are unknown"})
	}
	if err == nil {
		rep, err = inferredcontract.Publish(ctx, p.Deps, p.Root, snap)
	}
	if err != nil {
		PublishErrors.Inc()
		p.Log.Error(err, "inferred contracts not published", "repository", p.Root)
		return
	}
	Published.Add(float64(len(rep.Published)))
	PublishErrors.Add(float64(len(rep.Errors)))
	for _, e := range rep.Errors {
		p.Log.Error(errors.New(e.Error), "inferred contract not published", "repository", e.Repository)
	}
	p.Log.Info("inferred contracts", "repository", p.Root, "published", len(rep.Published), "repaired", len(rep.Repaired),
		"unchanged", rep.Unchanged, "errors", len(rep.Errors), "capped", rep.Capped, "listError", listErr != nil)
}

// detectionSource is the id of the source the detector's Result is read as.
const detectionSource = "detection"

// detected is the source of the detector's last Result, none when no
// detector runs. undetected says a detector runs but has not finished a run:
// the snapshot is then partial, so no observed engine is dropped. The
// snapshot's targets carry no cluster (one cluster, the operator's), so the
// resources and refs drop theirs to join them.
func (p *InferredPublisher) detected() (extra []fleet.Source, undetected bool) {
	if p.Detected == nil {
		return nil, false
	}
	res := p.Detected()
	if res == nil {
		return nil, true
	}
	col := &fleet.Collection{ManagedResources: slices.Clone(res.ManagedResources), SecretRefs: slices.Clone(res.SecretRefs)}
	for i := range col.ManagedResources {
		col.ManagedResources[i].Cluster, col.ManagedResources[i].ClusterName = "", ""
	}
	for i := range col.SecretRefs {
		col.SecretRefs[i].Cluster, col.SecretRefs[i].ClusterName = "", ""
	}
	// A capped list or a read that failed makes the snapshot partial, so
	// no published engine is dropped or narrowed on a transient failure.
	col.Limitations = append(detect.CappedLimitations(*res, detectionSource), detect.ReadsPartial(*res, detectionSource)...)
	return []fleet.Source{fleet.NewMemorySource(detectionSource, fleet.SourceKindDetection, col)}, false
}

// partial marks snap partial with lim.
func partial(snap *fleet.FleetSnapshot, lim fleet.Limitation) {
	snap.Completeness = fleet.CompletenessPartial
	snap.Limitations = append(snap.Limitations, lim)
}

// pactos reads every Pacto, cluster-wide or in each of Namespaces, in pages.
// A failed read keeps what was read before it, with the error: the snapshot
// is then partial and keeps every value already published.
func (p *InferredPublisher) pactos(ctx context.Context) ([]delivery.PactoView, error) {
	scopes := [][]client.ListOption{nil}
	if len(p.Namespaces) > 0 {
		scopes = make([][]client.ListOption, 0, len(p.Namespaces))
		for _, ns := range p.Namespaces {
			scopes = append(scopes, []client.ListOption{client.InNamespace(ns)})
		}
	}
	var items []unstructured.Unstructured
	var err error
read:
	for _, opts := range scopes {
		for cont := ""; ; {
			page := &unstructured.UnstructuredList{}
			page.SetGroupVersionKind(gvkPactoList)
			if err = p.Reader.List(ctx, page, append(opts, client.Limit(pactoPage), client.Continue(cont))...); err != nil {
				break read
			}
			items = append(items, page.Items...)
			if cont = page.GetContinue(); cont == "" {
				break
			}
		}
	}
	b, _ := json.Marshal(map[string]any{"items": items})
	pactos, derr := delivery.DecodePactos(b)
	return pactos, errors.Join(err, derr)
}
