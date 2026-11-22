package memberlink

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

func jwt(payload string) string {
	return "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".sig"
}

func TestTokenFresh(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	exp := func(d time.Duration) string {
		return jwt(`{"exp":` + strconv.FormatInt(now.Add(d).Unix(), 10) + `}`)
	}
	cases := []struct {
		name, token string
		want        error
	}{
		{"fresh", exp(59 * time.Minute), nil},
		{"no exp", jwt(`{"iss":"x"}`), errTokenNoExpiry},
		{"not a jwt", "opaque", errTokenNoExpiry},
		{"bad payload", "a.!!!.c", errTokenNoExpiry},
		{"expired", exp(-time.Second), errTokenExpired},
		{"61m ahead", exp(61 * time.Minute), ErrTokenLifetime},
	}
	for _, c := range cases {
		err := TokenFresh(c.token, now)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
		if err != nil && strings.Contains(err.Error(), c.token) {
			t.Errorf("%s: the error holds the token", c.name)
		}
	}
}

func TestTokenIssuer(t *testing.T) {
	if got := TokenIssuer(jwt(`{"iss":"https://login.acme.dev","exp":1}`)); got != "https://login.acme.dev" {
		t.Fatalf("issuer = %q", got)
	}
	if got := TokenIssuer("opaque"); got != "" {
		t.Fatalf("issuer = %q", got)
	}
}

func TestReviewable(t *testing.T) {
	if !Reviewable("apps", "deployments") || Reviewable("", "secrets") || Reviewable("apps", "secrets") {
		t.Fatal("Reviewable disagrees with ReviewableResources")
	}
}
