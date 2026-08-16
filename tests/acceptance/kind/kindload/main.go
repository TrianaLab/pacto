// Command kindload imports images into a kind cluster through ONE boundary that
// behaves the same on CI's classic Docker image store and on Docker Desktop's
// containerd image store. tests/acceptance/kind/lib.sh calls it from
// load_images; no scenario calls it, or `kind load`, directly.
//
// Why it exists instead of a bare `kind load docker-image REF`:
//
// `kind load docker-image REF` pipes `docker save REF` into `ctr
// --namespace=k8s.io images import --all-platforms` inside the node. Under the
// containerd image store a pulled multi-platform tag keeps its INDEX identity
// locally while only this host's platform is materialized, so that import asks
// the node's containerd for manifests the host never fetched and dies with
// `ctr: content digest sha256:...: not found` — the digest being ANOTHER
// platform's manifest. kind's "already present?" short-circuit cannot rescue it
// either: `docker image inspect --format {{.Id}}` reports the INDEX digest under
// that store while the node reports the CONFIG digest, so the two identities
// never compare equal and every load re-imports.
//
// A Docker image ID, an index digest, a platform manifest digest and a config
// digest are four different identities. This command keeps them apart:
//
//  1. ask the NODE which platform it runs (not the host, not the OS name),
//  2. export an archive narrowed to that platform when the Docker CLI can do it,
//  3. prove the archive is self-contained BEFORE the node sees it — every
//     descriptor it references must have its content in the archive, which is
//     exactly the condition `--all-platforms` violated,
//  4. `kind load image-archive`, which has no identity short-circuit to get
//     wrong,
//  5. verify through the kubelet's own view (crictl) that every node now has the
//     reference bound to the CONFIG digest of the manifest that was exported.
//
// Step 5 is what lets a scenario write `imagePullPolicy: Never` and mean it: the
// image the pod runs is provably the image this command loaded, not a same-named
// image the node pulled from Docker Hub.
package main

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

func main() {
	cluster := flag.String("cluster", "", "kind cluster name (required)")
	flag.Parse()
	if err := run(*cluster, flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "kindload: "+err.Error())
		os.Exit(1)
	}
}

func run(cluster string, refs []string) error {
	if cluster == "" {
		return errors.New("-cluster is required")
	}
	if len(refs) == 0 {
		return errors.New("no images to load")
	}
	out, err := output("kind", "get", "nodes", "--name", cluster)
	if err != nil {
		return err
	}
	nodes := parseNodes(out)
	if len(nodes) == 0 {
		return fmt.Errorf("kind cluster %q has no nodes", cluster)
	}
	// The node, not the host and not runtime.GOARCH: the node is the container
	// that has to run the image, and it is the only honest answer to "which
	// platform must be present".
	uname, err := output("docker", "exec", nodes[0], "uname", "-m")
	if err != nil {
		return err
	}
	arch, err := parseArch(uname)
	if err != nil {
		return fmt.Errorf("node %s: %w", nodes[0], err)
	}
	plat := "linux/" + arch

	// Capability, not product name: whether THIS docker CLI can narrow an export
	// to one platform. A CLI that cannot is not rejected here — its archives are
	// single-platform anyway on a classic image store, and the self-containment
	// check below is what actually fails closed if they are not.
	help, _ := output("docker", "save", "--help")
	narrow := acceptsPlatform(help)

	for _, ref := range refs {
		if err := loadOne(cluster, nodes, plat, narrow, ref); err != nil {
			return err
		}
	}
	return nil
}

