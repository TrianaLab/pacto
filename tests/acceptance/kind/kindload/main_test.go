package main

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- synthetic OCI archives --------------------------------------------------
//
// The archives below are the ones `docker save` really produces, reduced to the
// descriptors that decide the outcome. Building them here rather than shelling
// out to docker is what makes the platform rules testable at all: the failure
// this package exists to prevent needs an archive that references a platform
// whose content was never exported, which no reachable `docker save` will
// produce on demand.

const (
	mtIndex    = "application/vnd.oci.image.index.v1+json"
	mtManifest = "application/vnd.oci.image.manifest.v1+json"
	mtConfig   = "application/vnd.oci.image.config.v1+json"
	mtLayer    = "application/vnd.oci.image.layer.v1.tar+gzip"
	mtInToto   = "application/vnd.in-toto+json"
)

type builder struct {
	t       *testing.T
	blobs   map[string][]byte
	omitted map[string]bool
	index   []descriptor
}

func newBuilder(t *testing.T) *builder {
	t.Helper()
	return &builder{t: t, blobs: map[string][]byte{}, omitted: map[string]bool{}}
}

// put stores a blob and returns a descriptor for it. Nothing here fakes a
// digest: the descriptor carries the sha256 of the bytes actually written, so a
// walk that resolves a child is resolving real content-addressed data.
func (b *builder) put(mediaType string, raw []byte) descriptor {
	b.t.Helper()
	sum := sha256.Sum256(raw)
	d := descriptor{MediaType: mediaType, Digest: "sha256:" + hex.EncodeToString(sum[:])}
	b.blobs[d.Digest] = raw
	return d
}

func (b *builder) putJSON(mediaType string, doc blob) descriptor {
	b.t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		b.t.Fatal(err)
	}
	return b.put(mediaType, raw)
}

// image writes a runnable image manifest (config + one layer) and returns its
// descriptor plus the config digest a node would report for it.
func (b *builder) image(name string) (descriptor, string) {
	b.t.Helper()
	cfg := b.put(mtConfig, []byte(`{"architecture":"`+name+`"}`))
	layer := b.put(mtLayer, []byte("layer-"+name))
	d := b.putJSON(mtManifest, blob{MediaType: mtManifest, Config: &cfg, Layers: []descriptor{layer}})
	return d, cfg.Digest
}

// attestation is the non-runnable manifest buildkit rides alongside an image.
func (b *builder) attestation(subject descriptor) descriptor {
	b.t.Helper()
	cfg := b.put(mtConfig, []byte(`{}`))
	layer := b.put(mtInToto, []byte(`{"_type":"https://in-toto.io/Statement/v0.1"}`))
	d := b.putJSON(mtManifest, blob{MediaType: mtManifest, Config: &cfg, Layers: []descriptor{layer}, Subject: &subject})
	d.Annotations = map[string]string{"io.containerd.manifest.subject": subject.Digest}
	return d
}

// omit drops a blob's CONTENT while every reference to it stays: exactly the
// state Docker's containerd image store is in for a platform it never pulled.
func (b *builder) omit(d descriptor) {
	b.t.Helper()
	delete(b.blobs, d.Digest)
	b.omitted[d.Digest] = true
}

func withPlatform(d descriptor, os, arch, variant string) descriptor {
	d.Platform = &ociPlatform{OS: os, Architecture: arch, Variant: variant}
	return d
}

func (b *builder) write() string {
	b.t.Helper()
	path := filepath.Join(b.t.TempDir(), "image.tar")
	f, err := os.Create(path) //nolint:gosec // t.TempDir
	if err != nil {
		b.t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	tw := tar.NewWriter(f)
	add := func(name string, raw []byte) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(raw))}); err != nil {
			b.t.Fatal(err)
		}
		if _, err := tw.Write(raw); err != nil {
			b.t.Fatal(err)
		}
	}
	if err := tw.WriteHeader(&tar.Header{Name: "blobs/sha256/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		b.t.Fatal(err)
	}
	add("oci-layout", []byte(`{"imageLayoutVersion":"1.0.0"}`))
	idx, err := json.Marshal(blob{MediaType: mtIndex, Manifests: b.index})
	if err != nil {
		b.t.Fatal(err)
	}
	add("index.json", idx)
	for digest, raw := range b.blobs {
		add("blobs/sha256/"+strings.TrimPrefix(digest, "sha256:"), raw)
	}
	if err := tw.Close(); err != nil {
		b.t.Fatal(err)
	}
	return path
}

