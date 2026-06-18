package oci

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

// fakeCandidateKeychain is a keychain that also satisfies candidateProvider so
// doWithAuth exercises the fall-through path without any network access.
type fakeCandidateKeychain struct {
	cands []CredSource
	err   error
}

func (k fakeCandidateKeychain) Resolve(authn.Resource) (authn.Authenticator, error) {
	if len(k.cands) == 0 {
		return authn.Anonymous, nil
	}
	return k.cands[0].Authenticator, nil
}

func (k fakeCandidateKeychain) Candidates(authn.Resource) ([]CredSource, error) {
	return k.cands, k.err
}

// scriptedOp returns an op closure that yields the next error from errs on each
// call, and records how many times it was invoked.
func scriptedOp(errs []error, calls *int) func(opts []remote.Option) error {
	return func(_ []remote.Option) error {
		i := *calls
		*calls++
		return errs[i]
	}
}

func testResource(t *testing.T) authn.Resource {
	t.Helper()
	r, err := name.NewRepository("example.com/test/repo", name.Insecure)
	if err != nil {
		t.Fatalf("NewRepository() error: %v", err)
	}
	return r
}

func authError() error { return &transport.Error{StatusCode: 403} }

// TestDoWithAuth_FallThroughToNextSource is the core regression test: the first
// candidate is rejected (403) and the second succeeds.
func TestDoWithAuth_FallThroughToNextSource(t *testing.T) {
	kc := fakeCandidateKeychain{cands: []CredSource{
		{Name: "pacto login", Authenticator: authn.Anonymous},
		{Name: "gh", Authenticator: authn.Anonymous},
	}}
	c := &Client{keychain: kc}

	calls := 0
	op := scriptedOp([]error{authError(), nil}, &calls)
	if err := c.doWithAuth(context.Background(), testResource(t), "example.com/test/repo:v1", op); err != nil {
		t.Fatalf("doWithAuth() error: %v", err)
	}
	if calls != 2 {
		t.Errorf("op calls = %d, want 2 (first rejected, second succeeds)", calls)
	}
}

func TestDoWithAuth_AllRejected(t *testing.T) {
	kc := fakeCandidateKeychain{cands: []CredSource{
		{Name: "pacto login", Authenticator: authn.Anonymous},
		{Name: "gh", Authenticator: authn.Anonymous},
	}}
	c := &Client{keychain: kc}

	calls := 0
	op := scriptedOp([]error{authError(), authError()}, &calls)
	err := c.doWithAuth(context.Background(), testResource(t), "example.com/test/repo:v1", op)
	if err == nil {
		t.Fatal("expected error when all candidates rejected")
	}
	var authErr *AuthenticationError
	if !errors.As(err, &authErr) {
		t.Fatalf("expected AuthenticationError, got %T: %v", err, err)
	}
	wantTried := []string{"pacto login", "gh"}
	if fmt.Sprint(authErr.Tried) != fmt.Sprint(wantTried) {
		t.Errorf("Tried = %v, want %v", authErr.Tried, wantTried)
	}
	if fmt.Sprint(authErr.Rejected) != fmt.Sprint(wantTried) {
		t.Errorf("Rejected = %v, want %v", authErr.Rejected, wantTried)
	}
}

func TestDoWithAuth_NonAuthErrorReturnedImmediately(t *testing.T) {
	kc := fakeCandidateKeychain{cands: []CredSource{
		{Name: "pacto login", Authenticator: authn.Anonymous},
		{Name: "gh", Authenticator: authn.Anonymous},
	}}
	c := &Client{keychain: kc}

	calls := 0
	op := scriptedOp([]error{&transport.Error{StatusCode: 404}, nil}, &calls)
	err := c.doWithAuth(context.Background(), testResource(t), "example.com/test/repo:v1", op)
	if err == nil {
		t.Fatal("expected error for 404")
	}
	var notFound *ArtifactNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("expected ArtifactNotFoundError, got %T: %v", err, err)
	}
	if calls != 1 {
		t.Errorf("op calls = %d, want 1 (non-auth error stops fall-through)", calls)
	}
}

