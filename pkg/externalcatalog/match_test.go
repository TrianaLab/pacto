package externalcatalog

import (
	"encoding/json"
	"strings"
	"testing"
)

const credFixture = "postgres://user:pass@db.xyz.rds.amazonaws.com/app?password=x"

func hostsOf(hs []HostPort) []string {
	out := make([]string, 0, len(hs))
	for _, h := range hs {
		out = append(out, h.String())
	}
	return out
}

func wantHosts(t *testing.T, raw string, want ...string) {
	t.Helper()
	got := hostsOf(NormalizeHost(raw))
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("NormalizeHost(%q) = %v, want %v", raw, got, want)
	}
}

func TestNormalizeHostStripsCredentials(t *testing.T) {
	wantHosts(t, credFixture, "db.xyz.rds.amazonaws.com")
	wantHosts(t, "postgres://user:pass@db.xyz.rds.amazonaws.com:5432/app?password=x", "db.xyz.rds.amazonaws.com:5432")
	wantHosts(t, "jdbc:postgresql://pg.corp-legacy.co.uk:5432/db", "pg.corp-legacy.co.uk:5432")
	wantHosts(t, "mongodb://u:p@a.example.com:27017,b.example.com/db?replicaSet=x", "a.example.com:27017", "b.example.com")
	wantHosts(t, "[2001:db8::1]:443")
	wantHosts(t, "10.0.0.5:5432")
	wantHosts(t, "redis.cache.svc.cluster.local:6379")
	wantHosts(t, "redis.cache.cluster.local:6379")
	wantHosts(t, "localhost:8080")
	wantHosts(t, "https://api.stripe.com/v1/charges?key=sk_live_x", "api.stripe.com")
	wantHosts(t, "eyJhbGciOi.eyJzdWIi.SflKxw_x")
	wantHosts(t, "API.Stripe.COM.", "api.stripe.com")
	wantHosts(t, "https://bad host.example.com")
	wantHosts(t, " , ")
	wantHosts(t, "https://"+strings.Repeat("a", 64)+".example.com")
	wantHosts(t, "https://"+strings.Repeat("abcdefgh.", 30)+"com")

	hs := NormalizeHost(credFixture)
	if len(hs) != 1 || hs[0].Port != "" || !hs[0].Scheme {
		t.Fatalf("fixture = %+v", hs)
	}
	if hs := NormalizeHost("db.example.com:5432"); len(hs) != 1 || hs[0].Scheme || hs[0].Port != "5432" {
		t.Fatalf("scheme-less = %+v", hs)
	}
	if hs := NormalizeHost("mongodb://a.example.com,b.example.com"); len(hs) != 2 || !hs[1].Scheme {
		t.Fatalf("seed list keeps the scheme on every host = %+v", hs)
	}
}

func TestNormalizeHostCustomClusterDomain(t *testing.T) {
	wantHosts(t, "orders.shop.svc.corp.local:8080")
	wantHosts(t, "orders.shop.svc")
	wantHosts(t, "http://orders.shop.svc.:8080")
}

func orgCatalog(t *testing.T) *Catalog {
	t.Helper()
	org := mustParse(t, `apiVersion: catalog.pacto.trianalab.io/v1
entries:
  - identity: corp-erp
    class: legacy
    hosts: ["*.corp.example"]
  - identity: exact-erp
    class: legacy
    hosts: ["erp.corp.example"]
    keyPatterns: ["ERP_TOKEN"]
`)
	def := mustParse(t, string(DefaultBytes()))
	return Merge(def, DefaultRef(), org, &Ref{Origin: OriginOrganization, Digest: "sha256:org"})
}

func matchHost(t *testing.T, c *Catalog, raw string) (Match, bool) {
	t.Helper()
	hs := NormalizeHost(raw)
	if len(hs) != 1 {
		t.Fatalf("NormalizeHost(%q) = %v", raw, hs)
	}
	return c.MatchHost(hs[0])
}

func TestMatchHostSuffixBeatsRegistrableDomain(t *testing.T) {
	m, ok := matchHost(t, Default(), "db.xyz.eu-west-1.rds.amazonaws.com")
	if !ok || m.Identity != "aws/rds" || m.Class != ClassManagedCloud || m.Entry != "aws/rds" || m.Confidence != ConfidenceLikely || m.Kind != KindHost {
		t.Fatalf("match = %+v, %v", m, ok)
	}
	m, ok = matchHost(t, Default(), "api.stripe.com:443")
	if !ok || m.Identity != "stripe" || m.Host != "api.stripe.com:443" {
		t.Fatalf("exact = %+v, %v", m, ok)
	}
	// An exact host rule beats a wildcard of the same suffix.
	m, ok = matchHost(t, orgCatalog(t), "erp.corp.example")
	if !ok || m.Identity != "exact-erp" {
		t.Fatalf("exact over wildcard = %+v, %v", m, ok)
	}
	// A wildcard does not match its own apex.
	if m, ok := matchHost(t, orgCatalog(t), "x.corp.example"); !ok || m.Identity != "corp-erp" {
		t.Fatalf("wildcard = %+v, %v", m, ok)
	}
}

