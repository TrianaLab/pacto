/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package targetsnapshot publishes the cluster's signed target snapshot to
// <registry>/pacto-targets:cluster-<uid>, from the operator, whenever the
// installation registry is set. Delivery is not needed: one writer per
// cluster, signed with the installation key.
package targetsnapshot

import (
	"cmp"
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"oras.land/oras-go/v2/registry/remote"
	"sigs.k8s.io/controller-runtime/pkg/client"

	core "github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/evidenceingest"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/signing"
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

// gvkPactoList is the Pacto list kind, read unstructured.
var gvkPactoList = schema.GroupVersionKind{Group: "pacto.trianalab.io", Version: "v1alpha1", Kind: "PactoList"}

// TargetPublisher publishes the cluster's signed target snapshot to
// <registry>/pacto-targets:cluster-<uid> at once and then every Interval, so CI
// reads what runs with registry credentials and no kubeconfig. It reads Pactos
// and the four workload kinds, never Secrets, ConfigMaps, pods or nodes.
type TargetPublisher struct {
	Client    client.Reader
	Repo      *remote.Repository
	Signer    core.Signer
	ClusterID string
	// Key is the installation key Signer signs with; the join block names
	// its public half, so a hub can approve this cluster as a member.
	Key ed25519.PrivateKey
	// ClusterName and ClusterNameSource are the join block's display name,
	// a DNS label, and where it came from.
	ClusterName, ClusterNameSource string
	// Publisher is the operator ServiceAccount, written as a claim.
	Publisher string
	// Namespaces, when set, are the only namespaces read (a scoped
	// operator): the snapshot is then partial with TARGET_SNAPSHOT_SCOPED, so
	// a reader never takes an unread namespace for an empty one.
	Namespaces []string
	// OnPush, when set, receives the result of every publish attempt.
	OnPush func(detect.PushResult)
	// Mode is the registry mode the record store detected, empty if unknown.
	Mode func(context.Context) string
	// EvidenceURL, read on every publish, is the installation's Evidence
	// Server; while it returns a URL, each snapshot carries the targets it
	// accepted from any producer, read with HTTP. Nil or empty reads none.
	EvidenceURL func() string
	HTTP        *http.Client
	// OwnerPolicy, when set, is published as the snapshot's ownerPolicy: who
	// may set a service owner, signed with the installation key.
	OwnerPolicy *core.OwnerPolicy
	// Linked, when set, is the linker's last result: its targets ride in
	// Pactos (a released reader reads them as Pacto targets) and its
	// completeness marks the Pacto section.
	Linked func() core.LinkedTargets
	// Requests, when set, says whether the cluster takes member requests:
	// its answer is the join block's requests, nil omits them.
	Requests func() *core.JoinRequests
	Now      func() time.Time
	Interval time.Duration
	// Tick, when set, replaces the ticker (tests).
	Tick func(time.Duration) <-chan time.Time
	Log  logr.Logger
	// maxBytes overrides core.MaxTargetSnapshotBytes (tests).
	maxBytes int
	// linkPoll overrides linkPoll (tests).
	linkPoll time.Duration
}

// The operator starts the linker and the publisher together: a first snapshot
// published before the linker finished a cycle would go out partial and stay
// up for a whole Interval. Start waits for that cycle, polling every
// linkPoll, at most linkWaits times.
const (
	linkPoll  = time.Second
	linkWaits = 120
)

// Start publishes at once, then on every tick until ctx ends. A failed publish
// is logged and retried on the next tick; CI sees the previous snapshot turn
// stale. The manager runs a plain Runnable only on the elected leader, so one
// operator per cluster is the snapshot's writer.
func (p *TargetPublisher) Start(ctx context.Context) error {
	tick := p.Tick
	if tick == nil {
		t := time.NewTicker(p.Interval)
		defer t.Stop()
		tick = func(time.Duration) <-chan time.Time { return t.C }
	}
	c := tick(p.Interval)
	for i := 0; p.Linked != nil && p.Linked().AsOf == "" && i < linkWaits; i++ {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(cmp.Or(p.linkPoll, linkPoll)):
		}
	}
	for {
		p.attempt(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-c:
		}
	}
}

