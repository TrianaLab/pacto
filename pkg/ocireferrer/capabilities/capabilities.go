// Package capabilities is the oras-free shape of a registry probe result: what
// a registry supports and which mode Pacto uses there. It imports only the
// standard library, so evidence consumers and the WASM demo read a probe
// result without linking oras-go; pkg/ocireferrer runs the probe.
package capabilities

import "time"

// ReferrersMode is how Pacto attaches records on a registry.
type ReferrersMode string

// ListingMode is how Pacto lists repositories on a registry.
type ListingMode string

const (
	// ReferrersNative: the registry serves the OCI 1.1 Referrers API.
	ReferrersNative ReferrersMode = "native"
	// ReferrersWriteOnceTags: the registry serves no Referrers API, so records
	// are one write-once, content-addressed tag each.
	ReferrersWriteOnceTags ReferrersMode = "write-once-tags"
	// ListingCatalog: the registry serves _catalog.
	ListingCatalog ListingMode = "catalog"
	// ListingTags: the registry refused _catalog, so repositories are listed by tag.
	ListingTags ListingMode = "tags"
)

// ArtifactType values. Accepted is set only after the caller's own artifact
// push (a manifest with artifactType) succeeded on the host.
const (
	ArtifactTypeAccepted = "accepted"
	ArtifactTypeUnknown  = "unknown"
)

// Capabilities is what one probe of a registry host found.
type Capabilities struct {
	Host      string
	Referrers ReferrersMode
	Listing   ListingMode
	// ArtifactType is ArtifactTypeAccepted when the caller pushed an artifact
	// manifest to this host, else ArtifactTypeUnknown.
	ArtifactType string
	// Auth is "anonymous", "basic", "bearer" or "unknown", from the /v2/ challenge.
	Auth      string
	CheckedAt time.Time
}

// Mode is "referrers=<r> listing=<l>", the string records and rows carry.
func (c Capabilities) Mode() string {
	return "referrers=" + string(c.Referrers) + " listing=" + string(c.Listing)
}

// Evidence is what the probe saw, for example "no Referrers, no _catalog".
func (c Capabilities) Evidence() string {
	ev := "Referrers, "
	if c.Referrers != ReferrersNative {
		ev = "no " + ev
	}
	if c.Listing != ListingCatalog {
		return ev + "no _catalog"
	}
	return ev + "_catalog"
}
