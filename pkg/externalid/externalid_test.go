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
