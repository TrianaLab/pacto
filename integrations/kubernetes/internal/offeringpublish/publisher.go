/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package offeringpublish publishes, from the operator, one inferred offering
// per Crossplane XRD to the installation's own contribution repository
// <registry>/pacto-inferred/crossplane-<clusterID>/services, signed with the
// installation key, and keeps what its last runs observed for the detection
// advertisement.
package offeringpublish

import (
	"cmp"
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing/fstest"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-containerregistry/pkg/authn"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/trianalab/pacto/integrations/kubernetes/v7/internal/xrdoffering"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// OfferingInterval is how often the operator maps the XRDs.
const OfferingInterval = time.Minute

// Bounds of one run, charged before the work: the list asks for one more
// item than it handles, so one more means the rest is unknown.
const (
	maxXRDs         = detect.MaxObservedOfferings
	maxCompositions = 1000
)

// crossplaneGroup is the Crossplane API group the XRDs and Compositions are in.
const crossplaneGroup = "apiextensions.crossplane.io"

// notServed is the partial reason of a tick with no Crossplane API.
const notServed = "crossplane API not served"

// Config is what the operator hands the publisher.
type Config struct {
	// Registry is the installation registry (--registry).
	Registry string
	// ClusterID is the cluster's id (the kube-system namespace UID): the
	// producer is crossplane-<ClusterID>, so installations sharing a
	// registry never write one repository.
	ClusterID string
	// Key is the installation key.
	Key ed25519.PrivateKey
	// Keychain answers only under the push prefixes.
	Keychain authn.Keychain
	Insecure []string
	Dynamic  dynamic.Interface
	// Served reports whether a group version is served (detection.ServedFor).
	Served func(gv string) (bool, error)
	// Pacto is the operator version the records name.
	Pacto string
	// Watched is the namespace list of a scoped operator, which reads no
	// cluster-scoped XRD and so publishes nothing.
	Watched []string
}

// ForOperator is the operator's publisher, nil without a registry or on a
// scoped operator. Whether the Crossplane API is served is asked on every
// tick, so a Crossplane installed later is published without a restart.
func ForOperator(cfg Config) (*Publisher, error) {
	if cfg.Registry == "" || len(cfg.Watched) > 0 {
		return nil, nil
	}
	if len(cfg.Key) != ed25519.PrivateKeySize {
		return nil, errors.New("the inferred offerings need the installation key")
	}
	if cfg.ClusterID == "" {
		return nil, errors.New("the inferred offerings need the cluster id: the operator could not read the kube-system namespace UID")
	}
	return &Publisher{cfg: cfg}, nil
}

// The publisher only reads XRDs and Compositions; it writes to the registry.
// +kubebuilder:rbac:groups=apiextensions.crossplane.io,resources=compositeresourcedefinitions;compositions,verbs=get;list;watch

// Publisher maps every XRD at once and then every OfferingInterval, and
// pushes each offering whose content changed. The manager runs it only on the
// elected leader.
type Publisher struct {
	cfg Config
	// now, tick and log replace the clock, the ticker and the logger (tests).
	now  func() time.Time
	tick func(time.Duration) <-chan time.Time
	log  logr.Logger
	// reg, when set, replaces the scoped registry client (tests).
	reg publish.Registry

	mu sync.Mutex
	ad detect.OfferingsAd
}

// producer is the producer id the installation contributes as.
func (p *Publisher) producer() string { return "crossplane-" + p.cfg.ClusterID }

// repository is the installation's own contribution repository.
func (p *Publisher) repository() string {
	return contract.ContributionRepository(strings.TrimSuffix(p.cfg.Registry, "/"), p.producer())
}

// clock is the publisher's time.
func (p *Publisher) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
}

// Start maps at once, then on every tick until ctx ends.
func (p *Publisher) Start(ctx context.Context) error {
	if p.log.GetSink() == nil {
		p.log = ctrl.Log.WithName("offerings")
	}
	tick := p.tick
	if tick == nil {
		t := time.NewTicker(OfferingInterval)
		defer t.Stop()
		tick = func(time.Duration) <-chan time.Time { return t.C }
	}
	c := tick(OfferingInterval)
	for {
		p.run(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-c:
		}
	}
}

