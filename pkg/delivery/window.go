package delivery

import (
	"cmp"
	"errors"
	"slices"
	"time"
)

// WindowEntries keeps the changes of h that ran at some time between since
// and until; a zero bound is open. A change runs from when it was applied
// (or, still in flight, since when it waits) until it finished, or until now
// while in flight. A finish time only bounds the end: a change with no start,
// such as a refused record or a gap, is never dropped for starting after
// until. A change with no time, or a time that does not parse, is kept: an
// unknown time is never evidence that it fell outside. Completeness, problems and what was unreadable are kept as
// they are: a filter never makes a history more complete. h is not changed.
func WindowEntries(h History, since, until time.Time) History {
	n := len(h.Entries)
	h.Entries = slices.DeleteFunc(slices.Clone(h.Entries), func(e HistoryEntry) bool { return outside(e, since, until) })
	if len(h.Entries) < n {
		h.Note += "; only changes " + windowWords(since, until) + " are listed"
	}
	return h
}

// CheckWindow refuses a window that ends before it starts: it would drop every
// timed change and read as nothing changed.
func CheckWindow(since, until time.Time) error {
	if !since.IsZero() && !until.IsZero() && since.After(until) {
		return errors.New("since is after until")
	}
	return nil
}

// outside reports whether e certainly ran only before since or after until.
// A missing or unparseable time decides nothing; in flight, it runs until now.
func outside(e HistoryEntry, since, until time.Time) bool {
	start, err := time.Parse(time.RFC3339, cmp.Or(e.Applied, e.Since))
	if err == nil && !until.IsZero() && start.After(until) {
		return true
	}
	end, err := time.Parse(time.RFC3339, e.Finished)
	return err == nil && !since.IsZero() && end.Before(since)
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
