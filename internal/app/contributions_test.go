package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"

	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/evidenceingest"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// countingReads is a registry that counts its reads, can list extra
// tags, and fails what its errors say.
type countingReads struct {
	publish.Registry
	extraTags        []string
	tagsErr, headErr error
	heads, referrers int
}

func (c *countingReads) Tags(ctx context.Context, repo string) ([]string, error) {
	if c.tagsErr != nil {
		return nil, c.tagsErr
	}
	var tags []string
	if c.Registry != nil {
		var err error
		if tags, err = c.Registry.Tags(ctx, repo); err != nil {
			return nil, err
		}
	}
	return append(tags, c.extraTags...), nil
}

func (c *countingReads) Head(ctx context.Context, ref string) (*publish.Remote, error) {
	c.heads++
	if c.headErr != nil {
		return nil, c.headErr
	}
	return c.Registry.Head(ctx, ref)
}

func (c *countingReads) Referrers(ctx context.Context, repo, digest, artifactType string) ([]string, error) {
	c.referrers++
	return c.Registry.Referrers(ctx, repo, digest, artifactType)
}

// ctbEnv is an in-memory registry with my-idp's contribution repository
// under host/acme, my-idp's key and another producer's.
type ctbEnv struct {
	repo   string
	reg    *countingReads
	reader *contributionReader
	mine   *signing.Keypair
	other  *signing.Keypair
}

func readNow() time.Time { return time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC) }

func newKeypair(t *testing.T) *signing.Keypair {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	kp, err := signing.FromPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return kp
}