// run maps every XRD and publishes what changed. A tick with no Crossplane
// API, a failed list, a run past a bound or an offering it could not publish
// keeps the last complete observation and says why it is partial.
func (p *Publisher) run(ctx context.Context) {
	ver, err := p.served()
	if err != nil || ver == "" {
		p.partial(cmpErr(err, notServed))
		return
	}
	xrds, comps, why, err := p.list(ctx, ver)
	if err != nil {
		PublishErrors.Inc()
		p.partial("listing failed: " + fleet.SanitizedReason(err))
		return
	}
	reg := p.reg
	if reg == nil {
		reg = publish.NewScopedClient(p.cfg.Keychain, oci.NewPushScope(p.cfg.Registry), remote.DefaultTransport, p.cfg.Insecure...)
	}
	repo := p.repository()
	tags, err := reg.Tags(ctx, repo)
	if err != nil {
		PublishErrors.Inc()
		p.partial("reading " + repo + " failed: " + fleet.SanitizedReason(err))
		return
	}
	observed, skipped, collided, failed := []string{}, 0, 0, 0
	for _, x := range xrds {
		o, err := xrdoffering.Map(&x, comps, p.cfg.Pacto)
		if err == nil && slices.Contains(observed, o.Slug) {
			// An XRD that maps to an offering but loses its slug is left out
			// of a run that would otherwise read complete: say so. One that
			// maps to no offering is no offering, only counted.
			collided++
			err = fmt.Errorf("XRD %s: slug %s is another XRD's", x.GetName(), o.Slug)
		}
		if err != nil {
			skipped++
			p.log.Info("XRD skipped", "reason", err.Error())
			continue
		}
		observed = append(observed, o.Slug)
		if err := p.publish(ctx, reg, repo, tags, o); err != nil {
			failed++
			PublishErrors.Inc()
			p.log.Error(err, "offering not published", "offering", o.Slug, "repository", repo)
		}
	}
	if collided > 0 {
		why = strings.TrimPrefix(why+"; "+strconv.Itoa(collided)+" XRDs share a slug with another and were left out", "; ")
	}
	if failed > 0 {
		why = strings.TrimPrefix(why+"; "+strconv.Itoa(failed)+" offerings could not be published", "; ")
	}
	p.log.Info("inferred offerings", "repository", repo, "observed", len(observed), "skipped", skipped, "failed", failed)
	if why != "" {
		p.partial(why)
		return
	}
	slices.Sort(observed)
	now := p.clock().UTC().Truncate(time.Second)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ad = detect.OfferingsAd{Producer: p.producer(), Repository: repo, Observed: observed, AsOf: &now}
}

// Advertisement is what detection advertises: the producer, its repository,
// the last complete run's observation and why the newest run stopped short.
// Before the first run finishes it is partial, so nothing reads as observed.
// It is nil while no Crossplane API was ever served: a cluster without
// Crossplane has no producer, so every answer reads as before.
func (p *Publisher) Advertisement() *detect.OfferingsAd {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ad.AsOf == nil && p.ad.Partial == notServed {
		return nil
	}
	ad := p.ad
	ad.Producer, ad.Repository, ad.Observed = p.producer(), p.repository(), slices.Clone(ad.Observed)
	if ad.AsOf == nil && ad.Partial == "" {
		ad.Partial = "the first run has not finished"
	}
	return &ad
}

// cmpErr is err's text, or alt without one.
func cmpErr(err error, alt string) string {
	if err != nil {
		return "reading the API failed: " + fleet.SanitizedReason(err)
	}
	return alt
}

// partial records why the newest run stopped short, keeping the last
// complete run's observation.
func (p *Publisher) partial(why string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ad.Producer, p.ad.Repository, p.ad.Partial = p.producer(), p.repository(), why
}

