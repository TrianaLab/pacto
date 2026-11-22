package backstage

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// maxName is the longest name Backstage accepts.
const maxName = 63

var (
	// namePattern is Backstage's rule for metadata.name and the name part of a ref.
	namePattern = regexp.MustCompile(`^[A-Za-z0-9]([-_.A-Za-z0-9]*[A-Za-z0-9])?$`)
	nameInvalid = regexp.MustCompile(`[^-_.A-Za-z0-9]`)
	dashRun     = regexp.MustCompile(`-{2,}`)
)

// suffixLengths are the digest lengths a colliding name escalates through:
// none, 8 hex digits, 16, then the whole digest.
var suffixLengths = []int{0, 8, 16, sha256.Size * 2}

// sha256Hex is the hex SHA-256 of s, the identity hash names are suffixed with.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// sanitize turns base into a Backstage name: every other character becomes
// `-`, runs of `-` collapse and leading and trailing separators go; an empty
// result is `x`.
func sanitize(base string) string {
	s := dashRun.ReplaceAllString(nameInvalid.ReplaceAllString(base, "-"), "-")
	s = strings.Trim(s, "-_.")
	if s == "" {
		return "x"
	}
	return s
}

// entityName is base sanitized and, when n > 0, suffixed with the first n hex
// digits of hash(identity), cutting the base so the name stays at most maxName
// characters. A suffix that leaves no room for the base is the name itself.
func entityName(base, identity string, n int, hash func(string) string) string {
	b := sanitize(base)
	if n == 0 {
		return b
	}
	d := hash(identity)[:n]
	room := maxName - 1 - n
	if room < 1 {
		return d[:min(n, maxName)]
	}
	if len(b) > room {
		b = strings.TrimRight(b[:room], "-_.")
	}
	return b + "-" + d
}

// nameRequest is one entity a kind needs a name for: the base it is named
// after and the identity that tells it apart. force always suffixes it.
type nameRequest struct {
	base, identity string
	force          bool
}

// assignNames names every request of one kind. A name two or more identities
// share is suffixed on EVERY member of the group, so a name never depends on
// source or map order and never moves to another service when a second
// registry joins; names that still collide escalate to a longer suffix as a
// group until they are unique.
func assignNames(reqs []nameRequest, hash func(string) string) []string {
	level := make([]int, len(reqs))
	for i, r := range reqs {
		if r.force || len(sanitize(r.base)) > maxName {
			level[i] = 1
		}
	}
	names := make([]string, len(reqs))
	for {
		for i, r := range reqs {
			names[i] = entityName(r.base, r.identity, suffixLengths[level[i]], hash)
		}
		if !escalate(names, level) {
			return names
		}
	}
}

// escalate raises the suffix level of every name that collides and reports
// whether any rose.
func escalate(names []string, level []int) bool {
	seen := map[string]int{}
	for _, n := range names {
		seen[n]++
	}
	rose := false
	for i, n := range names {
		if seen[n] > 1 && level[i] < len(suffixLengths)-1 {
			level[i]++
			rose = true
		}
	}
	return rose
}

// componentNames names one Component per service, by service name, told apart
// by service key, and one stub Component per caller with no contract. A stub
// is always suffixed with its caller identity, so it never takes a service's
// plain name.
func componentNames(services []*fleet.ServiceRecord, callers []callerGroup) (svc, stubs []string) {
	reqs := make([]nameRequest, 0, len(services)+len(callers))
	for _, s := range services {
		reqs = append(reqs, nameRequest{base: s.Name, identity: string(s.Key)})
	}
	for _, c := range callers {
		reqs = append(reqs, nameRequest{base: c.base(), identity: c.caller, force: true})
	}
	names := assignNames(reqs, sha256Hex)
	return names[:len(services)], names[len(services):]
}

// ownerRef is the Backstage ref of an owner: a team is a Group and a DRI a
// User. It is never sanitized: rewriting would map two different owners to one
// ref and invent an identity. ok is false when the owner has no canonical key
// or its value is not already a valid Backstage name.
func ownerRef(o contract.Owner) (string, bool) {
	k, has := o.Key()
	if !has {
		return "", false
	}
	return ownerKeyRef(k)
}

// ownerKeyRef is the Backstage ref of owner key k, false when its value is no
// valid Backstage name.
func ownerKeyRef(k contract.OwnerKey) (string, bool) {
	if !namePattern.MatchString(k.Value) || len(k.Value) > maxName {
		return "", false
	}
	kind := "group"
	if k.Kind == contract.OwnerKindDRI {
		kind = "user"
	}
	return kind + ":default/" + k.Value, true
}
