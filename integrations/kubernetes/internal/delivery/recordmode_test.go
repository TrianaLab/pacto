/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v7/api/delivery/v1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

const testStream = "cid/prod/payments-prod"

// tagsOf lists the tags of path on reg.
func (r *testRegistry) tagsOf(t *testing.T, path string) []string {
	t.Helper()
	repo, _ := ocireferrer.Open(r.host+"/"+path, ocireferrer.Options{})
	tags, err := ocireferrer.Tags(context.Background(), repo, "")
	if err != nil {
		t.Fatal(err)
	}
	return tags
}

// recordTags lists the write-once record tags of the test stream in path.
func (r *testRegistry) recordTags(t *testing.T, path string) []string {
	t.Helper()
	var out []string
	for _, tag := range r.tagsOf(t, path) {
		if strings.HasPrefix(tag, deliveryrecord.AnchorTag(testStream)+"-") {
			out = append(out, tag)
		}
	}
	return out
}

// manifestAt reads the manifest tag or digest names in path on reg.
func (r *testRegistry) manifestAt(t *testing.T, path, ref string) ocispec.Manifest {
	t.Helper()
	repo, _ := ocireferrer.Open(r.host+"/"+path, ocireferrer.Options{})
	desc, err := repo.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := content.FetchAll(context.Background(), repo, desc)
	if err != nil {
		t.Fatal(err)
	}
	var m ocispec.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func subjectNames(st delivery.Statement) []string {
	out := make([]string, 0, len(st.Subject))
	for _, s := range st.Subject {
		out = append(out, s.Name)
	}
	return out
}

func TestRecorderWritesWriteOnceTagsWithoutReferrers(t *testing.T) {
	re := newRecEnv(t)
	re.reg.unsupported = true
	re.stage(1)
	re.concluded("ship", 1, delivery.Succeeded)
	re.reconcile()
	s := re.getStage()
	if c := condition(s); c.Status != "True" || c.Reason != ReasonWriteOnceTags ||
		!strings.Contains(c.Message, "as write-once tags: the registry serves no Referrers API") {
		t.Fatalf("condition %+v", c)
	}
	if s.Status.Stream.RegistryMode != string(capabilities.ReferrersWriteOnceTags) || s.Status.Stream.RecordedSeq != 1 {
		t.Fatalf("stream %+v", s.Status.Stream)
	}
	// The next pass reads the stream back from its tags and chains from it.
	re.setStage(func(s *delivery.StageStatus) { s.Stream.Seq = 2 })
	re.concluded("ship2", 2, delivery.Succeeded)
	re.reconcile()
	recs := re.records()
	if len(recs) != 2 || re.getStage().Status.Stream.RecordedSeq != 2 || recs[1].Statement.Predicate.Chain.Parent != recs[0].ID {
		t.Fatalf("records %+v", recs)
	}
	tags := re.reg.recordTags(t, "delivery/streams")
	if len(tags) != 2 {
		t.Fatalf("record tags = %v", tags)
	}
	for _, tag := range re.reg.tagsOf(t, "delivery/streams") {
		if strings.HasPrefix(tag, "sha256-") {
			t.Fatalf("the stream wrote the referrers-tag index %s", tag)
		}
	}
	if controllerutil.ContainsFinalizer(re.getOp("ship"), dv1.RecordFinalizer) {
		t.Fatal("the finalizer stayed after the record was written")
	}
}

func TestRecorderNativeRecordsMode(t *testing.T) {
	re := newRecEnv(t)
	re.stage(1)
	re.concluded("ship", 1, delivery.Succeeded)
	re.reconcile()
	s := re.getStage()
	if c := condition(s); c.Status != "True" || c.Reason != ReasonReferrersSupported {
		t.Fatalf("condition %+v", c)
	}
	if s.Status.Stream.RegistryMode != string(capabilities.ReferrersNative) {
		t.Fatalf("stream %+v", s.Status.Stream)
	}
	if tags := re.reg.recordTags(t, "delivery/streams"); len(tags) != 0 {
		t.Fatalf("a native stream was tagged: %v", tags)
	}
}

// TestApplyHostWithoutReferrersGetsNoPlacement: the apply and contract
// repositories share the streams host, which serves no Referrers API, so
// they gain neither a tag nor a referrer and the record names the anchor only.
func TestApplyHostWithoutReferrersGetsNoPlacement(t *testing.T) {
	re := newRecEnv(t)
	re.reg.unsupported = true
	re.stage(1)
	re.concluded("ship", 1, delivery.Succeeded)
	re.reconcile()
	recs := re.records()
	if len(recs) != 1 || !slices.Equal(subjectNames(recs[0].Statement), []string{"anchor"}) {
		t.Fatalf("records %+v", recs)
	}
	for _, path := range []string{"apps/payments", "pactos/payments"} {
		if tags := re.reg.tagsOf(t, path); len(tags) != 0 {
			t.Fatalf("%s was tagged: %v", path, tags)
		}
	}
}

// TestWriteOnceStreamsKeepsNativeApplyPlacement: streams on a registry
// without Referrers, the apply repository on one that serves them. The
// anchor placement is a write-once tag; the apply and contract placements
// stay confirmed referrers, untagged.
func TestWriteOnceStreamsKeepsNativeApplyPlacement(t *testing.T) {
	re := newRecEnv(t)
	streams := newTestRegistry(t)
	streams.unsupported = true
	re.rec.Store.Streams = streams.host + "/delivery/streams"
	re.stage(1)
	re.concluded("ship", 1, delivery.Succeeded)
	re.reconcile()
	recs := re.records()
	if len(recs) != 1 || !slices.Equal(subjectNames(recs[0].Statement), []string{"to", "from", "contract", "anchor"}) {
		t.Fatalf("records %+v", recs)
	}
	if n := re.reg.referrers(t, "apps/payments", re.reg.to); n != 1 {
		t.Fatalf("apply placement referrers = %d", n)
	}
	if n := re.reg.referrers(t, "pactos/payments", re.reg.ctr); n != 1 {
		t.Fatalf("contract placement referrers = %d", n)
	}
	if tags := re.reg.tagsOf(t, "apps/payments"); len(tags) != 0 {
		t.Fatalf("the apply repository was tagged: %v", tags)
	}
	if tags := streams.recordTags(t, "delivery/streams"); len(tags) != 1 {
		t.Fatalf("record tags = %v", tags)
	}
}

func TestRecordManifestCarriesRegistryMode(t *testing.T) {
	t.Run("write-once tags", func(t *testing.T) {
		re := newRecEnv(t)
		re.reg.unsupported = true
		re.stage(1)
		re.concluded("ship", 1, delivery.Succeeded)
		re.reconcile()
		tags := re.reg.recordTags(t, "delivery/streams")
		if len(tags) != 1 {
			t.Fatalf("record tags = %v", tags)
		}
		m := re.reg.manifestAt(t, "delivery/streams", tags[0])
		if got := m.Annotations[deliveryrecord.AnnRegistryMode]; got != "write-once-tags" {
			t.Fatalf("mode annotation = %q", got)
		}
	})
	t.Run("native", func(t *testing.T) {
		re := newRecEnv(t)
		re.stage(1)
		re.concluded("ship", 1, delivery.Succeeded)
		re.reconcile()
		repo, _ := ocireferrer.Open(re.reg.host+"/apps/payments", ocireferrer.Options{})
		desc, _ := repo.Resolve(context.Background(), re.reg.to)
		var got []string
		_ = repo.Referrers(context.Background(), desc, "", func(page []ocispec.Descriptor) error {
			for _, d := range page {
				got = append(got, re.reg.manifestAt(t, "apps/payments", d.Digest.String()).Annotations[deliveryrecord.AnnRegistryMode])
			}
			return nil
		})
		if !slices.Equal(got, []string{"native"}) {
			t.Fatalf("mode annotations = %v", got)
		}
	})
	// Streams without Referrers, the apply host with them: each copy says
	// how it was written.
	t.Run("mixed", func(t *testing.T) {
		re := newRecEnv(t)
		streams := newTestRegistry(t)
		streams.unsupported = true
		re.rec.Store.Streams = streams.host + "/delivery/streams"
		re.stage(1)
		re.concluded("ship", 1, delivery.Succeeded)
		re.reconcile()
		tags := streams.recordTags(t, "delivery/streams")
		if len(tags) != 1 || streams.manifestAt(t, "delivery/streams", tags[0]).Annotations[deliveryrecord.AnnRegistryMode] != "write-once-tags" {
			t.Fatalf("anchor tag %v", tags)
		}
		repo, _ := ocireferrer.Open(re.reg.host+"/apps/payments", ocireferrer.Options{})
		desc, _ := repo.Resolve(context.Background(), re.reg.to)
		var got []string
		_ = repo.Referrers(context.Background(), desc, "", func(page []ocispec.Descriptor) error {
			for _, d := range page {
				got = append(got, re.reg.manifestAt(t, "apps/payments", d.Digest.String()).Annotations[deliveryrecord.AnnRegistryMode])
			}
			return nil
		})
		if !slices.Equal(got, []string{"native"}) {
			t.Fatalf("apply referrer mode annotations = %v", got)
		}
	})
}

func TestRecorderProbeFailureIsRegistryUnavailable(t *testing.T) {
	re := newRecEnv(t)
	re.reg.fault = from(1, http.MethodGet, "/referrers/")
	re.stage(1)
	re.concluded("ship", 1, delivery.Succeeded)
	re.reconcile()
	s := re.getStage()
	if c := condition(s); c.Status != "False" || c.Reason != ReasonRegistryUnavailable {
		t.Fatalf("condition %+v", c)
	}
	if s.Status.Stream.RegistryMode != "" || !controllerutil.ContainsFinalizer(re.getOp("ship"), dv1.RecordFinalizer) {
		t.Fatalf("a failed probe chose a mode or let the operation go: %+v", s.Status.Stream)
	}
}

func TestRecorderRefusesNativeToWriteOnceDowngrade(t *testing.T) {
	re := newRecEnv(t)
	re.reg.unsupported = true
	re.stage(1)
	re.setStage(func(s *delivery.StageStatus) { s.Stream.RegistryMode = "native" })
	re.concluded("ship", 1, delivery.Succeeded)
	re.reconcile()
	s := re.getStage()
	if c := condition(s); c.Status != "False" || c.Reason != ReasonRegistryUnavailable ||
		c.Message != "the registry served the Referrers API for this stream and no longer does" {
		t.Fatalf("condition %+v", c)
	}
	if s.Status.Stream.RegistryMode != "native" || len(re.records()) != 0 {
		t.Fatalf("the downgrade changed the stream: %+v", s.Status.Stream)
	}
}

func TestRecorderUpgradesWriteOnceToNative(t *testing.T) {
	re := newRecEnv(t)
	re.stage(1)
	re.setStage(func(s *delivery.StageStatus) {
		s.Stream.RegistryMode = "write-once-tags"
		s.Stream.Anchor = deliveryrecord.AnchorRef(re.rec.Store.Streams, testStream)
		s.Conditions = []delivery.Condition{{Type: ConditionRecordStoreReady, Status: "True", Reason: ReasonWriteOnceTags,
			Message: "records are written to " + re.rec.Store.Streams + " as write-once tags: the registry serves no Referrers API"}}
	})
	re.concluded("ship", 1, delivery.Succeeded)
	re.reconcile()
	s := re.getStage()
	if c := condition(s); c.Reason != ReasonReferrersSupported || s.Status.Stream.RegistryMode != "native" || len(re.records()) != 1 {
		t.Fatalf("condition %+v stream %+v", c, s.Status.Stream)
	}
}

func TestStoreProbeReportsMode(t *testing.T) {
	ctx := context.Background()
	s, _, _ := testStore(t)
	if caps, err := s.Probe(ctx); err != nil || caps.Referrers != capabilities.ReferrersNative || caps.ArtifactType != capabilities.ArtifactTypeAccepted {
		t.Fatalf("native probe = %+v %v", caps, err)
	}
	s, reg, _ := testStore(t)
	reg.unsupported = true
	if caps, err := s.Probe(ctx); err != nil || caps.Referrers != capabilities.ReferrersWriteOnceTags {
		t.Fatalf("write-once probe = %+v %v", caps, err)
	}
	// A stream that cannot be listed in its mode is not ready.
	reg.fault = from(1, http.MethodGet, "/tags/list")
	if _, err := s.anchor(ctx, "s"); err == nil {
		t.Fatal("an unlistable write-once stream was ready")
	}
}

// TestStorePlacementProbeFailureFails: a probe that cannot tell is an
// error, never a reason to drop the placement for good.
func TestStorePlacementProbeFailureFails(t *testing.T) {
	s, reg, _ := testStore(t)
	repo, _ := remote.NewRepository(reg.host + "/apps/payments")
	repo.PlainHTTP = true
	reg.fault = from(1, http.MethodGet, "/referrers/")
	desc, _ := repo.Resolve(context.Background(), reg.to)
	if p, err := s.native(context.Background(), &place{repo: repo, desc: desc}); p != nil || err == nil {
		t.Fatalf("a placement whose probe failed = %v, %v", p, err)
	}
}

// TestRecorderApplyProbeFailureIsRetried: streams healthy, the apply host's
// probe fails. The record is not written without its version placement:
// the Stage is RegistryUnavailable and the operation keeps its finalizer.
func TestRecorderApplyProbeFailureIsRetried(t *testing.T) {
	re := newRecEnv(t)
	streams := newTestRegistry(t)
	re.rec.Store.Streams = streams.host + "/delivery/streams"
	re.rec.Store.Approvals = streams.host + "/delivery/approvals"
	re.reg.fault = from(1, http.MethodGet, "/referrers/")
	re.stage(1)
	re.concluded("ship", 1, delivery.Succeeded)
	re.reconcile()
	if c := condition(re.getStage()); c.Status != "False" || c.Reason != ReasonRegistryUnavailable {
		t.Fatalf("condition %+v", c)
	}
	if len(re.records()) != 0 || !controllerutil.ContainsFinalizer(re.getOp("ship"), dv1.RecordFinalizer) {
		t.Fatal("the record was written without its version placement")
	}
}

// TestRecorderRefusesLegacyNativeDowngrade: a Stage anchored before the
// mode was recorded has an anchor and no mode, and was native. One answer
// saying write-once tags must not narrow it.
func TestRecorderRefusesLegacyNativeDowngrade(t *testing.T) {
	re := newRecEnv(t)
	re.reg.unsupported = true
	re.stage(1)
	re.setStage(func(s *delivery.StageStatus) {
		s.Stream.Anchor = deliveryrecord.AnchorRef(re.rec.Store.Streams, testStream)
	})
	re.concluded("ship", 1, delivery.Succeeded)
	re.reconcile()
	s := re.getStage()
	if c := condition(s); c.Status != "False" || c.Reason != ReasonRegistryUnavailable || c.Message != errDowngrade.Error() {
		t.Fatalf("condition %+v", c)
	}
	if s.Status.Stream.RegistryMode != "" || len(re.records()) != 0 {
		t.Fatalf("the downgrade changed the stream: %+v", s.Status.Stream)
	}
}
