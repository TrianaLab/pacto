/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"oras.land/oras-go/v2/registry/remote"
	"sigs.k8s.io/controller-runtime/pkg/client"

	core "github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/evidenceingest"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// snapshotPage is how many objects one list request of the target snapshot
// asks for.
const snapshotPage = 500

// snapshotWorkloads are the workload kinds the target snapshot reads: the
// ones the CLI's workload source reads.
var snapshotWorkloads = []schema.GroupVersionKind{
	{Group: "apps", Version: "v1", Kind: "Deployment"},
	{Group: "apps", Version: "v1", Kind: "StatefulSet"},
	{Group: "apps", Version: "v1", Kind: "DaemonSet"},
	{Group: "batch", Version: "v1", Kind: "CronJob"},
}

// TargetPublisher publishes the cluster's signed target snapshot to
// <registry>/pacto-targets:cluster-<uid> at once and then every Interval, so CI
// reads what runs with registry credentials and no kubeconfig. It reads Pactos
// and the four workload kinds, never Secrets, ConfigMaps, pods or nodes.
type TargetPublisher struct {
	Client    client.Reader
	Repo      *remote.Repository
	Signer    core.Signer
	ClusterID string
	// Publisher is the recorder ServiceAccount, written as a claim.
	Publisher string
	// Mode is the registry mode the record store detected, empty if unknown.
	Mode func(context.Context) string
	// EvidenceURL is the installation's Evidence Server; when set, each
	// snapshot carries the targets it accepted from any producer, read with
	// HTTP.
	EvidenceURL string
	HTTP        *http.Client
	Now         func() time.Time
	Interval    time.Duration
	// Tick, when set, replaces the ticker (tests).
	Tick func(time.Duration) <-chan time.Time
	Log  logr.Logger
	// maxBytes overrides core.MaxTargetSnapshotBytes (tests).
	maxBytes int
}

// NeedLeaderElection makes one recorder per cluster the snapshot's writer.
func (p *TargetPublisher) NeedLeaderElection() bool { return true }

// Start publishes at once, then on every tick until ctx ends. A failed publish
// is logged and retried on the next tick; CI sees the previous snapshot turn
// stale.
func (p *TargetPublisher) Start(ctx context.Context) error {
	tick := p.Tick
	if tick == nil {
		t := time.NewTicker(p.Interval)
		defer t.Stop()
		tick = func(time.Duration) <-chan time.Time { return t.C }
	}
	c := tick(p.Interval)
	for {
		if err := p.publish(ctx); err != nil {
			p.Log.Error(err, "target snapshot not published", "repository", p.Repo.Reference.Repository)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-c:
		}
	}
}

// publish reads, signs and pushes one snapshot: the Pactos, the workloads and,
// with an Evidence Server, the targets it accepted, each a section with its
// own completeness. A read that fails leaves its section partial with a
// limitation; a snapshot over the bound is refused.
func (p *TargetPublisher) publish(ctx context.Context) error {
	now := p.Now().UTC()
	asOf := now.Format(time.RFC3339)
	s := core.TargetSnapshot{Cluster: p.ClusterID, AsOf: asOf, Completeness: fleet.CompletenessComplete,
		RegistryMode: p.Mode(ctx), Publisher: p.Publisher, Pactos: []core.PactoView{}, Workloads: []fleet.RawInferredService{}}
	pactos := core.SnapshotSource{Kind: fleet.SourceKindKubernetes, AsOf: asOf, Completeness: fleet.CompletenessComplete}
	items, err := p.list(ctx, gvkPactoList)
	if err == nil {
		b, _ := json.Marshal(map[string]any{"items": items})
		s.Pactos, err = core.DecodePactos(b)
	}
	if err != nil {
		p.limit(&pactos, "pactos", err)
	}
	workloads := core.SnapshotSource{Kind: fleet.SourceKindKubernetesWorkloads, AsOf: asOf, Completeness: fleet.CompletenessComplete}
	p.collectWorkloads(ctx, &s, &workloads, now)
	s.Sources = []core.SnapshotSource{pactos, workloads}
	if p.EvidenceURL != "" {
		s.Sources = append(s.Sources, p.collectEvidence(ctx, &s, asOf))
	}
	for _, sec := range s.Sources {
		if sec.Completeness != fleet.CompletenessComplete {
			s.Completeness = fleet.CompletenessPartial
		}
	}
	body, _ := json.Marshal(s) // plain data
	if limit := cmp.Or(p.maxBytes, core.MaxTargetSnapshotBytes); len(body) > limit {
		return fmt.Errorf("target snapshot is %d bytes, over the %d byte bound", len(body), limit)
	}
	bundle, err := core.SignTargetSnapshot(ctx, p.Signer, core.NewTargetSnapshotStatement(s))
	if err != nil {
		return err
	}
	desc, err := deliveryrecord.PushTargetSnapshot(ctx, p.Repo, p.ClusterID, bundle)
	if err != nil {
		return err
	}
	p.Log.V(1).Info("target snapshot published", "digest", desc.Digest.String(), "pactos", len(s.Pactos),
		"workloads", len(s.Workloads), "evidence", len(s.Evidence) > 0, "completeness", s.Completeness)
	return nil
}