func newCtbEnv(t *testing.T) *ctbEnv {
	t.Helper()
	srv := httptest.NewServer(registry.New(registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	kc := authn.NewMultiKeychain()
	reg := &countingReads{Registry: publish.NewClient(kc, host)}
	e := &ctbEnv{repo: contract.ContributionRepository(host+"/acme", "my-idp"), reg: reg, mine: newKeypair(t), other: newKeypair(t)}
	e.reader = &contributionReader{Store: oci.NewClient(kc, oci.WithInsecureRegistries(host)), Registry: reg, Roots: []string{host, host + "/acme"},
		Now: readNow, Memo: &verifiedMemo{}}
	return e
}

// contributor is my-idp as the Evidence Server advertises it.
func (e *ctbEnv) contributor() evidenceingest.ProducerContributor {
	pub := e.mine.GetPublicKey().(ed25519.PublicKey)
	return evidenceingest.ProducerContributor{Producer: "my-idp", Repository: e.repo,
		Keys: []evidenceingest.ContributorKey{{KeyID: "bad", PublicKey: "not base64"}, {KeyID: "k1", PublicKey: base64.StdEncoding.EncodeToString(pub)}}}
}

// streamedOnce is an image whose streamed layer was read for its digest.
type streamedOnce struct {
	v1.Image
	digest v1.Hash
}

func (s streamedOnce) Digest() (v1.Hash, error) { return s.digest, nil }

// push pushes name@version as tag, created at created ("" for none), signed
// by kp (nil for unsigned), and returns its digest.
func (e *ctbEnv) push(t *testing.T, tag, name, version, created string, kp *signing.Keypair) string {
	t.Helper()
	ctx := context.Background()
	raw := fmt.Sprintf("pactoVersion: \"2.5\"\nservice:\n  name: %s\n  version: %s\n", name, version)
	c, err := contract.Parse(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	b := &contract.Bundle{Contract: c, RawYAML: []byte(raw), FS: fstest.MapFS{"pacto.yaml": {Data: []byte(raw)}}}
	image := func() v1.Image {
		img, _ := oci.BundleImage(b)
		if created != "" {
			img = mutate.Annotations(img, map[string]string{publish.AnnotationCreated: created}).(v1.Image)
		}
		return img
	}
	first := image()
	layers, _ := first.Layers()
	rc, _ := layers[0].Compressed()
	_, _ = io.Copy(io.Discard, rc)
	_ = rc.Close()
	h, _ := first.Digest()
	if _, err := e.reg.Write(ctx, e.repo, streamedOnce{image(), h}); err != nil {
		t.Fatal(err)
	}
	e.tag(t, h.String(), tag)
	if kp != nil {
		if err := publish.SignDigest(ctx, e.reg, signing.Keyed(kp, ""), e.repo, h.String()); err != nil {
			t.Fatal(err)
		}
	}
	return h.String()
}

func (e *ctbEnv) tag(t *testing.T, digest, tag string) {
	t.Helper()
	if err := e.reg.Tag(context.Background(), e.repo, digest, tag); err != nil {
		t.Fatal(err)
	}
}

func (e *ctbEnv) read(c evidenceingest.ProducerContributor) ([]fleet.RawRevision, fleet.ProducerFacts, []fleet.Limitation) {
	f := fleet.ProducerFacts{Producer: c.Producer}
	revs, lims := e.reader.Read(context.Background(), "ev", c, &f)
	return revs, f, lims
}

func hasProblem(f fleet.ProducerFacts, part string) bool {
	for _, p := range f.Problems {
		if strings.Contains(p, part) {
			return true
		}
	}
	return false
}

// TestContributionReaderReadsNewestPerService: the newest signed version of
// each service whose record holds its tag's name and version; a tag that
// replays another digest is refused and counted, and other tags ignored.
func TestContributionReaderReadsNewestPerService(t *testing.T) {
	e := newCtbEnv(t)
	created := readNow().Add(-time.Hour).Format(time.RFC3339)
	old := e.push(t, "ledger-sync_0.1.0", "ledger-sync", "0.1.0", created, e.mine)
	e.push(t, "ledger-sync_0.2.0", "ledger-sync", "0.2.0", created, e.mine)
	e.push(t, "billing-sync_1.0.0", "billing-sync", "1.0.0", created, e.mine)
	e.tag(t, old, "billing-sync_2.0.0")
	e.tag(t, old, "ledger-sync_9.0.0")
	e.reg.extraTags = []string{"junk", "x_1.0.0+build", "Bad_1.0.0"}
	revs, f, lims := e.read(e.contributor())
	if len(lims) != 0 || len(revs) != 2 {
		t.Fatalf("revs = %+v lims = %+v", revs, lims)
	}
	got := e.versions(t, revs)
	if got["ledger-sync"] != "0.2.0" || got["billing-sync"] != "1.0.0" {
		t.Errorf("versions = %v", got)
	}
	if f.Read != 2 || f.Ignored != 3 || !hasProblem(f, "2 tags name another service or version") || f.Repository != e.repo {
		t.Errorf("facts = %+v", f)
	}
}

// versions are the version of each service revs hold, each checked to be
// my-idp's contribution read by key k1.
func (e *ctbEnv) versions(t *testing.T, revs []fleet.RawRevision) map[string]string {
	t.Helper()
	got := map[string]string{}
	for _, r := range revs {
		got[r.Bundle.Contract.Service.Name] = r.Bundle.Contract.Service.Version
		if r.Contributor == nil || r.Contributor.Producer != "my-idp" || r.Contributor.KeyID != "k1" || !r.Contributor.ReadAt.Equal(readNow()) ||
			!strings.HasPrefix(r.ResolvedRef, "oci://"+e.repo+"@sha256:") || r.Domain != contract.OCIDomain(r.ResolvedRef) {
			t.Errorf("revision = %+v %+v", r, r.Contributor)
		}
	}
	return got
}

// TestContributionReaderAsOfFromManifest: AsOf is the signed manifest's
// created time, zero when it carries none.
func TestContributionReaderAsOfFromManifest(t *testing.T) {
	e := newCtbEnv(t)
	made := readNow().Add(-3 * time.Hour)
	e.push(t, "dated_1.0.0", "dated", "1.0.0", made.Format(time.RFC3339), e.mine)
	e.push(t, "undated_1.0.0", "undated", "1.0.0", "", e.mine)
	revs, f, _ := e.read(e.contributor())
	if len(revs) != 2 || !revs[0].Contributor.AsOf.Equal(made) || !revs[1].Contributor.AsOf.IsZero() {
		t.Fatalf("revs = %+v", revs)
	}
	if f.NewestContribution == nil || !f.NewestContribution.Equal(made) {
		t.Errorf("newest = %v", f.NewestContribution)
	}
	if !laterOf(&made, made.Add(-time.Hour)).Equal(made) {
		t.Error("an older time replaced a newer one")
	}
}

// TestContributionReaderRefusesForeignRepository: a repository the reader
// cannot rebuild under one of its roots for the advertised producer is
// never listed or pulled.
func TestContributionReaderRefusesForeignRepository(t *testing.T) {
	e := newCtbEnv(t)
	for name, repo := range map[string]string{
		"another host":     "evil.example/acme/pacto-inferred/my-idp/services",
		"another producer": strings.Replace(e.repo, "/my-idp/", "/other-idp/", 1),
		"no contribution":  strings.TrimSuffix(e.repo, "/pacto-inferred/my-idp/services") + "/ledger",
		"nested inferred":  strings.Replace(e.repo, "/acme/", "/acme/pacto-inferred/x/", 1),
	} {
		c := e.contributor()
		c.Repository = repo
		e.reg.tagsErr = errors.New("listed")
		revs, f, lims := e.read(c)
		if len(revs) != 0 || len(lims) != 1 || lims[0].Code != fleet.LimitationContributionUnread || f.Unread == "" || e.reg.heads != 0 {
			t.Errorf("%s: revs = %+v lims = %+v facts = %+v", name, revs, lims, f)
		}
	}
}

// TestContributionReaderDropsUnsigned: an unsigned record is refused and
// counted; the source is not partial.
func TestContributionReaderDropsUnsigned(t *testing.T) {
	e := newCtbEnv(t)
	e.push(t, "ledger-sync_1.0.0", "ledger-sync", "1.0.0", "", nil)
	revs, f, lims := e.read(e.contributor())
	if len(revs) != 0 || len(lims) != 0 || !hasProblem(f, "1 records were unsigned or signed by a key not granted to my-idp") {
		t.Errorf("revs = %+v facts = %+v lims = %+v", revs, f, lims)
	}
}

// TestContributionReaderDropsForeignKey: a record another producer's key
// signed is refused, and an older one my-idp signed is read instead.
func TestContributionReaderDropsForeignKey(t *testing.T) {
	e := newCtbEnv(t)
	e.push(t, "ledger-sync_1.0.0", "ledger-sync", "1.0.0", "", e.mine)
	e.push(t, "ledger-sync_2.0.0", "ledger-sync", "2.0.0", "", e.other)
	revs, f, lims := e.read(e.contributor())
	if len(revs) != 1 || revs[0].Bundle.Contract.Service.Version != "1.0.0" || len(lims) != 0 || !hasProblem(f, "1 records were unsigned") {
		t.Errorf("revs = %+v facts = %+v lims = %+v", revs, f, lims)
	}
}

// TestContributionReaderBounds: at most maxContributedServices services per
// producer, charged before any read, and at most fleetsrc.MaxContributors producers.
func TestContributionReaderBounds(t *testing.T) {
	reg := &countingReads{headErr: errors.New("connection refused")}
	for i := range maxContributedServices + 1 {
		reg.extraTags = append(reg.extraTags, fmt.Sprintf("svc-%03d_1.0.0", i))
	}
	r := &contributionReader{Registry: reg, Roots: []string{"ghcr.io/acme"}, Now: readNow}
	f := fleet.ProducerFacts{Producer: "my-idp"}
	_, lims := r.Read(context.Background(), "ev", evidenceingest.ProducerContributor{Producer: "my-idp", Repository: contract.ContributionRepository("ghcr.io/acme", "my-idp")}, &f)
	if reg.heads != maxContributedServices || !hasProblem(f, "201 services contributed, at most 200 are read") || len(lims) != 1 ||
		!strings.Contains(lims[0].Message, "200 records could not be read: the source is unavailable") {
		t.Errorf("heads = %d facts = %+v lims = %+v", reg.heads, f, lims)
	}

	var cs []evidenceingest.ProducerContributor
	for i := range fleetsrc.MaxContributors + 1 {
		p := fmt.Sprintf("p%02d", i)
		cs = append(cs, evidenceingest.ProducerContributor{Producer: p, Repository: contract.ContributionRepository("elsewhere.example/x", p)})
	}
	col := collectContributors(t, cs, &contributionReader{Registry: reg, Roots: []string{"ghcr.io/acme"}, Now: readNow})
	if len(col.Limitations) != fleetsrc.MaxContributors+1 || len(col.Providers) != fleetsrc.MaxContributors {
		t.Errorf("limitations = %d rows = %d", len(col.Limitations), len(col.Providers))
	}
}

// TestContributionReaderUnlistedRepoIsPartialSource: a repository the
// registry refuses to list makes the source partial, with a reason that
// echoes no credential.
func TestContributionReaderUnlistedRepoIsPartialSource(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	kc := authn.NewMultiKeychain()
	r := &contributionReader{Store: oci.NewClient(kc, oci.WithInsecureRegistries(host)), Registry: publish.NewClient(kc, host), Roots: []string{host + "/acme"}, Now: readNow}
	c := evidenceingest.ProducerContributor{Producer: "my-idp", Repository: contract.ContributionRepository(host+"/acme", "my-idp")}
	col := collectContributors(t, []evidenceingest.ProducerContributor{c}, r)
	if len(col.Limitations) != 1 || col.Limitations[0].Code != fleet.LimitationContributionUnread || strings.Contains(col.Limitations[0].Message, host) ||
		len(col.Providers) != 1 || col.Providers[0].Status != fleet.SourceUnavailable {
		t.Errorf("limitations = %+v rows = %+v", col.Limitations, col.Providers)
	}
}

// TestContributionReaderNoTagRow: a repository that does not exist yet
// reads as a partial row and an available source.
func TestContributionReaderNoTagRow(t *testing.T) {
	e := newCtbEnv(t)
	col := collectContributors(t, []evidenceingest.ProducerContributor{e.contributor()}, e.reader)
	if len(col.Limitations) != 0 || len(col.Providers) != 1 || col.Providers[0].Status != fleet.SourcePartial ||
		!strings.Contains(col.Providers[0].Limitations[0].Message, "no contribution found at "+e.repo) {
		t.Errorf("limitations = %+v rows = %+v", col.Limitations, col.Providers)
	}
}

// TestContributionReaderMemoisesVerified: a second read verifies nothing it
// already verified.
func TestContributionReaderMemoisesVerified(t *testing.T) {
	e := newCtbEnv(t)
	e.push(t, "ledger-sync_1.0.0", "ledger-sync", "1.0.0", "", e.mine)
	if revs, _, _ := e.read(e.contributor()); len(revs) != 1 {
		t.Fatal(revs)
	}
	before := e.reg.referrers
	if revs, _, _ := e.read(e.contributor()); len(revs) != 1 || e.reg.referrers != before {
		t.Errorf("revs = %d, referrers %d then %d", len(revs), before, e.reg.referrers)
	}
	var m verifiedMemo
	for i := range fleetsrc.MaxContributors*maxContributedServices + 1 {
		m.put(fmt.Sprint(i), "k")
	}
	if _, ok := m.get("0"); ok || len(m.m) != 1 {
		t.Errorf("a full memo is not cleared: %d", len(m.m))
	}
	var none *verifiedMemo
	none.put("a", "b")
	if _, ok := none.get("a"); ok {
		t.Error("a nil memo remembers")
	}
}

// TestContributionReaderStopsOnReadError: a registry error while reading a
// record stops that service, makes the source partial and is counted.
func TestContributionReaderStopsOnReadError(t *testing.T) {
	e := newCtbEnv(t)
	e.push(t, "ledger-sync_1.0.0", "ledger-sync", "1.0.0", "", e.mine)
	e.reg.extraTags = []string{"ghost_1.0.0"}
	revs, f, lims := e.read(e.contributor())
	if len(revs) != 1 || len(lims) != 1 || !hasProblem(f, "1 records could not be read: the source or artifact was not found") {
		t.Errorf("revs = %+v facts = %+v lims = %+v", revs, f, lims)
	}
	e.reader.Store = failingStore{}
	if revs, _, lims := e.read(e.contributor()); len(revs) != 0 || len(lims) != 1 {
		t.Errorf("a failed pull: revs = %+v lims = %+v", revs, lims)
	}
	e.reader.Memo, e.reg.Registry = nil, brokenReferrers{e.reg.Registry}
	if revs, _, lims := e.read(e.contributor()); len(revs) != 0 || len(lims) != 1 {
		t.Errorf("a failed verification: revs = %+v lims = %+v", revs, lims)
	}
}

type failingStore struct{ oci.BundleStore }

func (failingStore) Pull(context.Context, string) (*contract.Bundle, error) {
	return nil, errors.New("connection reset")
}

type brokenReferrers struct{ publish.Registry }

func (brokenReferrers) Referrers(context.Context, string, string, string) ([]string, error) {
	return nil, errors.New("connection reset")
}

// collectContributors collects an Evidence Server advertising cs, read by r.
func collectContributors(t *testing.T, cs []evidenceingest.ProducerContributor, r *contributionReader) *fleet.Collection {
	t.Helper()
	srv := servedEvidence(t, `{"schemaVersion":"pacto.dev/evidence-source/v2","generatedAt":"2026-07-29T12:00:00Z","health":{"status":"ready","subjects":0,"failedSubjects":0,"invalidArtifacts":0},"truncated":false,"targets":[]}`,
		evidenceingest.ProducersResponse{Producers: []string{}, Contributors: cs})
	src := fleetsrc.NewEvidenceHTTPSource("ev", srv.URL)
	src.Contributions, src.Now, src.Freshness = r, readNow, 24*time.Hour
	col, err := src.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return col
}

// servedEvidence serves targets and producers as an Evidence Server.
func servedEvidence(t *testing.T, targets string, producers evidenceingest.ProducersResponse) *httptest.Server {
	t.Helper()
	body, _ := json.Marshal(producers)
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+evidenceingest.TargetsPath, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(targets)) })
	mux.HandleFunc("GET "+evidenceingest.ProducersPath, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}
