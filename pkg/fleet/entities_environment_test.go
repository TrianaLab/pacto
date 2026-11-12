package fleet

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// phaseEnvQuery is a postgresql offering with three consumers: orders failed
// in production, billing failed in staging, and audit with no report and a
// production target. The provider runs in production too and consumes nothing.
func phaseEnvQuery(t *testing.T) *Query {
	t.Helper()
	base := &Collection{
		Revisions: []RawRevision{provider("postgresql"), consumer("orders", "1.3.0", "postgresql"),
			consumer("billing", "1.0.0", "postgresql"), consumer("audit", "1.0.0", "postgresql")},
		Targets: []RawTarget{
			{Scope: "prod", Kind: "k8s", Name: "pg", Service: "postgresql", Domain: acme, Digest: testDigest(acme, "postgresql", "16.1.0"),
				Compliance: StatusCompliant, Labels: map[string]string{TargetEnvironmentLabel: "production"}},
			{Scope: "prod", Kind: "k8s", Name: "audit", Service: "audit", Domain: acme, Digest: testDigest(acme, "audit", "1.0.0"),
				Compliance: StatusCompliant, Labels: map[string]string{TargetEnvironmentLabel: "production"}},
			{Scope: "prod", Kind: "k8s", Name: "orders", Service: "orders", Domain: acme, Digest: testDigest(acme, "orders", "1.3.0"),
				Compliance: StatusCompliant, Labels: map[string]string{TargetEnvironmentLabel: "production"}},
		},
	}
	billing := func(r *RawProvisioning) {
		r.Service, r.Digest, r.Environment, r.Phase = "billing", testDigest(acme, "billing", "1.0.0"), "staging", "Failed"
	}
	return NewQuery(provisioningSnap(t, 0, base, []RawProvisioning{report(func(r *RawProvisioning) { r.Phase = "Failed" }), report(billing)}))
}

func phaseEnvLabels(t *testing.T, q *Query, f EntityFilter) ([]string, *EntityList) {
	t.Helper()
	l, err := q.Entities(f)
	if err != nil {
		t.Fatalf("Entities(%+v): %v", f, err)
	}
	var out []string
	for _, e := range l.Entities {
		out = append(out, e.Label)
	}
	return out, l
}

// "Failed in production" names only the consumer whose failed status is in
// production, and the answer says what the environment cannot see.
func TestEntitiesPhaseInEnvironment(t *testing.T) {
	q := phaseEnvQuery(t)
	f := EntityFilter{Kinds: []EntityKind{KindService}, Consumes: "postgresql", Phase: PhaseBucketFailed, Environment: "production"}
	got, l := phaseEnvLabels(t, q, f)
	if !slices.Equal(got, []string{"orders"}) {
		t.Errorf("failed in production = %v, want orders", got)
	}
	if !slices.ContainsFunc(l.Meta.Limitations, func(x Limitation) bool {
		return x.Code == LimitationProvisioningEnvironmentScoped && strings.Contains(x.Message, "does not declare production")
	}) {
		t.Errorf("meta limitations = %+v, want the environment-scoped one", l.Meta.Limitations)
	}
	f.Environment = ""
	if got, _ := phaseEnvLabels(t, q, f); !slices.Equal(got, []string{"billing", "orders"}) {
		t.Errorf("failed anywhere = %v, want billing and orders", got)
	}
	f.Environment = "staging"
	if got, _ := phaseEnvLabels(t, q, f); !slices.Equal(got, []string{"billing"}) {
		t.Errorf("failed in staging = %v, want billing", got)
	}
}

