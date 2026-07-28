package app

import "testing"

func TestSafeOutputName(t *testing.T) {
	cases := []struct {
		name    string
		wantErr bool
	}{
		{"test-svc", false},
		{"acme.svc_v2", false},
		{"", true},
		{".", true},
		{"..", true},
		{"../../etc/cron.d/x", true},
		{"a/b", true},
		{`a\b`, true},
	}
	for _, tc := range cases {
		err := safeOutputName(tc.name)
		if (err != nil) != tc.wantErr {
			t.Errorf("safeOutputName(%q): got err=%v, wantErr=%v", tc.name, err, tc.wantErr)
		}
	}
}