// served is the XRD API version served, v2 before v1, or "" when neither is.
func (p *Publisher) served() (string, error) {
	for _, v := range []string{"v2", "v1"} {
		ok, err := p.cfg.Served(crossplaneGroup + "/" + v)
		if err != nil || ok {
			return v, err
		}
	}
	return "", nil
}

// list reads the XRDs at ver and the Compositions, each sorted by name and
// cut at its bound, with why the run is partial when one was cut.
func (p *Publisher) list(ctx context.Context, ver string) (xrds, comps []unstructured.Unstructured, why string, err error) {
	read := func(resource, version string, limit int) ([]unstructured.Unstructured, bool, error) {
		gvr := schema.GroupVersionResource{Group: crossplaneGroup, Version: version, Resource: resource}
		l, err := p.cfg.Dynamic.Resource(gvr).List(ctx, metav1.ListOptions{Limit: int64(limit + 1)})
		if err != nil {
			return nil, false, err
		}
		items := l.Items
		slices.SortFunc(items, func(a, b unstructured.Unstructured) int { return strings.Compare(a.GetName(), b.GetName()) })
		return items[:min(len(items), limit)], len(items) > limit, nil
	}
	xrds, xcut, err := read("compositeresourcedefinitions", ver, maxXRDs)
	if err != nil {
		return nil, nil, "", err
	}
	comps, ccut, err := read("compositions", "v1", maxCompositions)
	if err != nil {
		return nil, nil, "", err
	}
	var reasons []string
	if xcut {
		reasons = append(reasons, "more than "+strconv.Itoa(maxXRDs)+" XRDs, the rest unknown")
	}
	if ccut {
		reasons = append(reasons, "more than "+strconv.Itoa(maxCompositions)+" Compositions, classes may be missing")
	}
	return xrds, comps, strings.Join(reasons, "; "), nil
}

// tagVersion is one tag of an offering: its generation and change count.
type tagVersion struct{ gen, k int64 }

func (v tagVersion) String() string { return xrdoffering.Version(v.gen, v.k) }

// newest is the newest version tags hold for slug, ok false when none.
func newest(tags []string, slug string) (tagVersion, bool) {
	var out []tagVersion
	for _, t := range tags {
		rest, ok := strings.CutPrefix(t, slug+"_0.0.0-inferred.")
		g, k, two := strings.Cut(rest, ".")
		gen, gerr := strconv.ParseInt(g, 10, 64)
		kk, kerr := strconv.ParseInt(k, 10, 64)
		v := tagVersion{gen, kk}
		if ok && two && gerr == nil && kerr == nil && slug+"_"+v.String() == t {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return tagVersion{}, false
	}
	return slices.MaxFunc(out, func(a, b tagVersion) int {
		return cmp.Or(cmp.Compare(a.gen, b.gen), cmp.Compare(a.k, b.k))
	}), true
}

// publish pushes o when it differs from the newest tag of its slug this
// installation signed: as the next change count of its generation, or of the
// newest one when the XRD's generation went back. The same content is
// re-pushed under its tag once its record ages, so it never reads stale. A
// newest tag another key signed, or none signed, is never compared: the
// next version goes above it.
func (p *Publisher) publish(ctx context.Context, reg publish.Registry, repo string, tags []string, o xrdoffering.Offering) error {
	top, ok := newest(tags, o.Slug)
	if !ok {
		return p.push(ctx, reg, repo, o, xrdoffering.Version(o.Generation, 0), "")
	}
	same, aging, renews, err := p.compare(ctx, reg, repo, o.Slug+"_"+top.String(), o.At(top.String()))
	switch {
	case err != nil:
		return err
	case same && aging:
		return p.push(ctx, reg, repo, o, top.String(), renews)
	case same:
		return nil
	case o.Generation > top.gen:
		return p.push(ctx, reg, repo, o, xrdoffering.Version(o.Generation, 0), "")
	default:
		// ponytail: a recreated XRD whose generation went back is published
		// under the newest generation; source/object.yaml keeps its own.
		return p.push(ctx, reg, repo, o, xrdoffering.Version(top.gen, top.k+1), "")
	}
}

// compare reports whether repo:tag holds want's files and is signed by the
// installation key, whether its record is older than half the freshness
// window, and the digest a renewal of it names: the one it renews itself, or
// its own.
func (p *Publisher) compare(ctx context.Context, reg publish.Registry, repo, tag string, want xrdoffering.Offering) (same, aging bool, renews string, err error) {
	h, err := reg.Head(ctx, repo+":"+tag)
	if err != nil || h == nil {
		return false, false, "", fmt.Errorf("reading %s: %v", tag, err)
	}
	verify := publish.PolicyVerifier(signing.Policy{Key: p.cfg.Key.Public()})
	switch err := publish.VerifyDigest(ctx, reg, verify, repo, h.Digest); {
	case errors.Is(err, publish.ErrNoSignature) || errors.Is(err, publish.ErrSignatureInvalid):
		return false, false, "", nil
	case err != nil:
		return false, false, "", err
	}
	b, err := reg.Pull(ctx, repo+"@"+h.Digest)
	if err != nil {
		return false, false, "", err
	}
	created, perr := time.Parse(time.RFC3339, h.Annotations[publish.AnnotationCreated])
	aging = perr != nil || p.clock().Sub(created) > fleet.RecentEvidenceWindow/2
	return sameFiles(b.FS, want.Files), aging, cmp.Or(h.Annotations[contract.AnnotationRenews], h.Digest), nil
}

// sameFiles reports whether fsys holds exactly files.
func sameFiles(fsys fs.FS, files map[string][]byte) bool {
	n := 0
	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		n++
		data, err := fs.ReadFile(fsys, path)
		if err != nil || string(data) != string(files[path]) {
			return fs.ErrInvalid
		}
		return nil
	})
	return err == nil && n == len(files)
}