func loadOne(cluster string, nodes []string, plat string, narrow bool, ref string) error {
	f, err := os.CreateTemp("", "kindload-*.tar")
	if err != nil {
		return err
	}
	archive := f.Name()
	_ = f.Close()
	defer func() { _ = os.Remove(archive) }()

	if err := run3("docker", saveArgs(ref, archive, plat, narrow)...); err != nil {
		return fmt.Errorf("docker save %s (platform %s): %w", ref, plat, err)
	}
	a, err := readArchive(archive)
	if err != nil {
		return fmt.Errorf("%s: %w", ref, err)
	}
	if err := a.verifyComplete(); err != nil {
		return fmt.Errorf("%s: %w\n"+
			"    Docker's containerd image store keeps a pulled tag's multi-platform INDEX identity while\n"+
			"    materializing only this host's platform, so `ctr images import --all-platforms` inside the\n"+
			"    kind node cannot resolve the platforms that were never fetched. `docker save --platform`\n"+
			"    narrows the export to the node's platform; this docker CLI %s it.", ref, err, supports(narrow))
	}
	img, err := a.selectImage(plat)
	if err != nil {
		return fmt.Errorf("%s: %w", ref, err)
	}
	cfg, err := a.configDigest(img)
	if err != nil {
		return fmt.Errorf("%s: %w", ref, err)
	}
	if err := run3("kind", "load", "image-archive", archive, "--name", cluster); err != nil {
		return fmt.Errorf("kind load image-archive %s: %w", ref, err)
	}
	for _, node := range nodes {
		seen, err := output("docker", "exec", node, "crictl", "images", "--output", "json")
		if err != nil {
			return fmt.Errorf("%s: reading images on node %s: %w", ref, node, err)
		}
		if err := verifyNode(seen, ref, cfg); err != nil {
			return fmt.Errorf("%s: node %s: %w", ref, node, err)
		}
	}
	fmt.Printf("kindload: %s (%s) present on %d node(s) as %s\n", ref, plat, len(nodes), cfg)
	return nil
}

func supports(narrow bool) string {
	if narrow {
		return "supports"
	}
	return "does NOT support"
}

// --- process boundary --------------------------------------------------------

func output(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(out), nil
}

// run3 streams to the caller's stdio: `docker save` of a dashboard image and
// `kind load` both take long enough that silence reads as a hang.
func run3(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// --- pure helpers ------------------------------------------------------------

func parseNodes(out string) []string {
	var nodes []string
	for line := range strings.SplitSeq(out, "\n") {
		if n := strings.TrimSpace(line); n != "" {
			nodes = append(nodes, n)
		}
	}
	return nodes
}

// parseArch maps `uname -m` inside the node to an OCI architecture.
func parseArch(uname string) (string, error) {
	switch strings.TrimSpace(uname) {
	case "aarch64", "arm64":
		return "arm64", nil
	case "x86_64", "amd64":
		return "amd64", nil
	default:
		return "", fmt.Errorf("unsupported node architecture %q", strings.TrimSpace(uname))
	}
}

// acceptsPlatform reports whether `docker save` takes --platform (Docker CLI
// 28+). Read off the CLI's own help rather than a version comparison, so a
// vendored or repackaged CLI is judged by what it accepts.
func acceptsPlatform(help string) bool { return strings.Contains(help, "--platform") }

func saveArgs(ref, archive, plat string, narrow bool) []string {
	args := []string{"save"}
	if narrow {
		args = append(args, "--platform", plat)
	}
	return append(args, "-o", archive, ref)
}

// --- OCI archive -------------------------------------------------------------

type ociPlatform struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
	Variant      string `json:"variant"`
}

type descriptor struct {
	MediaType   string            `json:"mediaType"`
	Digest      string            `json:"digest"`
	Platform    *ociPlatform      `json:"platform"`
	Annotations map[string]string `json:"annotations"`
}

// blob is the union of the two documents this walks: an index (Manifests) and an
// image manifest (Config/Layers/Subject). Both are small JSON files.
type blob struct {
	MediaType string       `json:"mediaType"`
	Manifests []descriptor `json:"manifests"`
	Config    *descriptor  `json:"config"`
	Layers    []descriptor `json:"layers"`
	Subject   *descriptor  `json:"subject"`
}

