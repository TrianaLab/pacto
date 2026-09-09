package tui

import (
	"strings"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// Selection is what a verb runs against: an entity plus the argument the CLI
// would have been given for it. Ref is empty for entities that are not backed
// by a bundle (owners and sources), and the verb layer uses that to decide what
// it can offer. Local distinguishes file:// bundles from registry ones.
type Selection struct {
	Kind    fleet.EntityKind
	Key     string
	Label   string
	Ref     string
	Local   bool
	Version string
}

// bundleRef turns a revision's identity into the argument the app-layer
// resolver takes, plus a local flag. A local bundle is recorded by
// internal/fleetsrc as "file://<dir>" and the resolver wants the bare directory;
// everything else is an OCI reference the same resolver accepts unchanged.
//
// The file:// prefix decides locality, never IdentityClass. That field
// classifies content RETRIEVABILITY from the resolved ref alone
// (pkg/fleet/detail.go:76), so a local revision -- which has no resolved ref —
// comes back IdentityNoRef, not IdentityLocal. Keying on the class would leave
// the scheme on every real local bundle while passing every hand-built test
// identity. A bare registry reference (ghcr.io/acme/svc:1.0, no scheme) is
// remote, not local: only internal/fleetsrc/local.go emits file://, and it does
// so always.
func bundleRef(id fleet.RevisionIdentity) (ref string, local bool) {
	ref = id.ResolvedRef
	if ref == "" {
		ref = id.RequestedRef
	}
	local = strings.HasPrefix(ref, "file://")
	ref = strings.TrimPrefix(ref, "file://")
	return ref, local
}

// resolveSelection turns a list row into a runnable selection. A revision
// resolves directly; a service resolves through its first active revision,
// which is what the CLI would have picked; a target resolves through the
// revision it is running; an owner or a source has no bundle at all.
func resolveSelection(c *Context, ref fleet.EntityRef) (Selection, error) {
	sel := Selection{Kind: ref.Kind, Key: ref.Key, Label: ref.Label, Version: ref.Version}
	switch ref.Kind {
	case fleet.KindOwner, fleet.KindSource:
		return sel, nil
	}
	det, err := c.Query.EntityDetail(ref.Kind, ref.Key)
	if err != nil {
		return Selection{}, err
	}
	switch {
	case det.Revision != nil:
		sel.Ref, sel.Local = bundleRef(det.Revision.Identity)
		if sel.Version == "" {
			sel.Version = det.Revision.Version
		}
	case det.Service != nil:
		sel.Ref, sel.Local = refFromServiceRevisions(c, det.Service)
	case det.Target != nil:
		sel.Ref, sel.Local = refFromTarget(c, det.Target)
	}
	return sel, nil
}

// refFromRevisionKey resolves a revision by key and returns its bundle ref plus
// locality. It returns empty when the revision cannot be resolved, which is the
// honest answer: a verb with no ref is a verb that cannot run.
func refFromRevisionKey(c *Context, key string) (string, bool) {
	det, err := c.Query.EntityDetail(fleet.KindRevision, key)
	if err != nil || det.Revision == nil {
		return "", false
	}
	return bundleRef(det.Revision.Identity)
}

// refFromServiceRevisions resolves a service through its first active revision,
// which is the one the CLI would have picked.
func refFromServiceRevisions(c *Context, s *fleet.ServiceDetailData) (string, bool) {
	if len(s.ActiveRevisions.Items) == 0 {
		return "", false
	}
	return refFromRevisionKey(c, s.ActiveRevisions.Items[0].Key)
}

// refFromTarget resolves a target through the revision it is running.
func refFromTarget(c *Context, t *fleet.TargetDetailData) (string, bool) {
	if t.Revision == nil {
		return "", false
	}
	return refFromRevisionKey(c, t.Revision.Key)
}