// An environment nothing visible declares or runs in gives an empty list that
// says the name is unknown, so "prod" never reads as "nothing failed in prod".
func TestEntitiesPhaseInUnknownEnvironment(t *testing.T) {
	q := phaseEnvQuery(t)
	unknown := func(l *EntityList) bool {
		return slices.ContainsFunc(l.Meta.Limitations, func(x Limitation) bool {
			return x.Code == LimitationProvisioningEnvironmentScoped && strings.Contains(x.Message, "no visible revision declares")
		})
	}
	f := EntityFilter{Kinds: []EntityKind{KindService}, Consumes: "postgresql", Phase: PhaseBucketFailed, Environment: "prod"}
	if got, l := phaseEnvLabels(t, q, f); len(got) != 0 || !unknown(l) {
		t.Errorf("failed in prod = %v, limitations %+v, want none and the name called unknown", got, l.Meta.Limitations)
	}
	f.Phase, f.Environment = PhaseBucketReady, "production"
	if _, l := phaseEnvLabels(t, q, f); unknown(l) {
		t.Errorf("production has targets, so it is known: %+v", l.Meta.Limitations)
	}
	for _, r := range q.snap.Revisions {
		r.Environments = []EnvironmentResult{{Name: "prod"}}
		break
	}
	if !q.environmentKnown("prod") {
		t.Error("a revision declares prod, so it is known")
	}
}

// Under phase unknown, a consumer that runs in the environment with nothing
// tracking it there is listed; one with a status there in another phase is not.
func TestEntitiesUnknownInEnvironmentIncludesUndeclaredRunning(t *testing.T) {
	q := phaseEnvQuery(t)
	got, _ := phaseEnvLabels(t, q, EntityFilter{Kinds: []EntityKind{KindService}, Consumes: "postgresql", Phase: PhaseBucketUnknown, Environment: "production"})
	if !slices.Equal(got, []string{"audit"}) {
		t.Errorf("unknown in production = %v, want audit", got)
	}
}

// On services the environment needs consumes and phase; alone it is an error.
func TestEntitiesEnvironmentServicesNeedPhase(t *testing.T) {
	q := phaseEnvQuery(t)
	for _, f := range []EntityFilter{
		{Kinds: []EntityKind{KindService}, Environment: "production"},
		{Kinds: []EntityKind{KindService, KindOwner}, Consumes: "postgresql", Environment: "production"},
	} {
		var iq *InvalidQueryError
		if _, err := q.Entities(f); !errors.As(err, &iq) || iq.Field != "environment" || !strings.Contains(iq.Reason, "consumes and phase") {
			t.Errorf("%+v = %v, want an environment InvalidQueryError", f, err)
		}
	}
	var iq *InvalidQueryError
	if _, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindOwner}, Environment: "production"}); !errors.As(err, &iq) || iq.Field != "environment" {
		t.Errorf("environment on owners = %v, want InvalidQueryError", err)
	}
}

// Targets filter by their label exactly as before.
func TestEntitiesEnvironmentTargetsUnchanged(t *testing.T) {
	q := phaseEnvQuery(t)
	got, _ := phaseEnvLabels(t, q, EntityFilter{Kinds: []EntityKind{KindTarget}, Environment: "production", Sort: SortName})
	if !slices.Equal(got, []string{"prod/k8s/audit", "prod/k8s/orders", "prod/k8s/pg"}) {
		t.Errorf("production targets = %v", got)
	}
	if got, _ := phaseEnvLabels(t, q, EntityFilter{Kinds: []EntityKind{KindTarget}, Environment: "staging"}); len(got) != 0 {
		t.Errorf("staging targets = %v, want none", got)
	}
}

// An environment alone never lists services or any other kind: with every
// kind, or with services and targets, the answer is the targets there.
func TestEntitiesEnvironmentAllKindsNoPhase(t *testing.T) {
	q := phaseEnvQuery(t)
	for _, kinds := range [][]EntityKind{nil, {KindService, KindTarget}} {
		_, l := phaseEnvLabels(t, q, EntityFilter{Kinds: kinds, Environment: "production"})
		if l.Total != 3 {
			t.Errorf("kinds %v: total %d, want 3", kinds, l.Total)
		}
		for _, e := range l.Entities {
			if e.Kind != KindTarget || e.Environment != "production" {
				t.Errorf("kinds %v: %+v is no production target", kinds, e)
			}
		}
	}
}