// push writes o at version to repo, annotated with when it was pushed and,
// on a renewal, the digest it renews (so drift reads it as that record), tags
// it <slug>_<version> and signs its digest with the installation key.
func (p *Publisher) push(ctx context.Context, reg publish.Registry, repo string, o xrdoffering.Offering, version, renews string) error {
	o = o.At(version)
	fsys := fstest.MapFS{}
	for _, name := range slices.Sorted(maps.Keys(o.Files)) {
		fsys[name] = &fstest.MapFile{Data: o.Files[name]}
	}
	c, _ := contract.Parse(strings.NewReader(string(o.Files["pacto.yaml"]))) // Map's pacto.yaml always parses
	b := &contract.Bundle{Contract: c, RawYAML: o.Files["pacto.yaml"], FS: fsys}
	ann := map[string]string{publish.AnnotationCreated: p.clock().UTC().Format(time.RFC3339)}
	if renews != "" {
		ann[contract.AnnotationRenews] = renews
	}
	annotated := func() v1.Image {
		img, _ := oci.BundleImage(b) // the bundle holds a contract and an FS
		return mutate.Annotations(img, ann).(v1.Image)
	}
	// The layer is a stream read once: one image is drained for its digest,
	// a second is written under it.
	img := annotated()
	layers, _ := img.Layers()
	rc, _ := layers[0].Compressed()
	_, _ = io.Copy(io.Discard, rc)
	_ = rc.Close()
	h, _ := img.Digest()
	if _, err := reg.Write(ctx, repo, digested{annotated(), h}); err != nil {
		return fmt.Errorf("pushing %s: %w", version, err)
	}
	tag := o.Slug + "_" + version
	if err := reg.Tag(ctx, repo, h.String(), tag); err != nil {
		return fmt.Errorf("tagging %s: %w", tag, err)
	}
	kp, _ := signing.FromPrivateKey(p.cfg.Key) // a full-size Ed25519 key always loads
	if err := publish.SignDigest(ctx, reg, signing.Keyed(kp, ""), repo, h.String()); err != nil {
		return fmt.Errorf("signing %s: %w", tag, err)
	}
	Published.Inc()
	return nil
}

// digested is an image whose streamed layer was drained, with that digest.
type digested struct {
	v1.Image
	digest v1.Hash
}

func (d digested) Digest() (v1.Hash, error) { return d.digest, nil }
