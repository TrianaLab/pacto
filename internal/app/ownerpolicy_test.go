package app

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
)

// policySnapshot is cluster's snapshot at asOf carrying policy.
func policySnapshot(cluster string, asOf time.Time, policy *delivery.OwnerPolicy) delivery.TargetSnapshot {
	s := ledgerSnapshot(cluster, asOf)
	s.OwnerPolicy = policy
	return s
}

func approvers(names ...string) *delivery.OwnerPolicy {
	p := &delivery.OwnerPolicy{}
	for _, n := range names {
		p.DefaultApprovers = append(p.DefaultApprovers, delivery.SignerRule{Name: n, Issuer: personIssuer, Subject: `^` + n + `@acme\.com$`})
	}
	return p
}

// readOwnerPolicy reads the owner policy beside e's registry with trust.
func (e *snapEnv) readOwnerPolicy(t *testing.T, trust string) (*delivery.OwnerPolicy, string) {
	t.Helper()
	svc := &Service{sys: sysDeps{now: func() time.Time { return snapNow }}}
	return svc.OwnerPolicy(t.Context(), strings.TrimSuffix(e.repo, delivery.TargetSnapshotRepository)+"invoices", trust)
}

func TestOwnerPolicyAgreeingClusters(t *testing.T) {
	e := newSnapEnv(t)
	e.push(t, e.key, snapA, policySnapshot(snapA, snapNow, approvers("admin")))
	e.push(t, e.key, snapB, policySnapshot(snapB, snapNow, approvers("admin")))
	p, why := e.readOwnerPolicy(t, trustFile(t, e.scoped("rec", snapA, snapB)))
	if p == nil || why != "" || p.DefaultApprovers[0].Name != "admin" {
		t.Fatalf("%+v %q", p, why)
	}
}

func TestOwnerPolicyDisagreementIsUnverified(t *testing.T) {
	e := newSnapEnv(t)
	e.push(t, e.key, snapA, policySnapshot(snapA, snapNow, approvers("admin")))
	e.push(t, e.key, snapB, policySnapshot(snapB, snapNow, approvers("admin", "dev")))
	if p, why := e.readOwnerPolicy(t, trustFile(t, e.scoped("rec", snapA, snapB))); p != nil || why != "clusters disagree on the owner policy" {
		t.Fatalf("%+v %q", p, why)
	}
}

func TestOwnerPolicyStaleSnapshotUnused(t *testing.T) {
	e := newSnapEnv(t)
	e.push(t, e.key, snapA, policySnapshot(snapA, snapNow.Add(-16*time.Minute), approvers("admin")))
	if p, why := e.readOwnerPolicy(t, trustFile(t, e.scoped("rec", snapA))); p != nil || !strings.HasPrefix(why, "stale:") {
		t.Fatalf("%+v %q", p, why)
	}
}

func TestOwnerPolicyUnscopedKeyUnused(t *testing.T) {
	e := newSnapEnv(t)
	e.push(t, e.key, snapA, policySnapshot(snapA, snapNow, approvers("admin")))
	bare := filepath.Join(t.TempDir(), "recorder.pub.pem")
	_ = os.WriteFile(bare, []byte(e.pub), 0o600)
	if p, why := e.readOwnerPolicy(t, bare); p != nil || !strings.HasPrefix(why, "unscoped:") {
		t.Fatalf("%+v %q", p, why)
	}
}

func TestOwnerPolicyNoTrustIsUnverified(t *testing.T) {
	e := newSnapEnv(t)
	e.push(t, e.key, snapA, policySnapshot(snapA, snapNow, approvers("admin")))
	if p, why := e.readOwnerPolicy(t, ""); p != nil || !strings.HasPrefix(why, "trust missing") {
		t.Fatalf("%+v %q", p, why)
	}
	if p, why := e.readOwnerPolicy(t, filepath.Join(t.TempDir(), "absent.yaml")); p != nil || !strings.HasPrefix(why, "trust unreadable") {
		t.Fatalf("%+v %q", p, why)
	}
}

