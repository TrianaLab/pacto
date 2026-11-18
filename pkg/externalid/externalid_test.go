package externalid

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseAcceptsClasses(t *testing.T) {
	for _, s := range []string{"stripe", "host/corp-legacy.example", "aws/rds/123456789012/eu-west-1/orders-db", "aws/rds"} {
		id, err := Parse(s)
		if err != nil {
			t.Fatalf("Parse(%q): %v", s, err)
		}
		if id.String() != s {
			t.Errorf("String() = %q, want %q", id, s)
		}
	}
	id, _ := Parse("aws/rds/123456789012/eu-west-1/orders-db")
	if got := id.Segments(); !reflect.DeepEqual(got, []string{"aws", "rds", "123456789012", "eu-west-1", "orders-db"}) {
		t.Errorf("Segments() = %v", got)
	}
}

func expectReject(t *testing.T, s, want string) {
	t.Helper()
	if _, err := Parse(s); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("Parse(%q) error = %v, want it to mention %q", s, err, want)
	}
}

func TestParseRejectsUppercase(t *testing.T) { expectReject(t, "Stripe", "lower-case") }

func TestParseRejectsEmptySegment(t *testing.T) {
	expectReject(t, "", "empty")
	expectReject(t, "aws//rds", "empty")
	expectReject(t, "aws/rds/", "empty")
}

func TestParseRejectsSixSegments(t *testing.T) { expectReject(t, "a/b/c/d/e/f", "at most 5") }

func TestParseRejectsHostWithoutDomain(t *testing.T) {
	expectReject(t, "host", "exactly two")
	expectReject(t, "host/a/b", "exactly two")
}

func TestParseRejectsDotDot(t *testing.T) { expectReject(t, "host/a..example", "..") }

func TestParseRejectsReserved(t *testing.T) { expectReject(t, Reserved, "reserved identity") }

func TestParseRejectsTooLong(t *testing.T) {
	seg := strings.Repeat("a", 63)
	expectReject(t, strings.Join([]string{seg, seg, seg, seg, seg}, "/"), "253")
}

func TestParseRejectsBadCharacters(t *testing.T) {
	expectReject(t, "-stripe", "lower-case")
	expectReject(t, "stri pe", "lower-case")
	expectReject(t, strings.Repeat("a", 64), "lower-case")
}

func TestNameNormalizes(t *testing.T) {
	for in, want := range map[string]ID{
		"legacy-mq":         "legacy-mq",
		"Legacy_MQ":         "legacy-mq",
		"  Legacy MQ ":      "legacy-mq",
		"carrier-api/v3":    "carrier-api-v3",
		"_payments.gateway": "payments.gateway",
		"Stripe (EU)!":      "stripe-eu",
	} {
		got, err := Name(in)
		if err != nil || got != want {
			t.Errorf("Name(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestNameRejectsEmpty(t *testing.T) {
	for _, s := range []string{"", "   ", "日本語", "---"} {
		if _, err := Name(s); err == nil {
			t.Errorf("Name(%q) succeeded, want an error", s)
		}
	}
}

func TestNameRejectsTooLong(t *testing.T) {
	if _, err := Name(strings.Repeat("a", 254)); err == nil {
		t.Fatal("a 254-byte name must not normalize to an identity")
	}
}

func TestNameRejectsReserved(t *testing.T) {
	for _, s := range []string{"other-egress", "Other Egress", "OTHER_EGRESS"} {
		if _, err := Name(s); err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Errorf("Name(%q) error = %v, want reserved", s, err)
		}
	}
}

func TestHostRegistrableDomain(t *testing.T) {
	for in, want := range map[string]ID{
		"mq.corp-legacy.co.uk":       "host/corp-legacy.co.uk",
		"API.Stripe.com":             "host/stripe.com",
		"api.stripe.com.":            "host/stripe.com",
		"db.legacy.example.com:5432": "host/example.com",
		"example.com":                "host/example.com",
	} {
		got, ok := Host(in)
		if !ok || got != want {
			t.Errorf("Host(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

func TestHostRejectsPrivateSuffixOnly(t *testing.T) {
	for _, s := range []string{"legacy-mq", "mq.corp.internal", "app.herokuapp.com", "co.uk", "bad_host.example.com", "-a.example.com"} {
		if id, ok := Host(s); ok {
			t.Errorf("Host(%q) = %q, want no host identity", s, id)
		}
	}
}

func TestHostRejectsIP(t *testing.T) {
	for _, s := range []string{"10.0.0.1", "10.0.0.1:5432", "[::1]:80", "::1"} {
		if id, ok := Host(s); ok {
			t.Errorf("Host(%q) = %q, want no host identity", s, id)
		}
	}
}

func TestNameDropsPort(t *testing.T) {
	a, _ := Name("legacy-db.corp.internal:5432")
	b, _ := Name("legacy-db.corp.internal")
	if a != b {
		t.Fatalf("one host with and without a port: %q, %q", a, b)
	}
	x, _ := Name("10.0.0.5:5672")
	y, _ := Name("10.0.0.55:672")
	if x == y {
		t.Fatalf("two IP:port names collapsed into %q", x)
	}
}

func TestManagedBuildsFiveSegments(t *testing.T) {
	id, err := Managed(" AWS ", "SQS", "123456789012", "EU-West-1", "Orders-Queue")
	if err != nil || id != "aws/sqs/123456789012/eu-west-1/orders-queue" {
		t.Fatalf("Managed = %q, %v", id, err)
	}
	if _, err := Managed("aws", "sqs", "123456789012", "eu-west-1", "bad/id"); err == nil {
		t.Fatal("an id with a slash must not parse")
	}
}

func TestManagedRefusesEmptyPart(t *testing.T) {
	for i := range 5 {
		parts := []string{"aws", "sqs", "123456789012", "eu-west-1", "orders"}
		parts[i] = " "
		if _, err := Managed(parts[0], parts[1], parts[2], parts[3], parts[4]); err == nil || !strings.Contains(err.Error(), "provider, service, account, region and id") {
			t.Errorf("empty part %d: %v", i, err)
		}
	}
}