func TestMatchHostUnclassifiedByRegistrableDomain(t *testing.T) {
	m, ok := matchHost(t, Default(), "erp.corp-legacy.co.uk")
	if !ok || m.Identity != "host/corp-legacy.co.uk" || m.Class != ClassUnclassified || m.Entry != "" || m.Confidence != ConfidenceLikely {
		t.Fatalf("unclassified = %+v, %v", m, ok)
	}
	for _, raw := range []string{"db1.corp-legacy.example", "payments.default", "orders.shop", "acme.io", "a.b.corp.local"} {
		if m, ok := matchHost(t, Default(), raw); ok {
			t.Errorf("%s matched %+v", raw, m)
		}
	}
	m, ok = matchHost(t, orgCatalog(t), "billing.corp.example")
	if !ok || m.Identity != "corp-erp" || m.Class != ClassLegacy {
		t.Fatalf("org rule = %+v, %v", m, ok)
	}
}

func TestMatchKeyNamePossible(t *testing.T) {
	m, ok := Default().MatchKeyName("stripe_secret_key")
	if !ok || m.Identity != "stripe" || m.Confidence != ConfidencePossible || m.Kind != KindKeyName || m.Host != "" {
		t.Fatalf("key = %+v, %v", m, ok)
	}
	if m, ok := orgCatalog(t).MatchKeyName("ERP_TOKEN"); !ok || m.Identity != "exact-erp" {
		t.Fatalf("exact key = %+v, %v", m, ok)
	}
	if _, ok := orgCatalog(t).MatchKeyName("ERP_TOKEN_2"); ok {
		t.Fatal("exact pattern matched a longer key")
	}
	if _, ok := Default().MatchKeyName("HOME"); ok {
		t.Fatal("HOME matched")
	}
}

func identities(ms []Match) string {
	var s []string
	for _, m := range ms {
		s = append(s, string(m.Identity)+"/"+m.Kind+"/"+m.Confidence)
	}
	return strings.Join(s, ",")
}

func TestClassifyCandidateRule(t *testing.T) {
	c := Default()
	cases := map[[2]string]string{
		{"erp.corp-legacy.co.uk", "ERP_HOST"}:           "host/corp-legacy.co.uk/host/likely",
		{"erp.corp-legacy.co.uk", "ERP_NAME"}:           "",
		{"erp.corp-legacy.co.uk:8080", "ERP_NAME"}:      "host/corp-legacy.co.uk/host/likely",
		{"https://erp.corp-legacy.co.uk", "anything"}:   "host/corp-legacy.co.uk/host/likely",
		{"erp.corp-legacy.co.uk", ""}:                   "",
		{"config.yaml", "CONFIG_FILE"}:                  "",
		{"com.acme.billing.Main", "JAVA_MAIN"}:          "",
		{"orders.shop:8080", "ORDERS_URL"}:              "",
		{"https://api.stripe.com", "PAYMENTS_BASE_URL"}: "stripe/host/likely",
	}
	for in, want := range cases {
		if got := identities(c.Classify(in[0], in[1])); got != want {
			t.Errorf("Classify(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

func TestClassifySecretKeyValueNeverParsed(t *testing.T) {
	c := Default()
	for _, in := range [][2]string{
		{"eyJhbGciOi.eyJzdWIi.SflKxw", "API_TOKEN"},
		{"s3cr3t.v4lue", "password"},
		{"https://erp.corp-legacy.co.uk", "DB_PASSWD"},
		{credFixture, "DATABASE_SECRET"},
	} {
		if got := c.Classify(in[0], in[1]); got != nil {
			t.Errorf("Classify(%q) = %+v", in[1], got)
		}
	}
	got := c.Classify("https://api.stripe.com", "STRIPE_SECRET_KEY")
	if identities(got) != "stripe/keyName/possible" {
		t.Fatalf("sensitive key keeps its key-name match only: %+v", got)
	}
}

func TestClassifyHostWinsOverKeyName(t *testing.T) {
	got := Default().Classify("https://api.stripe.com/v1", "STRIPE_API_BASE")
	if identities(got) != "stripe/host/likely" || got[0].Host != "api.stripe.com" {
		t.Fatalf("got %+v", got)
	}
	// A candidate host that matches nothing falls back to the key name.
	if got := Default().Classify("orders.shop:8080", "STRIPE_URL"); identities(got) != "stripe/keyName/possible" {
		t.Fatalf("fallback = %+v", got)
	}
}

func TestClassifyDedupesStrongest(t *testing.T) {
	got := Default().Classify("https://api.stripe.com,api.stripe.com:443,erp.corp-legacy.co.uk", "STRIPE_HOSTS")
	if identities(got) != "stripe/host/likely,host/corp-legacy.co.uk/host/likely" {
		t.Fatalf("got %+v", got)
	}
	if got := dedupe([]Match{
		{Identity: "stripe", Confidence: ConfidencePossible, Kind: KindKeyName},
		{Identity: "stripe", Confidence: ConfidenceLikely, Kind: KindHost},
		{Identity: "stripe", Confidence: ConfidencePossible, Kind: KindKeyName},
	}); identities(got) != "stripe/host/likely" {
		t.Fatalf("dedupe = %+v", got)
	}
}

func TestClassifyNilCatalog(t *testing.T) {
	var c *Catalog
	if got := c.Classify("https://api.stripe.com", "STRIPE_URL"); got != nil {
		t.Fatalf("got %+v", got)
	}
}

func TestMatchJSONHasNoRawValue(t *testing.T) {
	ms := Default().Classify(credFixture, "DATABASE_URL")
	if len(ms) != 1 {
		t.Fatalf("got %+v", ms)
	}
	b, err := json.Marshal(ms[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"user", "pass", "/app", "password", "?"} {
		if strings.Contains(string(b), bad) {
			t.Errorf("match JSON %s carries %q", b, bad)
		}
	}
	if !strings.Contains(string(b), `"host":"db.xyz.rds.amazonaws.com"`) {
		t.Errorf("match JSON %s lacks the host", b)
	}
}
