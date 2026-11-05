package delivery

import (
	"cmp"
	"slices"
	"time"
)

// WindowEntries keeps the changes of h that ran at some time between since
// and until; a zero bound is open. A change runs from when it was applied
// (or, still in flight, since when it waits; or else when it finished) until
// it finished, or until now while in flight. A change with no time, or a time
// that does not parse, is kept: an unknown time is never evidence that it
// fell outside. Completeness, problems and what was unreadable are kept as
// they are: a filter never makes a history more complete. h is not changed.
func WindowEntries(h History, since, until time.Time) History {
	n := len(h.Entries)
	h.Entries = slices.DeleteFunc(slices.Clone(h.Entries), func(e HistoryEntry) bool { return outside(e, since, until) })
	if len(h.Entries) < n {
		h.Note += "; only changes " + windowWords(since, until) + " are listed"
	}
	return h
}

// outside reports whether e certainly ran only before since or after until.
func outside(e HistoryEntry, since, until time.Time) bool {
	start, err := time.Parse(time.RFC3339, cmp.Or(e.Applied, e.Since, e.Finished))
	if err != nil {
		return false // no time, or one that does not parse
	}
	var end time.Time // in flight: it runs until now
	if e.Finished != "" {
		if end, err = time.Parse(time.RFC3339, e.Finished); err != nil {
			return false
		}
	}
	return !until.IsZero() && start.After(until) || !since.IsZero() && !end.IsZero() && end.Before(since)
}

// windowWords says a window in words.
func windowWords(since, until time.Time) string {
	switch {
	case since.IsZero():
		return "at or before " + until.UTC().Format(time.RFC3339)
	case until.IsZero():
		return "at or after " + since.UTC().Format(time.RFC3339)
	}
	return "between " + since.UTC().Format(time.RFC3339) + " and " + until.UTC().Format(time.RFC3339)
}

// sameDomain reports whether a Stage naming stage as its service's domain may
// be the service in domain. A Stage that names no domain cannot be told apart
// by it, so it matches any.
// ponytail: an unscoped Stage still matches every same-named service; the
// service page marks such a row "Stage names no domain" instead of hiding it.
func sameDomain(stage, domain string) bool { return stage == "" || stage == domain }

// OfService reports whether st is a Stage of service in domain; no service
// keeps every Stage.
func OfService(st StageObject, service, domain string) bool {
	return service == "" || st.Spec.Service.Name == service && sameDomain(st.Spec.Service.Domain, domain)
}