func read(t *testing.T, path string) *archive {
	t.Helper()
	a, err := readArchive(path)
	if err != nil {
		t.Fatalf("readArchive: %v", err)
	}
	return a
}

// --- the regression ----------------------------------------------------------

// TestPartialMultiPlatformArchiveIsRejected is the defect this package exists
// for. `kind load docker-image registry:2` on Docker Desktop's containerd image
// store streams exactly this archive into `ctr images import --all-platforms`
// in the node: a multi-platform index in which only the host's platform was
// ever materialized. The node answers `ctr: content digest sha256:...: not
// found` and the scenario dies at the image-loading boundary.
//
// The archive must be refused HERE, where the diagnostic can name the platform.
func TestPartialMultiPlatformArchiveIsRejected(t *testing.T) {
	b := newBuilder(t)
	arm, _ := b.image("arm64")
	amd, _ := b.image("amd64")
	amd = withPlatform(amd, "linux", "amd64", "")
	b.omit(amd) // never pulled on an arm64 host
	nested := b.putJSON(mtIndex, blob{MediaType: mtIndex, Manifests: []descriptor{
		withPlatform(arm, "linux", "arm64", "v8"), amd,
	}})
	b.index = []descriptor{nested}

	err := read(t, b.write()).verifyComplete()
	if err == nil {
		t.Fatal("a multi-platform archive missing another platform's manifest was accepted; " +
			"ctr --all-platforms in the node would fail on it")
	}
	for _, want := range []string{"not self-contained", amd.Digest, "(linux/amd64)"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("diagnostic %q does not name %q", err, want)
		}
	}
}

// A layer is content-addressed too: an index whose manifests all resolve but
// whose LAYER content is absent is just as unimportable.
func TestMissingLayerContentIsRejected(t *testing.T) {
	b := newBuilder(t)
	cfg := b.put(mtConfig, []byte(`{}`))
	layer := b.put(mtLayer, []byte("gone"))
	b.omit(layer)
	b.index = []descriptor{b.putJSON(mtManifest, blob{MediaType: mtManifest, Config: &cfg, Layers: []descriptor{layer}})}

	err := read(t, b.write()).verifyComplete()
	if err == nil || !strings.Contains(err.Error(), layer.Digest) {
		t.Fatalf("want the missing layer named, got %v", err)
	}
}

// --- what a good archive must yield -----------------------------------------

// The narrowed export: `docker save --platform linux/arm64 registry:2`. The
// index still carries the attestation manifest, which is not an image and must
// not be mistaken for one.
func TestNarrowedMultiPlatformArchiveSelectsTheNodePlatform(t *testing.T) {
	b := newBuilder(t)
	arm, armCfg := b.image("arm64")
	arm = withPlatform(arm, "linux", "arm64", "v8")
	b.index = []descriptor{arm, b.attestation(arm)}

	a := read(t, b.write())
	if err := a.verifyComplete(); err != nil {
		t.Fatalf("a self-contained archive was rejected: %v", err)
	}
	got, err := a.selectImage("linux/arm64")
	if err != nil {
		t.Fatal(err)
	}
	if got.Digest != arm.Digest {
		t.Fatalf("selected %s, want the arm64 manifest %s", got.Digest, arm.Digest)
	}
	cfg, err := a.configDigest(got)
	if err != nil {
		t.Fatal(err)
	}
	// The config digest, not the manifest digest and not an index digest: it is
	// the only one of the three a node reports back.
	if cfg != armCfg {
		t.Fatalf("config digest %s, want %s", cfg, armCfg)
	}
	if cfg == got.Digest {
		t.Fatal("config digest must not be the manifest digest")
	}
}

