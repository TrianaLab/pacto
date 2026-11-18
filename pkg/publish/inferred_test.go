package publish

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"testing/fstest"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

const deployYAML = "kind: Deployment\nmetadata: {name: payments}\n"

// recordBundle is a 2.5 payments bundle at version publishing only its
// bundle (and image when withImage), holding a record of discovery with
// deploy/app.yaml cited as source and fields as given.
func recordBundle(host, version, discovery, source string, withImage bool, fields ...contract.Inferred) *contract.Bundle {
	y := `pactoVersion: "2.5"
service: {name: payments, version: ` + version + `}
publish:
  bundle: oci://` + host + `/pacto/payments
  sign: {key: cosign.key}
`
	if withImage {
		y += "  artifacts:\n    - {name: api, type: image, ref: \"oci://" + host + "/payments\", build: {command: [x]}}\n"
	}
	sum := sha256.Sum256([]byte(source))
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Inputs: contract.InferredInputs{
		Pacto: "4.6.0", Discovery: discovery,
		Sources: []contract.InferredSourceFile{{Path: "deploy/app.yaml", Digest: "sha256:" + hex.EncodeToString(sum[:])}},
	}, Fields: append([]contract.Inferred{{Field: "workload", Value: "service", Source: "deploy/app.yaml:1", Confidence: contract.ConfidenceLikely}}, fields...)}
	c, err := contract.Parse(strings.NewReader(y))
	if err != nil {
		panic(err)
	}
	return &contract.Bundle{Contract: c, RawYAML: []byte(y), FS: fstest.MapFS{
		"pacto.yaml":                {Data: []byte(y)},
		"deploy/app.yaml":           {Data: []byte(deployYAML)},
		contract.InferredRecordFile: {Data: rec.Marshal()},
	}}
}

// pushBundle tags b as version in host's bundle repository and returns its digest.
func pushBundle(t *testing.T, reg *Client, host, version string, b *contract.Bundle) string {
	t.Helper()
	img, _ := oci.BundleImage(b)
	d, _ := oci.BundleDigest(b)
	h, _ := v1.NewHash(d)
	ctx := context.Background()
	got, err := reg.Write(ctx, host+"/pacto/payments", streamed{img, h})
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Tag(ctx, host+"/pacto/payments", got, version); err != nil {
		t.Fatal(err)
	}
	return got
}

func bundleItem(p Plan) Item { return p.Items[len(p.Items)-1] }

func TestPublishUnchangedWhenOnlyRecordDiffers(t *testing.T) {
	host, reg := newRegistry(t)
	ctx := context.Background()
	remote := pushBundle(t, reg, host, "1.0.0", recordBundle(host, "1.0.0", "0", deployYAML, false))
	tools := newTools()
	tools.signed[host+"/pacto/payments@"+remote] = true
	in := Input{Bundle: recordBundle(host, "1.0.0", "1", deployYAML, false), Revision: "abc123", Checks: []Check{{Name: "validation", Passed: true}}}
	p := MakePlan(ctx, reg, in)
	it := bundleItem(p)
	expect(t, it.State == Adopt && it.Digest == remote && it.Reason == "only the inferred record differs (discovery 0 -> 1): not republished" && p.Ready,
		"plan: %+v ready %v %+v", it, p.Ready, p.Checks)
	res, err := Apply(ctx, reg, tools, in)
	expect(t, err == nil && res.Digests[BundleItem] == remote, "apply: %v %+v", err, res)
	now, _ := reg.Head(ctx, host+"/pacto/payments:1.0.0")
	expect(t, now.Digest == remote && len(tools.builds) == 0 && len(tools.signed) == 1, "tag %s builds %v signed %v", now.Digest, tools.builds, tools.signed)
}

func TestPublishInferredOnlyUnsignedRemoteConflicts(t *testing.T) {
	host, reg := newRegistry(t)
	ctx := context.Background()
	forged := recordBundle(host, "1.0.0", "1", deployYAML, false, contract.Inferred{Field: "dependencies[evil]", Value: "evil", Source: "deploy/app.yaml:2", Confidence: contract.ConfidenceLikely})
	remote := pushBundle(t, reg, host, "1.0.0", forged)
	tools := newTools()
	in := Input{Bundle: recordBundle(host, "1.0.0", "1", deployYAML, false), Revision: "abc123", Checks: []Check{{Name: "validation", Passed: true}}}
	expect(t, bundleItem(MakePlan(ctx, reg, in)).State == Adopt, "the forged record is not a D3 match")
	res, err := Apply(ctx, reg, tools, in)
	it := bundleItem(res.Plan)
	expect(t, err != nil && it.State == Conflict && strings.HasSuffix(it.Reason, NextVersion) && !tools.signed[host+"/pacto/payments@"+remote] && len(tools.signed) == 0,
		"apply: %v %+v signed %v", err, it, tools.signed)
}

func TestPublishInferredOnlyWithArtifactsNotReady(t *testing.T) {
	host, reg := newRegistry(t)
	ctx := context.Background()
	img, _ := random.Image(64, 1)
	d, _ := reg.Write(ctx, host+"/payments", mutate.Annotations(img, map[string]string{AnnotationVersion: "1.0.0", AnnotationRevision: "old"}).(v1.Image))
	_ = reg.Tag(ctx, host+"/payments", d, "1.0.0")
	pushBundle(t, reg, host, "1.0.0", recordBundle(host, "1.0.0", "0", deployYAML, true))
	in := Input{Bundle: recordBundle(host, "1.0.0", "1", deployYAML, true), Revision: "new", Checks: []Check{{Name: "validation", Passed: true}}}
	p := MakePlan(ctx, reg, in)
	expect(t, states(p) == "api=conflict bundle=adopt" && strings.HasPrefix(bundleItem(p).Reason, "only the inferred record differs") && !p.Ready,
		"plan: %s %+v", states(p), p.Items)
}

