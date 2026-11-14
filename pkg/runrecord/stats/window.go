package stats

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// DefaultWindow is how far back a read looks when no since is given.
const DefaultWindow = 30 * 24 * time.Hour

// maxDays bounds a <n>d value so it cannot overflow a duration.
const maxDays = 36500

// Window is the time range an answer covers.
type Window struct {
	Since time.Time `json:"since" doc:"Start of the window, inclusive, UTC"`
	Until time.Time `json:"until" doc:"End of the window, UTC"`
}

// ParseWindow reads since and until. Each is empty, a number of days (30d),
// a Go duration (12h), a date (2006-01-02, UTC midnight) or an RFC 3339 time.
// A relative value counts back from now. An empty since is now minus
// [DefaultWindow]; an empty until is now.
func ParseWindow(since, until string, now time.Time) (Window, error) {
	now = now.UTC()
	s, err := parseBound("--since", since, now, now.Add(-DefaultWindow))
	if err != nil {
		return Window{}, err
	}
	u, err := parseBound("--until", until, now, now)
	if err != nil {
		return Window{}, err
	}
	if !u.After(s) {
		return Window{}, errors.New("--until must be after --since")
	}
	return Window{Since: s, Until: u}, nil
}

func parseBound(flag, v string, now, def time.Time) (time.Time, error) {
	if v == "" {
		return def, nil
	}
	if n, ok := strings.CutSuffix(v, "d"); ok {
		if days, err := strconv.Atoi(n); err == nil {
			if days <= 0 || days > maxDays {
				return time.Time{}, fmt.Errorf("%s %s: give between 1 and %d days", flag, v, maxDays)
			}
			return now.Add(-time.Duration(days) * 24 * time.Hour), nil
		}
	}
	if d, err := time.ParseDuration(v); err == nil {
		if d <= 0 {
			return time.Time{}, fmt.Errorf("%s %s: give a positive duration", flag, v)
		}
		return now.Add(-d), nil
	}
	if t, err := time.Parse(time.DateOnly, v); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("%s %q: give days (30d), a duration (12h), a date (2026-11-01) or an RFC 3339 time", flag, v)
}
