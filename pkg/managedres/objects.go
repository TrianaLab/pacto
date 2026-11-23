package managedres

import (
	"cmp"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// The [fleet.RawManagedResource] sources of a cluster object, by the family
// of its API group.
const (
	SourceCrossplane      = "crossplane"
	SourceACK             = "ack"
	SourceConfigConnector = "config-connector"
	SourceASO             = "aso"
)

// The labels Crossplane sets on a resource a claim produced. Anyone who may
// create the resource may set them, so they are a [ClaimCandidate] until
// [VerifiedClaim] accepts them.
const (
	claimNameLabel      = "crossplane.io/claim-name"
	claimNamespaceLabel = "crossplane.io/claim-namespace"
)

// ClaimCandidate is the claim a resource's labels name, trusted only once
// [VerifiedClaim] accepts it. The zero value names none.
type ClaimCandidate struct{ Namespace, Name string }

// ComposedClaim is what one composite says about its claim: the
// spec.claimRef kind, namespace and name, empty when it has none, and the
// uids of the composite's own owners, so a nested composite reaches the
// root.
type ComposedClaim struct {
	Kind, Namespace, Name string
	Owners                []string
}

// maxOwnerDepth bounds the ownerReference chain [VerifiedClaim] walks.
const maxOwnerDepth = 8

// FromObject is the managed resource of obj, an object of the managed kind
// group/kind, classified with cat and observed at now, and the claim its
// labels name. It is false when no catalog row names group/kind. Only the
// identity parts, the connection Secret name, the claim labels and the
// engine are read: the Secret is never followed and no other field is kept.
func FromObject(group, kind string, obj map[string]any, cat *externalcatalog.Catalog, now time.Time) (fleet.RawManagedResource, ClaimCandidate, bool) {
	m, ok := cat.MatchCrossplane(group, kind)
	if !ok {
		return fleet.RawManagedResource{}, ClaimCandidate{}, false
	}
	_, ref, _ := cat.Entry(m.Identity) // ponytail: a match always names an entry of cat
	ns, name := str(obj, "metadata", "namespace"), str(obj, "metadata", "name")
	source, account, region, id := identityParts(group, obj)
	r := fleet.RawManagedResource{Source: source, Entry: m.Entry, Catalog: ref.Digest, Type: group + "/" + kind,
		Namespace: ns, Name: name, ObservedAt: &now}
	identify(&r, m, strings.ToLower(account), strings.ToLower(region), strings.ToLower(id))
	if secret := str(obj, "spec", "writeConnectionSecretToRef", "name"); secret != "" {
		if sns := cmp.Or(str(obj, "spec", "writeConnectionSecretToRef", "namespace"), ns); sns != "" {
			r.ConnectionSecrets = []string{sns + "/" + secret}
		}
	}
	switch value, origin := Engine(obj); origin {
	case EngineObserved:
		r.Engine = value
	case EngineDesired:
		r.EngineRequested = value
	}
	labels, _ := dig(obj, "metadata", "labels").(map[string]any)
	c := ClaimCandidate{Namespace: text(labels[claimNamespaceLabel]), Name: text(labels[claimNameLabel])}
	return r, c, true
}

// identityParts are the source family of group and the account, region and
// id obj names, empty where it names none.
func identityParts(group string, obj map[string]any) (source, account, region, id string) {
	switch {
	case strings.HasSuffix(group, ".services.k8s.aws"):
		arn := str(obj, "status", "ackResourceMetadata", "arn")
		region, account = arnParts(arn)
		return SourceACK, cmp.Or(str(obj, "status", "ackResourceMetadata", "ownerAccountID"), account),
			cmp.Or(str(obj, "status", "ackResourceMetadata", "region"), region), arnID(arn)
	case strings.HasSuffix(group, ".cnrm.cloud.google.com"):
		annotations, _ := dig(obj, "metadata", "annotations").(map[string]any)
		return SourceConfigConnector, text(annotations["cnrm.cloud.google.com/project-id"]),
			cmp.Or(str(obj, "spec", "region"), str(obj, "spec", "location")), cmp.Or(str(obj, "spec", "resourceID"), str(obj, "metadata", "name"))
	case strings.HasSuffix(group, ".azure.com"):
		account, id = azureParts(str(obj, "status", "id"))
		return SourceASO, account, str(obj, "spec", "location"), id
	}
	// A Crossplane provider: the ARN or Azure id it observed, else what it
	// was asked for and the external name.
	arn := str(obj, "status", "atProvider", "arn")
	region, account = arnParts(arn)
	azAccount, azID := azureParts(str(obj, "status", "atProvider", "id"))
	annotations, _ := dig(obj, "metadata", "annotations").(map[string]any)
	return SourceCrossplane, cmp.Or(account, azAccount, str(obj, "spec", "forProvider", "project")),
		cmp.Or(region, str(obj, "spec", "forProvider", "region"), str(obj, "spec", "forProvider", "location")),
		cmp.Or(arnID(arn), azID, text(annotations["crossplane.io/external-name"]))
}

// arnID is the resource id of an AWS ARN, the last ':' or '/' segment of
// its resource part, empty when arn is not one.
func arnID(arn string) string {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 6 || parts[0] != "arn" {
		return ""
	}
	res := parts[5]
	return res[strings.LastIndexAny(res, ":/")+1:]
}

// azureParts are the subscription and the last name of an Azure resource id
// (/subscriptions/<sub>/.../<name>), empty when id is not one.
func azureParts(id string) (subscription, name string) {
	segs := strings.Split(strings.Trim(id, "/"), "/")
	if len(segs) < 3 || !strings.EqualFold(segs[0], "subscriptions") {
		return "", ""
	}
	return segs[1], segs[len(segs)-1]
}

// VerifiedClaim is the claim c of obj, "<Kind> <namespace>/<name>", only
// when obj's ownerReference chain reaches a composite in composites (keyed
// by uid) whose spec.claimRef names that claim and, for a namespaced obj,
// only when the claim is in obj's namespace. Labels anyone may set are
// never enough.
func VerifiedClaim(obj map[string]any, c ClaimCandidate, composites map[string]ComposedClaim) (string, bool) {
	if c.Name == "" || c.Namespace == "" {
		return "", false
	}
	if ns := str(obj, "metadata", "namespace"); ns != "" && ns != c.Namespace {
		return "", false
	}
	owners, seen := ownerUIDs(obj), map[string]bool{}
	for range maxOwnerDepth {
		var next []string
		for _, uid := range owners {
			xr, ok := composites[uid]
			if !ok || seen[uid] {
				continue
			}
			seen[uid] = true
			if xr.Name != "" {
				if xr.Name == c.Name && xr.Namespace == c.Namespace {
					return fleet.ManagedObjectRef(xr.Kind, xr.Namespace, xr.Name), true
				}
				continue
			}
			next = append(next, xr.Owners...)
		}
		owners = next
	}
	return "", false
}

// ownerUIDs are the uids of obj's ownerReferences.
func ownerUIDs(obj map[string]any) []string {
	refs, _ := dig(obj, "metadata", "ownerReferences").([]any)
	var out []string
	for _, ref := range refs {
		if uid := str(ref, "uid"); uid != "" {
			out = append(out, uid)
		}
	}
	return out
}

// FromClaim is the connection Secret, <namespace>/<name>, a claim of kind
// writes to (empty when it names none) and the claim's ref, "<Kind>
// <namespace>/<name>".
func FromClaim(kind string, claim map[string]any) (secret, ref string) {
	ns, name := str(claim, "metadata", "namespace"), str(claim, "metadata", "name")
	if s := str(claim, "spec", "writeConnectionSecretToRef", "name"); s != "" {
		secret = ns + "/" + s
	}
	return secret, fleet.ManagedObjectRef(kind, ns, name)
}

// dig is the value at path in v, nil when any step is missing.
func dig(v any, path ...string) any {
	for _, k := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}

// str is the string at path in v, empty when it is missing or not a string.
func str(v any, path ...string) string { return text(dig(v, path...)) }

// text is v when it is a string, else empty.
func text(v any) string {
	s, _ := v.(string)
	return s
}
