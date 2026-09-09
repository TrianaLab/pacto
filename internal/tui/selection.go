package tui

import (
	"strings"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// Selection is what a verb runs against: an entity plus the argument the CLI
// would have been given for it. Ref is empty for entities that are not backed
// by a bundle (owners and sources), and the verb layer uses that to decide what
// it can offer.
type Selection struct {
	Kind    fleet.EntityKind
	Key     string
	Label   string
	Ref     string
	Version string
}

// bundleRef turns a revision's identity into the argument the app-layer
// resolver takes. A local bundle is recorded by internal/fleetsrc as
// "file://<dir>"; the resolver wants the bare directory. Everything else is an
// OCI reference, which the same resolver accepts unchanged.
func bundleRef(id fleet.RevisionIdentity) string {
	if id.IdentityClass == fleet.IdentityLocal {
		return strings.TrimPrefix(id.RequestedRef, "file://")
	}
	if id.ResolvedRef != "" {
		return id.ResolvedRef
	}
	return id.RequestedRef
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
		sel.Ref = bundleRef(det.Revision.Identity)
		if sel.Version == "" {
			sel.Version = det.Revision.Version
		}
	case det.Service != nil:
		sel.Ref = refFromServiceRevisions(c, det.Service)
	case det.Target != nil:
		sel.Ref = refFromTarget(c, det.Target)
	}
	return sel, nil
}

// refFromRevisionKey resolves a revision by key and returns its bundle ref. It
// returns empty when the revision cannot be resolved, which is the honest
// answer: a verb with no ref is a verb that cannot run.
func refFromRevisionKey(c *Context, key string) string {
	det, err := c.Query.EntityDetail(fleet.KindRevision, key)
	if err != nil || det.Revision == nil {
		return ""
	}
	return bundleRef(det.Revision.Identity)
}

// refFromServiceRevisions resolves a service through its first active revision,
// which is the one the CLI would have picked.
func refFromServiceRevisions(c *Context, s *fleet.ServiceDetailData) string {
	if len(s.ActiveRevisions.Items) == 0 {
		return ""
	}
	return refFromRevisionKey(c, s.ActiveRevisions.Items[0].Key)
}

// refFromTarget resolves a target through the revision it is running.
func refFromTarget(c *Context, t *fleet.TargetDetailData) string {
	if t.Revision == nil {
		return ""
	}
	return refFromRevisionKey(c, t.Revision.Key)
}
