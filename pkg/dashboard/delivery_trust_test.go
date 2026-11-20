package dashboard

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// watching is a Stage of service in ns that watches the registry.
func watching(ns, name, service string, verdicts ...delivery.PublisherVerdict) delivery.StageObject {
	st := stage(ns, name, service, delivery.StageStatus{Publisher: &delivery.PublisherStatus{Verdicts: verdicts}})
	st.Spec.Candidates.Registry = &delivery.RegistryCandidates{}
	return st
}

// TestDeliveryTrustRoute: the route names the services of the Stages the
// caller listed that watch the registry, with the installation's rule, their
// pins and verdicts; a hidden namespace makes it partial and names it, and a
// service filter narrows it.
func TestDeliveryTrustRoute(t *testing.T) {
	id := &delivery.PublisherIdentity{Issuer: "https://token.actions.githubusercontent.com", Subject: "https://github.com/acme/payments/.github/workflows/ci.yml@refs/heads/dev"}
	f := &fakeDelivery{caller: person(), forbidAll: true, forbid: map[string]bool{"secret": true}, stageNS: []string{"prod", "secret"},
		publishers: delivery.PublisherConfig{Registry: "ghcr.io/acme"}, pins: []delivery.IdentityPin{{Subject: "pinned"}},
		stages: []delivery.StageObject{
			watching("prod", "payments-prod", "payments", delivery.PublisherVerdict{Digest: dNext, Verdict: delivery.VerdictNewSigner, Identity: id}),
			watching("prod", "orders-prod", "orders"),
			stage("prod", "flux", "flux-only", delivery.StageStatus{}),
			watching("secret", "hidden-prod", "hidden"),
		}}
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	code, body := call(t, http.MethodGet, base+"/api/delivery/trust", nil)
	services := body["services"].([]any)
	expect(t, code == http.StatusOK && len(services) == 2 && body["ruleSource"] == "rule" && body["rule"] != nil, "trust: %d %v", code, body)
	expect(t, body["completeness"] == "partial" && fmt.Sprint(body["unreadable"]) != "[]", "a refused namespace is named: %v", body["unreadable"])
	pay := page(services[1])
	expect(t, page(pay["service"])["name"] == "payments" && len(pay["pins"].([]any)) == 1 && len(pay["pending"].([]any)) == 1 &&
		len(pay["verdicts"].([]any)) == 1, "payments: %v", pay)
	expect(t, page(body["page"])["total"] == float64(2), "paged: %v", body["page"])

	_, body = call(t, http.MethodGet, base+"/api/delivery/trust?service=hidden", nil)
	expect(t, len(body["services"].([]any)) == 0, "a service the caller lists no Stage of is never named: %v", body)
	_, body = call(t, http.MethodGet, base+"/api/delivery/trust?q=ord", nil)
	expect(t, len(body["services"].([]any)) == 1, "q filters services: %v", body)

	f.trustErr = errors.New("boom")
	code, _ = call(t, http.MethodGet, base+"/api/delivery/trust", nil)
	expect(t, code >= 500, "a trust failure fails the route: %d", code)
	f.listErr = errors.New("down")
	code, _ = call(t, http.MethodGet, base+"/api/delivery/trust", nil)
	expect(t, code >= 500, "a list failure fails the route: %d", code)
	base = startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, errors.New("no client"))
	code, _ = call(t, http.MethodGet, base+"/api/delivery/trust", nil)
	expect(t, code >= 400, "no delivery client: %d", code)
}

// TestDeliveryTrustBounds: 1000 services answer one page of 25, and each
// service's pins, pending signers and verdicts stop at 20.
func TestDeliveryTrustBounds(t *testing.T) {
	var pins []delivery.IdentityPin
	var verdicts []delivery.PublisherVerdict
	for i := range 30 {
		pins = append(pins, delivery.IdentityPin{Subject: fmt.Sprint(i)})
		id := &delivery.PublisherIdentity{Subject: fmt.Sprint(i)}
		verdicts = append(verdicts, delivery.PublisherVerdict{Digest: fmt.Sprint(i), Verdict: delivery.VerdictNewSigner, Identity: id})
	}
	f := &fakeDelivery{caller: person(), pins: pins}
	for i := range 1000 {
		f.stages = append(f.stages, watching("prod", fmt.Sprintf("s%04d", i), fmt.Sprintf("svc%04d", i), verdicts...))
	}
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	_, body := call(t, http.MethodGet, base+"/api/delivery/trust", nil)
	services := body["services"].([]any)
	expect(t, len(services) == 25 && page(body["page"])["total"] == float64(1000) && page(body["page"])["nextOffset"] == float64(25), "one page: %d %v", len(services), body["page"])
	s := page(services[0])
	expect(t, len(s["pins"].([]any)) == 20 && len(s["pending"].([]any)) == 20 && len(s["verdicts"].([]any)) == 20, "capped: %v", s)
}
