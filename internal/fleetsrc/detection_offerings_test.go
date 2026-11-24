package fleetsrc

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/evidenceingest"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// fakeContributions records what it was asked to read and returns one
// revision per call, with lims.
type fakeContributions struct {
	asked []evidenceingest.ProducerContributor
	lims  []fleet.Limitation
}

func (f *fakeContributions) Read(_ context.Context, _ string, c evidenceingest.ProducerContributor, facts *fleet.ProducerFacts) ([]fleet.RawRevision, []fleet.Limitation) {
	f.asked = append(f.asked, c)
	facts.Repository, facts.Read = c.Repository, 1
	return []fleet.RawRevision{{Contributor: &fleet.Contributor{Producer: c.Producer}}}, f.lims
}

func derKey(t *testing.T, pub any) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(der)
}

// offeringsResult is cluster's detection advertising its own offerings
// repository under ghcr.io/acme, its key pub.
func offeringsResult(t *testing.T, cluster string, pub any) detect.Result {
	asOf := offeringsNow().Add(-time.Minute)
	producer := "idp-" + cluster
	return detect.Result{SchemaVersion: detect.SchemaVersion, AsOf: offeringsNow(), Cluster: cluster, ClusterName: "prod",
		Installation: &detect.Installation{Registry: "ghcr.io/acme", SnapshotKey: derKey(t, pub), SnapshotKeyID: "installation",
			Offerings: &detect.OfferingsAd{Producer: producer, Repository: contract.ContributionRepository("ghcr.io/acme", producer), Observed: []string{"xbucket"}, AsOf: &asOf}}}
}

func collectWith(t *testing.T, res detect.Result, r ContributionReader) *fleet.Collection {
	t.Helper()
	src := NewDetectionResultSource("detection", res, offeringsNow)
	src.Contributions = r
	col, err := src.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return col
}

func TestDetectionOfferingsSetsContributionCluster(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	r := &fakeContributions{}
	col := collectWith(t, offeringsResult(t, "c1", pub), r)
	want := evidenceingest.ProducerContributor{Producer: "idp-c1", Repository: "ghcr.io/acme/pacto-inferred/idp-c1/services",
		Keys: []evidenceingest.ContributorKey{{KeyID: "installation", PublicKey: base64.StdEncoding.EncodeToString(pub)}}}
	if len(r.asked) != 1 || !reflect.DeepEqual(r.asked[0], want) {
		t.Fatalf("asked %+v, want %+v", r.asked, want)
	}
	if len(col.Revisions) != 1 || col.Revisions[0].Contributor.Cluster != "c1" {
		t.Fatalf("revisions = %+v", col.Revisions)
	}
	var row *fleet.Provider
	for i, p := range col.Providers {
		if p.ID == fleet.ProducerRowPrefix+"idp-c1" {
			row = &col.Providers[i]
		}
	}
	if row == nil || row.ClusterID != "c1" || row.Cluster != "prod" {
		t.Fatalf("producer row = %+v", row)
	}
	if len(col.Offerings) != 1 || !reflect.DeepEqual(col.Offerings[0].Observed, []string{"xbucket"}) || col.Offerings[0].Cluster != "c1" {
		t.Fatalf("observations = %+v", col.Offerings)
	}
}

func TestDetectionOfferingsPartialIsLimitation(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	res := offeringsResult(t, "c1", pub)
	res.Installation.Offerings.Partial = "201 source objects, at most 200 read"
	col := collectWith(t, res, &fakeContributions{})
	if len(col.Notes) != 1 || col.Notes[0].Code != fleet.LimitationContributionUnread || !strings.Contains(col.Notes[0].Message, "partial: 201 source objects") {
		t.Fatalf("notes = %+v", col.Notes)
	}
	// Every record was read: the run stopped short, nothing went unread.
	if col.Offerings[0].Partial == "" || col.Offerings[0].Unread != "" {
		t.Fatalf("observation = %+v", col.Offerings[0])
	}
	for _, p := range col.Providers {
		if p.ID == fleet.ProducerRowPrefix+"idp-c1" && p.Status == fleet.SourceAvailable {
			t.Fatalf("a partial producer reads available: %+v", p)
		}
	}
}

func TestDetectionWithoutOfferingsUnchanged(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	res := offeringsResult(t, "c1", pub)
	plain := collectWith(t, res, nil)
	res.Installation.Offerings = nil
	r := &fakeContributions{}
	none := collectWith(t, res, r)
	if len(r.asked) != 0 || len(none.Revisions)+len(none.Offerings)+len(plain.Revisions)+len(plain.Offerings) != 0 {
		t.Fatalf("read without an advertisement or a reader: %+v / %+v", none, plain)
	}
	if !reflect.DeepEqual(none.Providers, plain.Providers) {
		t.Fatalf("rows differ: %+v / %+v", none.Providers, plain.Providers)
	}
}

func TestTwoInstallationsSharingRegistryReadApart(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	r := &fakeContributions{}
	a, b := collectWith(t, offeringsResult(t, "c1", pub), r), collectWith(t, offeringsResult(t, "c2", pub), r)
	if len(r.asked) != 2 || r.asked[0].Repository == r.asked[1].Repository {
		t.Fatalf("asked = %+v", r.asked)
	}
	if len(a.Notes)+len(b.Notes) != 0 || a.Offerings[0].Producer == b.Offerings[0].Producer {
		t.Fatalf("refusals %+v %+v", a.Notes, b.Notes)
	}
}

func TestDetectionOfferingsForeignRepositoryUnread(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	res := offeringsResult(t, "c1", pub)
	// Another installation's repository: never read.
	res.Installation.Offerings.Repository = contract.ContributionRepository("ghcr.io/acme", "idp-c2")
	r := &fakeContributions{}
	col := collectWith(t, res, r)
	if len(r.asked) != 0 || len(col.Notes) != 1 || col.Offerings[0].Unread != "the advertised repository is not this installation's own" {
		t.Fatalf("asked %+v, notes %+v, observations %+v", r.asked, col.Notes, col.Offerings)
	}
}

func TestDetectionOfferingsOtherKeyKindNotOffered(t *testing.T) {
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	for name, key := range map[string]string{"ecdsa": derKey(t, &ec.PublicKey), "not DER": base64.StdEncoding.EncodeToString([]byte("x")), "not base64": "%%"} {
		res := offeringsResult(t, "c1", ed25519.PublicKey(make([]byte, ed25519.PublicKeySize)))
		res.Installation.SnapshotKey = key
		r := &fakeContributions{lims: []fleet.Limitation{{Code: fleet.LimitationContributionUnread, Message: "refused"}}}
		col := collectWith(t, res, r)
		if len(r.asked) != 1 || len(r.asked[0].Keys) != 0 || col.Offerings[0].Unread != "refused" {
			t.Errorf("%s: asked %+v, observations %+v", name, r.asked, col.Offerings)
		}
	}
}

func offeringsNow() time.Time { return time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC) }