// A complete multi-platform archive (every platform present) must still hand the
// node ITS platform, never the first entry.
func TestCompleteMultiPlatformArchiveSelectsTheNodePlatform(t *testing.T) {
	b := newBuilder(t)
	amd, amdCfg := b.image("amd64")
	arm, armCfg := b.image("arm64")
	b.index = []descriptor{
		withPlatform(amd, "linux", "amd64", ""),
		withPlatform(arm, "linux", "arm64", "v8"),
	}
	a := read(t, b.write())
	if err := a.verifyComplete(); err != nil {
		t.Fatalf("a self-contained archive was rejected: %v", err)
	}
	for plat, want := range map[string]string{"linux/arm64": armCfg, "linux/amd64": amdCfg} {
		d, err := a.selectImage(plat)
		if err != nil {
			t.Fatalf("%s: %v", plat, err)
		}
		cfg, err := a.configDigest(d)
		if err != nil {
			t.Fatalf("%s: %v", plat, err)
		}
		if cfg != want {
			t.Errorf("%s selected config %s, want %s", plat, cfg, want)
		}
	}
}

// CI's classic Docker image store, and any locally built image: one manifest,
// no platform in the index descriptor. It must load, not be rejected for
// failing to announce a platform.
func TestSinglePlatformArchiveLoads(t *testing.T) {
	b := newBuilder(t)
	only, cfgDigest := b.image("arm64")
	b.index = []descriptor{only}

	a := read(t, b.write())
	if err := a.verifyComplete(); err != nil {
		t.Fatalf("a single-platform archive was rejected: %v", err)
	}
	for _, plat := range []string{"linux/arm64", "linux/amd64"} {
		d, err := a.selectImage(plat)
		if err != nil {
			t.Fatalf("%s: %v", plat, err)
		}
		cfg, err := a.configDigest(d)
		if err != nil {
			t.Fatalf("%s: %v", plat, err)
		}
		if cfg != cfgDigest {
			t.Errorf("%s: config %s, want %s", plat, cfg, cfgDigest)
		}
	}
}

func TestAmbiguousArchiveFailsClosed(t *testing.T) {
	b := newBuilder(t)
	one, _ := b.image("one")
	two, _ := b.image("two")
	b.index = []descriptor{one, two}

	if _, err := read(t, b.write()).selectImage("linux/arm64"); err == nil {
		t.Fatal("two unplatformed manifests must not silently resolve to one of them")
	}
}

func TestArchiveWithNoNodePlatformFailsClosed(t *testing.T) {
	b := newBuilder(t)
	amd, _ := b.image("amd64")
	b.index = []descriptor{withPlatform(amd, "linux", "amd64", "")}

	_, err := read(t, b.write()).selectImage("linux/arm64")
	if err == nil || !strings.Contains(err.Error(), "linux/arm64") {
		t.Fatalf("want a diagnostic naming the node platform, got %v", err)
	}
}

func TestArchiveWithoutIndexIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.tar")
	f, err := os.Create(path) //nolint:gosec // t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(f)
	raw := []byte(`[{"Config":"c.json","RepoTags":["x:1"],"Layers":[]}]`)
	if err := tw.WriteHeader(&tar.Header{Name: "manifest.json", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(raw))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	if _, err := readArchive(path); err == nil || !strings.Contains(err.Error(), "index.json") {
		t.Fatalf("want a diagnostic about the missing OCI index, got %v", err)
	}
}

// --- the export command ------------------------------------------------------