func TestPublishPatchWhenSourcesDiffer(t *testing.T) {
	host, reg := newRegistry(t)
	ctx := context.Background()
	// A valid remote record that cites one more file, as after a discovery
	// change: the declared bytes match and only the cited set differs.
	remote := recordBundle(host, "1.0.0", "1", deployYAML, false)
	rec, _ := contract.ReadInferredRecord(remote.FS)
	sum := sha256.Sum256(remote.RawYAML)
	rec.Inputs.Sources = append(rec.Inputs.Sources, contract.InferredSourceFile{Path: "pacto.yaml", Digest: "sha256:" + hex.EncodeToString(sum[:])})
	remote.FS.(fstest.MapFS)[contract.InferredRecordFile] = &fstest.MapFile{Data: rec.Marshal()}
	pushBundle(t, reg, host, "1.0.0", remote)
	p := MakePlan(ctx, reg, Input{Bundle: recordBundle(host, "1.0.0", "1", deployYAML, false), Revision: "abc123"})
	it := bundleItem(p)
	expect(t, it.State == Conflict && it.Reason == "only the inferred record changed since 1.0.0, so a patch is enough"+NextVersion, "bundle: %+v", it)
}

// TestPublishForgedRemoteRecordConflicts: a remote record whose digests do
// not match its own bundle is never read as a record-only change.
func TestPublishForgedRemoteRecordConflicts(t *testing.T) {
	host, reg := newRegistry(t)
	ctx := context.Background()
	remote := pushBundle(t, reg, host, "1.0.0", recordBundle(host, "1.0.0", "1", "kind: Job\n", false))
	it := bundleItem(MakePlan(ctx, reg, Input{Bundle: recordBundle(host, "1.0.0", "1", deployYAML, false), Revision: "abc123"}))
	expect(t, it.State == Conflict && it.Reason == "the tag holds "+remote+", which this publish did not compute; it is never overwritten"+NextVersion, "bundle: %+v", it)
}

// TestPublishComparesTheDigestItAdopts: the tag moves between Head and Pull
// to a bundle that matches the local one. The plan compares the manifest Head
// returned, whose declared contract differs, so it stays a conflict.
func TestPublishComparesTheDigestItAdopts(t *testing.T) {
	host, reg := newRegistry(t)
	ctx := context.Background()
	old := recordBundle(host, "1.0.0", "0", deployYAML, false)
	old.Contract.Workload = "job"
	old.FS.(fstest.MapFS)["pacto.yaml"] = &fstest.MapFile{Data: append(old.RawYAML, "workload: job\n"...)}
	oldDigest := pushBundle(t, reg, host, "1.0.0", old)
	fake := recordBundle(host, "1.0.0", "0", deployYAML, false)
	moving := &retagOnHead{Client: reg, t: t, host: host, fake: fake}
	it := bundleItem(MakePlan(ctx, moving, Input{Bundle: recordBundle(host, "1.0.0", "1", deployYAML, false), Revision: "abc123"}))
	expect(t, moving.moved && it.State == Conflict && it.Digest == oldDigest && strings.HasPrefix(it.Reason, "the tag holds "+oldDigest), "bundle: %+v", it)
}

// retagOnHead moves the bundle tag to fake right after answering its Head.
type retagOnHead struct {
	*Client
	t     *testing.T
	host  string
	fake  *contract.Bundle
	moved bool
}

func (r *retagOnHead) Head(ctx context.Context, ref string) (*Remote, error) {
	got, err := r.Client.Head(ctx, ref)
	if !r.moved && strings.HasSuffix(ref, "/pacto/payments:1.0.0") {
		r.moved = true
		pushBundle(r.t, r.Client, r.host, "1.0.0", r.fake)
	}
	return got, err
}

func TestPublishDeclaredChangeStillConflicts(t *testing.T) {
	host, reg := newRegistry(t)
	ctx := context.Background()
	other := recordBundle(host, "1.0.0", "0", deployYAML, false)
	other.Contract.Workload = "job"
	other.FS.(fstest.MapFS)["pacto.yaml"] = &fstest.MapFile{Data: append(other.RawYAML, "workload: job\n"...)}
	remote := pushBundle(t, reg, host, "1.0.0", other)
	it := bundleItem(MakePlan(ctx, reg, Input{Bundle: recordBundle(host, "1.0.0", "1", deployYAML, false), Revision: "abc123"}))
	expect(t, it.State == Conflict && it.Reason == "the tag holds "+remote+", which this publish did not compute; it is never overwritten"+NextVersion, "bundle: %+v", it)
	// A tag that cannot be pulled keeps the conflict too.
	broken := pullFails{reg}
	it = bundleItem(MakePlan(ctx, broken, Input{Bundle: recordBundle(host, "1.0.0", "1", deployYAML, false), Revision: "abc123"}))
	expect(t, it.State == Conflict && strings.HasPrefix(it.Reason, "the tag holds "), "unpullable: %+v", it)
}

// pullFails cannot pull.
type pullFails struct{ *Client }

func (pullFails) Pull(context.Context, string) (*contract.Bundle, error) { return nil, errTest }