func TestOwnerPolicyNoSectionIsUnverified(t *testing.T) {
	e := newSnapEnv(t)
	e.push(t, e.key, snapA, policySnapshot(snapA, snapNow, nil))
	if p, why := e.readOwnerPolicy(t, trustFile(t, e.scoped("rec", snapA))); p != nil || !strings.Contains(why, "carries no owner policy") {
		t.Fatalf("%+v %q", p, why)
	}
}

func TestOwnerPolicySnapshotUnusable(t *testing.T) {
	trustOf := func(t *testing.T, e *snapEnv) string { return trustFile(t, e.scoped("rec", snapA, snapB)) }
	for name, c := range map[string]struct {
		setup func(t *testing.T, e *snapEnv)
		want  string
	}{
		"none": {func(*testing.T, *snapEnv) {}, "no target snapshot was found"},
		"partial": {func(t *testing.T, e *snapEnv) {
			s := policySnapshot(snapA, snapNow, approvers("admin"))
			s.Completeness = fleet.CompletenessPartial
			e.push(t, e.key, snapA, s)
		}, "partial:"},
		"cluster": {func(t *testing.T, e *snapEnv) {
			e.push(t, e.key, snapB, policySnapshot(snapA, snapNow, approvers("admin")))
		}, "names cluster"},
		"verify": {func(t *testing.T, e *snapEnv) {
			other := newSnapEnv(t)
			pushAt(t, e.repo, other.key, snapA, policySnapshot(snapA, snapNow, approvers("admin")))
		}, "does not verify"},
		"fetch": {func(t *testing.T, e *snapEnv) {
			e.push(t, e.key, snapA, policySnapshot(snapA, snapNow, approvers("admin")))
			e.fail.Store("/blobs/")
		}, "could not be read"},
		"list": {func(t *testing.T, e *snapEnv) {
			e.push(t, e.key, snapA, policySnapshot(snapA, snapNow, approvers("admin")))
			e.status.Store(http.StatusInternalServerError)
		}, "could not be read"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newSnapEnv(t)
			trust := trustOf(t, e)
			c.setup(t, e)
			if p, why := e.readOwnerPolicy(t, trust); p != nil || !strings.Contains(why, c.want) {
				t.Fatalf("%+v %q, want %q", p, why, c.want)
			}
		})
	}
}

func TestOwnerPolicyFromPath(t *testing.T) {
	p := filepath.Join(t.TempDir(), "policy.json")
	_ = os.WriteFile(p, []byte(`{"defaultApprovers":[{"name":"admins","issuer":"`+personIssuer+`","subject":"^a@acme\\.com$"}]}`), 0o600)
	svc := &Service{OwnerPolicyPath: p}
	if pol, why := svc.OwnerPolicy(t.Context(), "unused/x", "unused"); pol == nil || why != "" || pol.DefaultApprovers[0].Name != "admins" {
		t.Fatalf("%+v %q", pol, why)
	}
	svc.OwnerPolicyPath = filepath.Join(t.TempDir(), "absent.json")
	if pol, why := svc.OwnerPolicy(t.Context(), "unused/x", ""); pol != nil || why == "" {
		t.Fatalf("%+v %q", pol, why)
	}
}

func TestOwnerPolicyTooManyClusters(t *testing.T) {
	e := newSnapEnv(t)
	desc := e.push(t, e.key, snapA, policySnapshot(snapA, snapNow, approvers("admin")))
	repo, _ := ocireferrer.Open(e.repo, ocireferrer.Options{})
	for i := range delivery.MaxTargetSnapshots {
		if err := repo.Tag(t.Context(), desc, fmt.Sprintf("cluster-z%02d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if p, why := e.readOwnerPolicy(t, trustFile(t, e.scoped("rec", snapA))); p != nil || !strings.Contains(why, "could not all be listed") {
		t.Fatalf("%+v %q", p, why)
	}
}
