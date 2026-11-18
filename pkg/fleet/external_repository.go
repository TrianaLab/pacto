package fleet

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/externalid"
)

// externalKeyRE is the shape of a configuration key an inferred record may
// cite: a URL or a value used as a key never reaches Where.
var externalKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

// recordExternalUses is one use per source of every external dependency the
// inferred record of each service's newest revision names, for
// foldExternalUses: kind repository, the record's revision, the digest of the
// catalog that classified it and every digest the record classified with, so
// a digest the snapshot lacks is a mismatch. A source whose host changes on
// re-normalization, or whose key is not a key name, is dropped and counted in
// one EXTERNAL_USE_UNATTRIBUTED per revision: no message echoes it. A golden
// path consumes nothing, so its record names no use.
func recordExternalUses(snap *FleetSnapshot) []sourcedUse {
	cat := snap.externalCatalog()
	var out []sourcedUse
	for _, sk := range slices.Sorted(maps.Keys(snap.Services)) {
		svc := snap.Services[sk]
		rev := newestRevision(snap, svc)
		if svc.Template || rev == nil || rev.Record == nil {
			continue
		}
		digests := map[string]string{}
		for _, c := range rev.Record.Inputs.Catalog {
			if origin, digest, ok := strings.Cut(c, " "); ok {
				digests[origin] = digest
			}
		}
		all := slices.Sorted(maps.Values(digests))
		dropped := 0
		for _, f := range rev.Record.Fields {
			if f.External == nil {
				continue
			}
			for _, s := range f.External.Sources {
				u, ok := repositoryUse(cat, rev, f, s, digests[f.External.Catalog])
				if !ok {
					dropped++
					continue
				}
				out = append(out, sourcedUse{use: u, source: rev.Source, revision: rev.Key, catalogs: all})
			}
		}
		if dropped > 0 {
			snap.Limitations = append(snap.Limitations, Limitation{Code: LimitationExternalUseUnattributed, Source: rev.Source,
				Message: fmt.Sprintf("the inferred record of revision %s names %d external sources whose host or key is not one; they were dropped", rev.Key, dropped)})
		}
	}
	return out
}

// newestRevision is the newest revision of s that carries a contract, in
// revision chronology; nil when none does.
func newestRevision(snap *FleetSnapshot, s *ServiceRecord) *ContractRevision {
	var newest *ContractRevision
	for _, rk := range s.Revisions {
		if r := snap.Revisions[rk]; r != nil && r.Contract != nil && (newest == nil || lessRevisionChrono(newest, r)) {
			newest = r
		}
	}
	return newest
}

// repositoryUse is the use source s of the external on record field f names,
// classified with the catalog of digest; false when its host or key is not
// one (a hand-edited record never carries a credential into the snapshot).
func repositoryUse(cat *externalcatalog.Catalog, rev *ContractRevision, f contract.Inferred, s contract.InferredExternalSource, digest string) (RawExternalUse, bool) {
	if s.Key != "" && !externalKeyRE.MatchString(s.Key) {
		return RawExternalUse{}, false
	}
	if s.Host != "" {
		if hp := externalcatalog.NormalizeHost(s.Host); len(hp) != 1 || hp[0].String() != s.Host {
			return RawExternalUse{}, false
		}
	}
	where := s.Path
	if s.Line > 0 {
		where += ":" + strconv.Itoa(s.Line)
	}
	if s.Key != "" {
		where += " " + s.Key
	}
	u := RawExternalUse{Service: rev.Service, Domain: rev.Domain, Identity: f.External.Identity, Confidence: s.Confidence(f.Confidence),
		Kind: s.Kind, Where: where, Host: s.Host}
	if f.External.Catalog != "" {
		u.Catalog = digest
		if e, _, ok := cat.Entry(externalid.ID(f.External.Identity)); ok {
			u.Entry = string(e.Identity)
		}
	}
	return u, true
}