type archive struct {
	path    string
	index   []descriptor
	present map[string]bool   // every blob digest the archive carries
	small   map[string][]byte // contents of the small ones (manifests, configs)
}

// smallBlob bounds what readArchive keeps in memory. Manifests and configs are
// kilobytes; layers are the reason this is not "keep everything".
const smallBlob = 1 << 20

func readArchive(path string) (*archive, error) {
	f, err := os.Open(path) //nolint:gosec // a temp file this process just wrote
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	a := &archive{path: path, present: map[string]bool{}, small: map[string][]byte{}}
	var index []byte
	tr := tar.NewReader(f)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg {
			continue // the `blobs/` and `blobs/sha256/` directory entries
		}
		name := strings.TrimPrefix(h.Name, "./")
		switch {
		case name == "index.json":
			if index, err = io.ReadAll(tr); err != nil {
				return nil, err
			}
		case strings.HasPrefix(name, "blobs/"):
			algo, hex, ok := strings.Cut(strings.TrimPrefix(name, "blobs/"), "/")
			if !ok || hex == "" {
				continue
			}
			digest := algo + ":" + hex
			a.present[digest] = true
			if h.Size > 0 && h.Size <= smallBlob {
				if a.small[digest], err = io.ReadAll(tr); err != nil {
					return nil, err
				}
			}
		}
	}
	if index == nil {
		return nil, errors.New("archive has no index.json (docker save must produce an OCI layout)")
	}
	var idx blob
	if err := json.Unmarshal(index, &idx); err != nil {
		return nil, fmt.Errorf("archive index.json: %w", err)
	}
	a.index = idx.Manifests
	return a, nil
}

// verifyComplete proves every descriptor the archive references has its content
// inside the archive. This is the exact condition `ctr images import
// --all-platforms` enforces in the node — checked here, where the diagnostic can
// name the platform, instead of there, where it is a bare missing digest.
func (a *archive) verifyComplete() error {
	seen := map[string]bool{}
	for _, d := range a.index {
		if err := a.walk("index.json", d, seen); err != nil {
			return err
		}
	}
	return nil
}

func (a *archive) walk(from string, d descriptor, seen map[string]bool) error {
	if !a.present[d.Digest] {
		return fmt.Errorf("archive is not self-contained: %s references %s%s but that content was not exported",
			from, d.Digest, platformSuffix(d))
	}
	if seen[d.Digest] {
		return nil
	}
	seen[d.Digest] = true
	if !isJSONDoc(d.MediaType) {
		return nil // a layer: presence is all that can be checked
	}
	b, err := a.read(d)
	if err != nil {
		return err
	}
	children := append([]descriptor{}, b.Manifests...)
	if b.Config != nil {
		children = append(children, *b.Config)
	}
	children = append(children, b.Layers...)
	for _, c := range children {
		if err := a.walk(d.Digest, c, seen); err != nil {
			return err
		}
	}
	return nil
}

// isJSONDoc reports whether a descriptor points at an index or a manifest, the
// two documents whose children must be walked. Both OCI and the Docker v2
// spellings appear in the wild, so both are listed.
func isJSONDoc(mediaType string) bool {
	switch mediaType {
	case "application/vnd.oci.image.index.v1+json",
		"application/vnd.oci.image.manifest.v1+json",
		"application/vnd.docker.distribution.manifest.list.v2+json",
		"application/vnd.docker.distribution.manifest.v2+json":
		return true
	}
	return false
}

func (a *archive) read(d descriptor) (*blob, error) {
	raw, ok := a.small[d.Digest]
	if !ok {
		return nil, fmt.Errorf("%s is a %s but is too large to be one", d.Digest, d.MediaType)
	}
	var b blob
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, fmt.Errorf("%s: %w", d.Digest, err)
	}
	return &b, nil
}

