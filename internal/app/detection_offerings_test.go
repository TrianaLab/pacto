package app

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

const offeringCluster = "c1"

// offeringDetection is the detection of an installation of offeringCluster
// whose registry is root, advertising kp as its key and the offerings
// repository of producer idp-c1 there.
func offeringDetection(t *testing.T, root string, kp *signing.Keypair) detect.Result {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(kp.GetPublicKey())
	if err != nil {
		t.Fatal(err)
	}
	asOf := readNow().Add(-time.Minute)
	return detect.Result{SchemaVersion: detect.SchemaVersion, AsOf: readNow(), Cluster: offeringCluster,
		Installation: &detect.Installation{Registry: root, SnapshotKey: base64.StdEncoding.EncodeToString(der), SnapshotKeyID: "installation",
			Offerings: &detect.OfferingsAd{Producer: "idp-" + offeringCluster, Repository: contract.ContributionRepository(root, "idp-"+offeringCluster),
				Observed: []string{"ledger-sync"}, AsOf: &asOf}}}
}

// collectOfferings reads res through a detection source over e's reader.
func collectOfferings(t *testing.T, e *ctbEnv, res detect.Result) *fleet.Collection {
	t.Helper()
	src := fleetsrc.NewDetectionResultSource("detection", res, readNow)
	src.Contributions, src.Freshness = e.reader, 24*time.Hour
	col, err := src.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return col
}

// offeringEnv is e with its repository the installation's own under
// host/acme, holding one record signed by kp.
func offeringEnv(t *testing.T, kp func(*ctbEnv) *signing.Keypair) (*ctbEnv, string) {
	t.Helper()
	e := newCtbEnv(t)
	root := strings.TrimSuffix(e.repo, "/pacto-inferred/my-idp/services")
	e.repo = contract.ContributionRepository(root, "idp-"+offeringCluster)
	e.push(t, "ledger-sync_0.1.0", "ledger-sync", "0.1.0", readNow().Add(-time.Hour).Format(time.RFC3339), kp(e))
	return e, root
}

func TestDetectionOfferingsVerifiedWithInstallationKey(t *testing.T) {
	e, root := offeringEnv(t, func(e *ctbEnv) *signing.Keypair { return e.mine })
	col := collectOfferings(t, e, offeringDetection(t, root, e.mine))
	if len(col.Revisions) != 1 || col.Revisions[0].Contributor.KeyID != "installation" || col.Revisions[0].Contributor.Cluster != offeringCluster {
		t.Fatalf("revisions = %+v", col.Revisions)
	}
	if len(col.Offerings) != 1 || col.Offerings[0].Unread != "" || col.Offerings[0].Producer != "idp-"+offeringCluster {
		t.Fatalf("observations = %+v", col.Offerings)
	}
}

func TestDetectionOfferingsOtherKeyRefused(t *testing.T) {
	e, root := offeringEnv(t, func(e *ctbEnv) *signing.Keypair { return e.other })
	col := collectOfferings(t, e, offeringDetection(t, root, e.mine))
	if len(col.Revisions) != 0 {
		t.Fatalf("a record signed by another key was read: %+v", col.Revisions)
	}
	row := producerRowOf(t, col)
	if row.Status != fleet.SourcePartial || !strings.Contains(row.Limitations[0].Message, "signed by a key not granted") {
		t.Fatalf("row = %+v", row)
	}
}

func TestDetectionOfferingsOutsideRootsUnread(t *testing.T) {
	e, root := offeringEnv(t, func(e *ctbEnv) *signing.Keypair { return e.mine })
	e.reader.Roots, e.reg.heads = []string{"elsewhere.example/acme"}, 0
	col := collectOfferings(t, e, offeringDetection(t, root, e.mine))
	if len(col.Revisions) != 0 || e.reg.heads != 0 {
		t.Fatalf("a repository outside the roots was read: %+v", col.Revisions)
	}
	if len(col.Notes) != 1 || col.Notes[0].Code != fleet.LimitationContributionUnread || col.Offerings[0].Unread == "" {
		t.Fatalf("notes = %+v, observations = %+v", col.Notes, col.Offerings)
	}
}

// producerRowOf is col's one producer row.
func producerRowOf(t *testing.T, col *fleet.Collection) fleet.Provider {
	t.Helper()
	for _, p := range col.Providers {
		if strings.HasPrefix(p.ID, fleet.ProducerRowPrefix) {
			return p
		}
	}
	t.Fatalf("no producer row in %+v", col.Providers)
	return fleet.Provider{}
}

// TestFleetDetectionReadsOfferings: the fleet hands its detection source the
// contribution reader, so the installation's inferred offerings list.
func TestFleetDetectionReadsOfferings(t *testing.T) {
	svc := NewService(nil, nil)
	svc.BundleStore = nil
	src := fleetsrc.NewDetectionResultSource("detection", detect.Result{}, readNow)
	svc.readOfferings(src, FleetOptions{})
	if src.Contributions != nil || src.Freshness != fleet.RecentEvidenceWindow {
		t.Fatalf("source = %+v", src)
	}
	e := newCtbEnv(t)
	svc.BundleStore = e.reader.Store
	svc.readOfferings(src, FleetOptions{Now: readNow, FreshnessWindow: time.Hour})
	if src.Contributions == nil || src.Freshness != time.Hour {
		t.Fatalf("source = %+v", src)
	}
}