// collectWorkloads adds every workload of the four kinds as an inferred
// service, without probes: a probe path is exposure to the registry's read
// audience and impact never reads it. Env never reaches the shape reader's
// output.
func (p *TargetPublisher) collectWorkloads(ctx context.Context, s *core.TargetSnapshot, sec *core.SnapshotSource, now time.Time) {
	for _, gvk := range snapshotWorkloads {
		items, err := p.list(ctx, gvk.GroupVersion().WithKind(gvk.Kind+"List"))
		if err != nil {
			p.limit(sec, gvk.Kind, err)
			continue
		}
		for _, it := range items {
			in := fleet.InferredServiceOf(gvk.Kind, it.Object, now)
			in.Probes = nil
			s.Workloads = append(s.Workloads, in)
		}
	}
}

// collectEvidence puts the Evidence Server's targets body in s, verbatim, and
// returns its section. A body a reader could not map is never carried: the
// section is partial instead.
func (p *TargetPublisher) collectEvidence(ctx context.Context, s *core.TargetSnapshot, asOf string) core.SnapshotSource {
	sec := core.SnapshotSource{Kind: fleet.SourceKindEvidenceHTTP, AsOf: asOf, Completeness: fleet.CompletenessComplete}
	body, err := p.readEvidence(ctx)
	if err == nil {
		_, err = evidenceingest.TargetsCollection("evidence", body)
	}
	if err != nil {
		p.limit(&sec, "the Evidence Server targets", err)
		return sec
	}
	s.Evidence = body
	return sec
}

// readEvidence reads the targets body of EvidenceURL, bounded as an
// --evidence-url source reads it.
func (p *TargetPublisher) readEvidence(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.EvidenceURL+evidenceingest.TargetsPath, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the Evidence Server answered HTTP %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, evidenceingest.MaxTargetsBodyBytes+1)) // a failed read fails the decode
	if len(body) > evidenceingest.MaxTargetsBodyBytes {
		return nil, fmt.Errorf("the Evidence Server targets are over the %d byte bound", evidenceingest.MaxTargetsBodyBytes)
	}
	return body, nil
}

// limit marks sec partial because what could not be read. The limitation
// carries no error text: the snapshot's read audience is wider than the
// cluster's.
func (p *TargetPublisher) limit(sec *core.SnapshotSource, what string, err error) {
	p.Log.Error(err, "target snapshot read failed", "kind", what)
	sec.Completeness = fleet.CompletenessPartial
	sec.Limitations = append(sec.Limitations, fleet.Limitation{Code: fleet.LimitationSourcePartial, Source: what,
		Message: "the recorder could not read " + what})
}

// list reads every object of the list kind gvk in pages.
func (p *TargetPublisher) list(ctx context.Context, gvk schema.GroupVersionKind) ([]unstructured.Unstructured, error) {
	var out []unstructured.Unstructured
	cont := ""
	for {
		page := &unstructured.UnstructuredList{}
		page.SetGroupVersionKind(gvk)
		if err := p.Client.List(ctx, page, client.Limit(snapshotPage), client.Continue(cont)); err != nil {
			return nil, err
		}
		out = append(out, page.Items...)
		if cont = page.GetContinue(); cont == "" {
			return out, nil
		}
	}
}
