package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

// The fake resolver records every call, so the suite can assert not only what
// the catalog concluded but how much work it did to conclude it. Bounds that
// only trimmed an answer would pass an output assertion and fail these.

var testTime = time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)

func fixedClock() time.Time { return testTime }

// cid builds a real digest from a seed, so tests name content by meaning while
// identity stays a validated content address.
func cid(scheme ContentScheme, seed string) ContentID {
	sum := sha256.Sum256([]byte(seed))
	return ContentID{Scheme: scheme, Digest: "sha256:" + hex.EncodeToString(sum[:])}
}

func ociID(seed string) ContentID   { return cid(SchemeOCI, seed) }
func localID(seed string) ContentID { return cid(SchemeLocal, seed) }

func dep(name, ref string) contract.Dependency {
	return contract.Dependency{Name: name, Ref: ref, Required: true, Compatibility: "^1.0.0"}
}

func ct(name, version string, deps ...contract.Dependency) *contract.Contract {
	return &contract.Contract{
		PactoVersion: "1.1",
		Service:      contract.Service{Name: name, Version: version},
		Dependencies: deps,
	}
}

// at names one revision the way a fixture means it: the service that published
// it, and a content identity derived from a seed. Both halves are identity,
// because mirroring publishes one content under two services, so a case that
// varies only the domain says so with inDomain.
func at(name, seed string) RevisionID {
	return RevisionID{Service: ServiceID{Name: name}, Content: ociID(seed)}
}

func atLocal(name, seed string) RevisionID {
	return RevisionID{Service: ServiceID{Name: name}, Content: localID(seed)}
}

func (r RevisionID) inDomain(d string) RevisionID { r.Service.Domain = d; return r }

// rev scripts the resolution that produces one revision. It takes the identity
// whole, so a fixture cannot script a domain its own handle disagrees with.
func rev(id RevisionID, c *contract.Contract) Resolution {
	return Resolution{
		Contract: c, Content: id.Content, Domain: id.Service.Domain,
		ResolvedRef: "reg.example/" + c.Service.Name + "@" + id.Content.Digest,
	}
}

func (r Resolution) withBase(b string) Resolution   { r.Base = b; return r }
func (r Resolution) withoutResolvedRef() Resolution { r.ResolvedRef = ""; return r }

type answer struct {
	res Resolution
	err error
}

type fake struct {
	mu     sync.Mutex
	script map[memoKey][]answer
	calls  []ResolveRequest
}

func newFake() *fake { return &fake{script: map[memoKey][]answer{}} }

// ok scripts a successful answer for a root-level reference (no declaring base).
func (f *fake) ok(ref string, r Resolution) *fake { return f.okFrom("", ref, r) }

// okFrom scripts a successful answer for a reference declared from base.
func (f *fake) okFrom(base, ref string, r Resolution) *fake {
	k := memoKey{Base: base, Ref: ref}
	f.script[k] = append(f.script[k], answer{res: r})
	return f
}

// okUnder scripts an answer that applies only when the declaring contract asked
// under this constraint. It shadows any unqualified answer for the same
// reference, which is how a version-less reference behaves for real.
func (f *fake) okUnder(base, ref, constraint string, r Resolution) *fake {
	k := memoKey{Base: base, Ref: ref, Constraint: constraint}
	f.script[k] = append(f.script[k], answer{res: r})
	return f
}

// fail scripts a sanitized failure.
func (f *fake) fail(ref, code string) *fake { return f.failFrom("", ref, code) }

func (f *fake) failFrom(base, ref, code string) *fake {
	k := memoKey{Base: base, Ref: ref}
	f.script[k] = append(f.script[k], answer{err: &ResolveError{Code: code, Message: "scripted failure"}})
	return f
}

func (f *fake) Resolve(_ context.Context, req ResolveRequest) (Resolution, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req)
	k := memoKey{Base: req.Base, Ref: req.Ref, Constraint: req.Constraint}
	if _, qualified := f.script[k]; !qualified {
		// Most fixtures answer a reference the same way whoever asked, so an
		// unqualified script entry applies under every constraint.
		k.Constraint = ""
	}
	as := f.script[k]
	if len(as) == 0 {
		return Resolution{}, &ResolveError{Code: ReasonNotFound, Message: "no such reference"}
	}
	if len(as) > 1 {
		f.script[k] = as[1:] // a moving tag: the registry's next answer differs
	}
	return as[0].res, as[0].err
}

// errSecretBearing is the kind of raw transport error a real resolver could
// carry: host, path and credential all in the text.
var errSecretBearing = errors.New("get \"https://reg.example/v2/api/manifests/1\": 401 basic auth failed for user deploy:hunter2")

// rawErrorResolver returns that error unwrapped, so the suite can prove the
// catalog reduces it to a category instead of echoing it.
type rawErrorResolver struct{}

func (rawErrorResolver) Resolve(context.Context, ResolveRequest) (Resolution, error) {
	return Resolution{}, errSecretBearing
}

func (f *fake) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fake) countFor(ref string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c.Ref == ref {
			n++
		}
	}
	return n
}

func build(t *testing.T, f *fake, b Bounds, roots ...string) *Catalog {
	t.Helper()
	c, err := Build(context.Background(), Request{Roots: roots, Resolver: f, Bounds: b, Clock: fixedClock})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return c
}

func mustRevision(t *testing.T, c *Catalog, id RevisionID) Revision {
	t.Helper()
	r, ok := c.Revision(id)
	if !ok {
		t.Fatalf("revision %+v is missing from the catalog", id)
	}
	return r
}

func hasPath(r Revision, want Path) bool {
	return slices.ContainsFunc(r.Paths, func(p Path) bool { return comparePath(p, want) == 0 })
}

func hasLimitation(c *Catalog, code string) bool {
	return slices.ContainsFunc(c.Meta().Limitations, func(l Limitation) bool { return l.Code == code })
}

func edgeTargets(c *Catalog, from RevisionID, index int) []RevisionID {
	var out []RevisionID
	for _, e := range c.Edges() {
		if e.Declaration == (DeclarationID{From: from, Index: index}) {
			out = append(out, e.To)
		}
	}
	return out
}