func TestDoWithAuth_PlainErrorReturnedImmediately(t *testing.T) {
	kc := fakeCandidateKeychain{cands: []CredSource{
		{Name: "pacto login", Authenticator: authn.Anonymous},
	}}
	c := &Client{keychain: kc}

	calls := 0
	op := scriptedOp([]error{fmt.Errorf("dial tcp: refused")}, &calls)
	err := c.doWithAuth(context.Background(), testResource(t), "example.com/test/repo:v1", op)
	var unreachable *RegistryUnreachableError
	if !errors.As(err, &unreachable) {
		t.Fatalf("expected RegistryUnreachableError, got %T: %v", err, err)
	}
}

func TestDoWithAuth_EmptyCandidatesAnonymousAttempt(t *testing.T) {
	kc := fakeCandidateKeychain{cands: nil}
	c := &Client{keychain: kc}

	calls := 0
	op := scriptedOp([]error{nil}, &calls)
	if err := c.doWithAuth(context.Background(), testResource(t), "example.com/test/repo:v1", op); err != nil {
		t.Fatalf("doWithAuth() error: %v", err)
	}
	if calls != 1 {
		t.Errorf("op calls = %d, want 1 anonymous attempt", calls)
	}
}

func TestDoWithAuth_EmptyCandidatesAnonymousRejected(t *testing.T) {
	kc := fakeCandidateKeychain{cands: nil}
	c := &Client{keychain: kc}

	calls := 0
	op := scriptedOp([]error{authError()}, &calls)
	err := c.doWithAuth(context.Background(), testResource(t), "example.com/test/repo:v1", op)
	var authErr *AuthenticationError
	if !errors.As(err, &authErr) {
		t.Fatalf("expected AuthenticationError, got %T: %v", err, err)
	}
	if fmt.Sprint(authErr.Rejected) != fmt.Sprint([]string{"anonymous"}) {
		t.Errorf("Rejected = %v, want [anonymous]", authErr.Rejected)
	}
}

func TestDoWithAuth_CandidatesError_FallsBack(t *testing.T) {
	kc := fakeCandidateKeychain{err: fmt.Errorf("resolve boom")}
	c := &Client{keychain: kc}

	calls := 0
	op := scriptedOp([]error{nil}, &calls)
	if err := c.doWithAuth(context.Background(), testResource(t), "example.com/test/repo:v1", op); err != nil {
		t.Fatalf("doWithAuth() error: %v", err)
	}
	if calls != 1 {
		t.Errorf("op calls = %d, want 1 back-compat attempt", calls)
	}
}

func TestDoWithAuth_BackCompatNonProvider(t *testing.T) {
	// authn.DefaultKeychain is not a candidateProvider → single back-compat path.
	c := &Client{keychain: authn.DefaultKeychain}

	calls := 0
	op := scriptedOp([]error{authError()}, &calls)
	err := c.doWithAuth(context.Background(), testResource(t), "example.com/test/repo:v1", op)
	// Back-compat path wraps via wrapRemoteError → AuthenticationError (no Tried/Rejected).
	var authErr *AuthenticationError
	if !errors.As(err, &authErr) {
		t.Fatalf("expected AuthenticationError, got %T: %v", err, err)
	}
	if len(authErr.Tried) != 0 || len(authErr.Rejected) != 0 {
		t.Errorf("back-compat path should not name sources, got Tried=%v Rejected=%v", authErr.Tried, authErr.Rejected)
	}
	if calls != 1 {
		t.Errorf("op calls = %d, want 1 back-compat attempt", calls)
	}
}

func TestDoWithAuth_BackCompatSuccess(t *testing.T) {
	c := &Client{keychain: authn.DefaultKeychain}

	calls := 0
	op := scriptedOp([]error{nil}, &calls)
	if err := c.doWithAuth(context.Background(), testResource(t), "example.com/test/repo:v1", op); err != nil {
		t.Fatalf("doWithAuth() error: %v", err)
	}
}

func TestIsAuthError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"401", &transport.Error{StatusCode: 401}, true},
		{"403", &transport.Error{StatusCode: 403}, true},
		{"404", &transport.Error{StatusCode: 404}, false},
		{"500", &transport.Error{StatusCode: 500}, false},
		{"non-transport", fmt.Errorf("boom"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isAuthError(tt.err); got != tt.want {
				t.Errorf("isAuthError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
