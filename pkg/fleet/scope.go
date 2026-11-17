package fleet

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
)

// NamespaceScopeLimitation says which namespaces source watches and that the
// rest is unknown. names are the namespaces the reader may see; hidden counts
// the watched namespaces left out of names because the reader may not read
// them. Every Kubernetes source that reads a namespace list states its scope
// with it, so the wording exists once.
func NamespaceScopeLimitation(source string, names []string, hidden int) Limitation {
	var watches string
	switch {
	case hidden == 0:
		watches = countNamespaces(len(names)) + " (" + joinNames(names) + ")"
	case len(names) == 0:
		watches = countNamespaces(hidden) + " you may not read"
	default:
		watches = countNamespaces(len(names)) + " you may read (" + joinNames(names) + ") and " + strconv.Itoa(hidden) + " more"
	}
	return Limitation{
		Code: LimitationK8sNamespaceScoped, Source: source,
		Message: "source " + source + " watches " + watches + "; services in any other namespace are unknown",
	}
}

// unreadablePhrase separates the source from the namespace in an unreadable
// namespace limitation; unreadableNamespace reads it back.
const unreadablePhrase = " could not read namespace "

// NamespaceUnreadableLimitation names a namespace source could not read and
// why, as a reason class (forbidden, not found or error).
func NamespaceUnreadableLimitation(source, namespace, reason string) Limitation {
	return Limitation{
		Code: LimitationK8sNamespaceUnreadable, Source: source,
		Message: "source " + source + unreadablePhrase + namespace + ": " + reason,
	}
}

// unreadableNamespace is the namespace an unreadable namespace limitation
// names. ponytail: the namespace lives only in the prose, read back here;
// a structured subject on Limitation would make it exact.
func unreadableNamespace(l Limitation) (string, bool) {
	if l.Code != LimitationK8sNamespaceUnreadable {
		return "", false
	}
	_, rest, ok := strings.Cut(l.Message, unreadablePhrase)
	ns, _, _ := strings.Cut(rest, ":")
	return ns, ok && ns != ""
}

func countNamespaces(n int) string {
	if n == 1 {
		return "1 namespace"
	}
	return strconv.Itoa(n) + " namespaces"
}

// joinNames joins names with commas and a final "and"; more than 4 show the
// first 3 and "N more", the dashboard banner's rule.
func joinNames(names []string) string {
	if len(names) > 4 {
		names = append(slices.Clone(names[:3]), strconv.Itoa(len(names)-3)+" more")
	}
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// scopeNamespaces lists the namespaces the snapshot's sources read. A
// namespace a source could not read is left out: the dashboard's review there
// is likely refused by the same missing grant, and one refused review would
// fail every request. restrictScope drops its limitation for a restricted
// reader, so only a reader of every namespace learns which one failed.
func scopeNamespaces(s *FleetSnapshot, seen map[string]bool) {
	for _, st := range s.Sources {
		for _, ns := range st.Namespaces {
			seen[ns] = true
		}
	}
}

// hideUnreadable adds to hidden each namespace a source could not read that
// visible refuses, so its limitation alone restricts the answer.
func hideUnreadable(s *FleetSnapshot, visible func(string) bool, hidden map[string]bool) {
	for _, l := range s.Limitations {
		if ns, ok := unreadableNamespace(l); ok && !visible(ns) {
			hidden[ns] = true
		}
	}
}

// restrictScope keeps in each source only the watched namespaces visible says
// yes to, keeps the total, and rebuilds every scope limitation from what is
// left, so a watched namespace the reader may not read is never named while
// the "rest is unknown" explanation stays.
func restrictScope(out *FleetSnapshot, visible func(string) bool) {
	out.Limitations = slices.DeleteFunc(out.Limitations, func(l Limitation) bool {
		if l.Code == LimitationK8sNamespaceScoped {
			return true
		}
		ns, ok := unreadableNamespace(l)
		return ok && !visible(ns)
	})
	for i := range out.Sources {
		st := &out.Sources[i]
		if len(st.Namespaces) == 0 && st.NamespacesTotal == 0 {
			continue
		}
		total := cmp.Or(st.NamespacesTotal, len(st.Namespaces))
		st.Namespaces = slices.DeleteFunc(slices.Clone(st.Namespaces), func(ns string) bool { return !visible(ns) })
		st.NamespacesTotal = total
		out.Limitations = append(out.Limitations, NamespaceScopeLimitation(st.ID, st.Namespaces, total-len(st.Namespaces)))
	}
}