// attempt publishes once, logs a failure and reports the result to OnPush.
func (p *TargetPublisher) attempt(ctx context.Context) {
	err := p.publish(ctx)
	res := detect.PushResult{At: p.Now().UTC(), Ref: p.Repo.Reference.Registry + "/" + p.Repo.Reference.Repository + ":cluster-" + p.ClusterID}
	if err != nil {
		res.Error = err.Error()
		p.Log.Error(err, "target snapshot not published", "repository", p.Repo.Reference.Repository)
	}
	if p.OnPush != nil {
		p.OnPush(res)
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
		RegistryMode: p.Mode(ctx), Publisher: p.Publisher, Pactos: []core.PactoView{}, Workloads: []fleet.RawInferredService{},
		OwnerPolicy: p.OwnerPolicy}
	pactos := core.SnapshotSource{Kind: fleet.SourceKindKubernetes, AsOf: asOf, Completeness: fleet.CompletenessComplete}
	items, err := p.list(ctx, gvkPactoList)
	if err == nil {
		b, _ := json.Marshal(map[string]any{"items": items})
		s.Pactos, err = core.DecodePactos(b)
	}
	if err != nil {
		p.limit(&pactos, "pactos", err)
	}
	for i := range s.Pactos {
		// Only the linker writes link labels; on a Pacto they are free text.
		core.StripLinkLabels(s.Pactos[i].Metadata.Labels)
	}
	s.Join = p.join(s.Pactos)
	p.addLinked(&s, &pactos, now)
	workloads := core.SnapshotSource{Kind: fleet.SourceKindKubernetesWorkloads, AsOf: asOf, Completeness: fleet.CompletenessComplete}
	p.collectWorkloads(ctx, &s, &workloads, now)
	if len(p.Namespaces) > 0 {
		scoped := fleet.Limitation{Code: fleet.LimitationTargetSnapshotScoped,
			Message: "read only namespaces " + strings.Join(p.Namespaces, ", ")}
		for _, sec := range []*core.SnapshotSource{&pactos, &workloads} {
			sec.Completeness = fleet.CompletenessPartial
			sec.Limitations = append(sec.Limitations, scoped)
		}
		s.Limitations = append(s.Limitations, scoped)
	}
	s.Sources = []core.SnapshotSource{pactos, workloads}
	if url := p.evidenceURL(); url != "" {
		s.Sources = append(s.Sources, p.collectEvidence(ctx, &s, asOf, url))
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

// addLinked appends the linker's targets to s.Pactos and merges its
// limitations, except the informational LINK_DIGEST_OFF, into sec, which
// turns partial when the linker's result is partial or older than
// core.LinkedStaleAfter.
func (p *TargetPublisher) addLinked(s *core.TargetSnapshot, sec *core.SnapshotSource, now time.Time) {
	if p.Linked == nil {
		return
	}
	l := p.Linked()
	s.Pactos = append(s.Pactos, l.Targets...)
	for _, lim := range l.Limitations {
		if lim.Code != core.LimitationLinkDigestOff {
			sec.Limitations = append(sec.Limitations, lim)
		}
	}
	asOf, err := time.Parse(time.RFC3339, l.AsOf)
	if l.Completeness != fleet.CompletenessComplete || err != nil || now.Sub(asOf) > core.LinkedStaleAfter {
		sec.Completeness = fleet.CompletenessPartial
	}
}

// join is the snapshot's join block: the display name, the installation
// public key and its fingerprint, the environments pactos suggest and
// whether the cluster takes member requests.
func (p *TargetPublisher) join(pactos []core.PactoView) *core.SnapshotJoin {
	pub := p.Key.Public()
	der, _ := x509.MarshalPKIXPublicKey(pub) // an Ed25519 key always marshals
	j := &core.SnapshotJoin{Name: p.ClusterName, NameSource: p.ClusterNameSource, KeyID: signing.Hint(pub),
		PublicKey: base64.StdEncoding.EncodeToString(der), Environments: core.AdvertisedEnvironments(pactos, p.ClusterName, p.ClusterNameSource)}
	if p.Requests != nil {
		j.Requests = p.Requests()
	}
	return j
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
func (p *TargetPublisher) collectEvidence(ctx context.Context, s *core.TargetSnapshot, asOf, url string) core.SnapshotSource {
	sec := core.SnapshotSource{Kind: fleet.SourceKindEvidenceHTTP, AsOf: asOf, Completeness: fleet.CompletenessComplete}
	body, err := p.readEvidence(ctx, url)
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

// evidenceURL is what EvidenceURL returns now, empty when it is nil.
func (p *TargetPublisher) evidenceURL() string {
	if p.EvidenceURL == nil {
		return ""
	}
	return p.EvidenceURL()
}

// readEvidence reads the targets body of the Evidence Server at url, bounded
// as an --evidence-url source reads it.
func (p *TargetPublisher) readEvidence(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+evidenceingest.TargetsPath, nil)
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
		Message: "the operator could not read " + what})
}

// list reads every object of the list kind gvk in pages: cluster-wide, or in
// each of Namespaces.
func (p *TargetPublisher) list(ctx context.Context, gvk schema.GroupVersionKind) ([]unstructured.Unstructured, error) {
	if len(p.Namespaces) == 0 {
		return p.listIn(ctx, gvk)
	}
	var out []unstructured.Unstructured
	for _, ns := range p.Namespaces {
		items, err := p.listIn(ctx, gvk, client.InNamespace(ns))
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	return out, nil
}

// listIn reads every object of the list kind gvk that opts select, in pages.
func (p *TargetPublisher) listIn(ctx context.Context, gvk schema.GroupVersionKind, opts ...client.ListOption) ([]unstructured.Unstructured, error) {
	var out []unstructured.Unstructured
	cont := ""
	for {
		page := &unstructured.UnstructuredList{}
		page.SetGroupVersionKind(gvk)
		if err := p.Client.List(ctx, page, append(opts, client.Limit(snapshotPage), client.Continue(cont))...); err != nil {
			return nil, err
		}
		out = append(out, page.Items...)
		if cont = page.GetContinue(); cont == "" {
			return out, nil
		}
	}
}
