package orgindex

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/semver"
	"github.com/trianalab/pacto/v4/pkg/strictjson"
)

// Read bounds. The tag listing's own page bound belongs to the registry
// client that lists it.
const (
	// MaxServices bounds how many entries one read fetches: one per service.
	MaxServices = 2000
	// Workers is how many entries are fetched at once.
	Workers = 8
)

// Limitation codes of an index read. Each makes the answer partial.
const (
	// LimitationNotFound: no candidate repository holds an index, so the
	// consumers it would name are unknown.
	LimitationNotFound = "CONSUMER_INDEX_NOT_FOUND"
	// LimitationUnavailable: the registry refused or failed the listing.
	LimitationUnavailable = "CONSUMER_INDEX_UNAVAILABLE"
	// LimitationTruncated: a read bound stopped the read before it finished.
	LimitationTruncated = "CONSUMER_INDEX_TRUNCATED"
	// LimitationEntryInvalid: entries that were malformed, unbound or not
	// confirmed by their service's own repository were never used.
	LimitationEntryInvalid = "CONSUMER_INDEX_ENTRY_INVALID"
)

// Provider is the changed service an index read selects consumers of: the
// repository it is published at and its fleet key.
type Provider struct {
	Repo string
	Key  fleet.ServiceKey
}

// Newest maps each service prefix to the tag of its newest entry: the highest
// release, or the highest prerelease when the service has no release. ignored
// counts the tags outside the scheme or with no semver version.
func Newest(tags []string) (newest map[string]string, ignored int) {
	type pick struct {
		tag, version string
		release      bool
	}
	best := map[string]pick{}
	for _, t := range tags {
		prefix, version, ok := ParseTag(t)
		if !ok || semver.Latest([]string{version}) == "" {
			ignored++
			continue
		}
		core, _, _ := strings.Cut(version, "+")
		p := pick{tag: t, version: version, release: !strings.Contains(core, "-")}
		cur, seen := best[prefix]
		if !seen || p.release && !cur.release || p.release == cur.release && semver.Compare(p.version, cur.version) > 0 {
			best[prefix] = p
		}
	}
	newest = make(map[string]string, len(best))
	for prefix, p := range best {
		newest[prefix] = p.tag
	}
	return newest, ignored
}

// Names reports whether e depends on p, the way the fleet resolves a
// dependency: its ref names p's repository, or its name is p's name in e's
// own domain.
func (e Entry) Names(p Provider) bool {
	pDomain, pName := fleet.ParseServiceKey(p.Key)
	eDomain, _ := fleet.ParseServiceKey(fleet.ServiceKey(e.Service))
	for _, d := range e.Dependencies {
		repo, _, _ := contract.SplitOCIRef(contract.TrimOCIScheme(d.Ref))
		if strings.HasPrefix(d.Ref, contract.OCIScheme) && repo == p.Repo || d.Name == pName && eDomain == pDomain {
			return true
		}
	}
	return false
}

// Decode reads one entry layer strictly: no unknown field, the entry schema
// and at most MaxEntryBytes.
func Decode(b []byte) (Entry, error) {
	var e Entry
	if len(b) > MaxEntryBytes {
		return e, refused("the entry is over its size bound", fmt.Sprintf("the index entry is %d bytes, over the %d-byte bound", len(b), MaxEntryBytes))
	}
	if err := strictjson.Unmarshal(b, &e); err != nil {
		return Entry{}, refused("the entry does not decode", "the index entry does not decode: "+err.Error())
	}
	if e.SchemaVersion != SchemaVersion {
		return Entry{}, refused("the entry's schemaVersion is not "+SchemaVersion, "the index entry's schemaVersion is "+e.SchemaVersion+", not "+SchemaVersion)
	}
	return e, nil
}

// EntryLayer is the one layer of the entry manifest rawManifest, refused
// unless the manifest is an index entry whose layer fits [MaxEntryBytes]. It
// only checks: the caller fetches the manifest (bounded by
// [MaxManifestBytes]) and then the layer it returns.
func EntryLayer(rawManifest []byte) (ocispec.Descriptor, error) {
	var m ocispec.Manifest
	_ = json.Unmarshal(rawManifest, &m) // a manifest that does not decode has no artifactType
	switch {
	case m.ArtifactType != ArtifactType:
		return ocispec.Descriptor{}, errors.New("the manifest's artifactType is not " + ArtifactType)
	case len(m.Layers) != 1 || m.Layers[0].MediaType != MediaType:
		return ocispec.Descriptor{}, fmt.Errorf("the manifest is not one %s layer", MediaType)
	case m.Layers[0].Size > MaxEntryBytes:
		return ocispec.Descriptor{}, fmt.Errorf("the entry layer is %d bytes, over the %d-byte bound", m.Layers[0].Size, MaxEntryBytes)
	}
	return m.Layers[0], nil
}
