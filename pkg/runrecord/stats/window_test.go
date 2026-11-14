package stats

import (
	"strings"
	"testing"
	"time"
)

func TestParseWindow(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name, since, until string
		want               Window
		err                string
	}{
		{name: "empty", want: Window{Since: now.Add(-DefaultWindow), Until: now}},
		{name: "days", since: "30d", want: Window{Since: now.Add(-30 * 24 * time.Hour), Until: now}},
		{name: "duration", since: "12h", until: "1h", want: Window{Since: now.Add(-12 * time.Hour), Until: now.Add(-time.Hour)}},
		{name: "date", since: "2026-11-01", want: Window{Since: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), Until: now}},
		{name: "rfc3339", since: "2026-11-01T10:00:00+02:00", until: "2026-11-02T00:00:00Z",
			want: Window{Since: time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC), Until: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)}},
		{name: "until before since", since: "1h", until: "2h", err: "--until must be after --since"},
		{name: "until equals since", since: "2026-11-01", until: "2026-11-01", err: "--until must be after --since"},
		{name: "zero days", since: "0d", err: "--since"},
		{name: "negative days", until: "-3d", err: "--until"},
		{name: "too many days", since: "99999999d", err: "--since"},
		{name: "zero duration", since: "0s", err: "--since"},
		{name: "negative duration", since: "-2h", err: "--since"},
		{name: "garbage", since: "last week", err: "--since"},
		{name: "garbage until", until: "soon", err: "--until"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseWindow(c.since, c.until, now)
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("err = %v, want %q", err, c.err)
				}
				return
			}
			if err != nil || !got.Since.Equal(c.want.Since) || !got.Until.Equal(c.want.Until) {
				t.Fatalf("got %+v %v, want %+v", got, err, c.want)
			}
			if got.Since.Location() != time.UTC || got.Until.Location() != time.UTC {
				t.Fatalf("window not in UTC: %+v", got)
			}
		})
	}
}