// The narrowing is a CAPABILITY of the docker CLI in front of us, read off its
// own help. Nothing here may branch on an operating system or a product name.
func TestSaveArgsNarrowOnlyWhenTheCLICanSupportIt(t *testing.T) {
	const help = "  -o, --output string      Write to a file\n      --platform strings   Save only the given platform(s)\n"
	if !acceptsPlatform(help) {
		t.Fatal("a CLI whose help documents --platform must be used with it")
	}
	if acceptsPlatform("  -o, --output string      Write to a file\n") {
		t.Fatal("a CLI without --platform must not be handed it")
	}
	got := strings.Join(saveArgs("registry:2", "/tmp/a.tar", "linux/arm64", true), " ")
	if got != "save --platform linux/arm64 -o /tmp/a.tar registry:2" {
		t.Fatalf("narrowed save args: %q", got)
	}
	// CI's classic image store: a plain save is already single-platform, and
	// the self-containment check is what proves it.
	got = strings.Join(saveArgs("registry:2", "/tmp/a.tar", "linux/amd64", false), " ")
	if got != "save -o /tmp/a.tar registry:2" {
		t.Fatalf("plain save args: %q", got)
	}
}

// --- the node's own view -----------------------------------------------------

const crictlOut = `{"images":[
  {"id":"sha256:aaa","repoTags":["docker.io/library/registry:2"]},
  {"id":"sha256:bbb","repoTags":["registry.k8s.io/pause:3.10"]}
]}`

func TestVerifyNodeAcceptsTheLoadedImage(t *testing.T) {
	if err := verifyNode(crictlOut, "registry:2", "sha256:aaa"); err != nil {
		t.Fatalf("the image that was loaded was not recognised: %v", err)
	}
}

// The point of the whole exercise: a scenario declaring imagePullPolicy: Never
// must not pass because the node happens to hold SOMETHING under that name.
func TestVerifyNodeRejectsADifferentImageUnderTheSameName(t *testing.T) {
	err := verifyNode(crictlOut, "registry:2", "sha256:ccc")
	if err == nil || !strings.Contains(err.Error(), "different image") {
		t.Fatalf("want a same-name/different-content rejection, got %v", err)
	}
}

func TestVerifyNodeRejectsAnAbsentImage(t *testing.T) {
	err := verifyNode(crictlOut, "pacto/operator:5.0.0-e2e", "sha256:aaa")
	if err == nil || !strings.Contains(err.Error(), "not present") {
		t.Fatalf("want an absent-image rejection, got %v", err)
	}
}

func TestVerifyNodeRejectsUnreadableOutput(t *testing.T) {
	if err := verifyNode("not json", "registry:2", "sha256:aaa"); err == nil {
		t.Fatal("unreadable crictl output must not read as success")
	}
}

func TestNormalizeRef(t *testing.T) {
	for in, want := range map[string]string{
		"registry:2":                    "docker.io/library/registry:2",
		"registry":                      "docker.io/library/registry:latest",
		"pacto/operator:5.0.0-e2e":      "docker.io/pacto/operator:5.0.0-e2e",
		"ghcr.io/trianalab/pacto:4.7.0": "ghcr.io/trianalab/pacto:4.7.0",
		"localhost:5000/demo/x:1":       "localhost:5000/demo/x:1",
	} {
		if got := normalizeRef(in); got != want {
			t.Errorf("normalizeRef(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- the node, not the host --------------------------------------------------

func TestParseArch(t *testing.T) {
	for in, want := range map[string]string{"aarch64\n": "arm64", "x86_64\n": "amd64", "arm64": "arm64", "amd64": "amd64"} {
		got, err := parseArch(in)
		if err != nil || got != want {
			t.Errorf("parseArch(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := parseArch("riscv64"); err == nil {
		t.Fatal("an architecture with no OCI spelling must fail closed, not guess")
	}
}

func TestParseNodes(t *testing.T) {
	got := parseNodes("pacto-mono-control-plane\npacto-mono-worker\n\n")
	if len(got) != 2 || got[0] != "pacto-mono-control-plane" || got[1] != "pacto-mono-worker" {
		t.Fatalf("parseNodes = %q", got)
	}
}

func TestRunRejectsIncompleteInvocations(t *testing.T) {
	if err := run("", []string{"registry:2"}); err == nil {
		t.Fatal("a load with no cluster must fail")
	}
	if err := run("pacto-mono", nil); err == nil {
		t.Fatal("a load with no images must fail")
	}
}