func platformSuffix(d descriptor) string {
	if d.Platform == nil || d.Platform.OS == "" {
		return ""
	}
	p := d.Platform.OS + "/" + d.Platform.Architecture
	if d.Platform.Variant != "" {
		p += "/" + d.Platform.Variant
	}
	return " (" + p + ")"
}

// selectImage picks the runnable image manifest the node will use. Attestations
// are not images; an index entry with no platform is the norm for a
// locally-built single-platform export, so "the only candidate" is as valid an
// answer as "the one whose platform matches".
func (a *archive) selectImage(plat string) (descriptor, error) {
	var candidates []descriptor
	for _, d := range a.index {
		if isAttestation(d) {
			continue
		}
		if d.Platform != nil && d.Platform.OS != "" {
			if d.Platform.OS+"/"+d.Platform.Architecture == plat {
				return d, nil
			}
			continue
		}
		candidates = append(candidates, d)
	}
	switch len(candidates) {
	case 1:
		return candidates[0], nil
	case 0:
		return descriptor{}, fmt.Errorf("archive carries no %s image", plat)
	default:
		return descriptor{}, fmt.Errorf("archive carries %d unplatformed manifests; cannot tell which is the %s image", len(candidates), plat)
	}
}

// isAttestation recognises the non-runnable manifests a modern `docker save`
// exports alongside the image: buildkit attestations ride in the index under a
// placeholder platform, a subject annotation, or a reference-type annotation.
func isAttestation(d descriptor) bool {
	if d.Platform != nil && d.Platform.OS == "unknown" {
		return true
	}
	_, subject := d.Annotations["io.containerd.manifest.subject"]
	_, refType := d.Annotations["vnd.docker.reference.type"]
	return subject || refType
}

// configDigest is the identity the NODE reports for an image: containerd and
// crictl both name an image by its config digest, never by the index or
// manifest digest it was distributed under.
func (a *archive) configDigest(d descriptor) (string, error) {
	b, err := a.read(d)
	if err != nil {
		return "", err
	}
	if b.Config == nil || b.Config.Digest == "" {
		return "", fmt.Errorf("manifest %s has no config descriptor", d.Digest)
	}
	if b.Subject != nil {
		return "", fmt.Errorf("manifest %s is an attestation, not an image", d.Digest)
	}
	return b.Config.Digest, nil
}

// --- in-node verification ----------------------------------------------------

type crictlImage struct {
	ID       string   `json:"id"`
	RepoTags []string `json:"repoTags"`
}

// verifyNode is the claim `imagePullPolicy: Never` rests on: the reference
// resolves, on this node, to the config digest that was exported. A same-named
// image the node pulled itself would carry a different one.
func verifyNode(crictlJSON, ref, wantConfig string) error {
	var payload struct {
		Images []crictlImage `json:"images"`
	}
	if err := json.Unmarshal([]byte(crictlJSON), &payload); err != nil {
		return fmt.Errorf("decoding crictl images: %w", err)
	}
	want := normalizeRef(ref)
	for _, img := range payload.Images {
		for _, tag := range img.RepoTags {
			if tag != ref && tag != want {
				continue
			}
			if img.ID != wantConfig {
				return fmt.Errorf("%s resolves to %s, not the loaded %s — the node is holding a different image under that name", want, img.ID, wantConfig)
			}
			return nil
		}
	}
	return fmt.Errorf("%s is not present after loading it", want)
}

// normalizeRef expands a reference to the fully qualified form containerd and
// crictl report, so `registry:2` can be matched against
// `docker.io/library/registry:2`.
func normalizeRef(ref string) string {
	name, tag := ref, "latest"
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		name, tag = ref[:i], ref[i+1:]
	}
	switch first, _, hasSlash := strings.Cut(name, "/"); {
	case hasSlash && (strings.ContainsAny(first, ".:") || first == "localhost"):
		// already registry-qualified
	case hasSlash:
		name = "docker.io/" + name
	default:
		name = "docker.io/library/" + name
	}
	return name + ":" + tag
}
