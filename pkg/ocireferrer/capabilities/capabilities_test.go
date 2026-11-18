package capabilities

import "testing"

func TestModeAndEvidence(t *testing.T) {
	cases := []struct {
		r        ReferrersMode
		l        ListingMode
		mode, ev string
	}{
		{ReferrersNative, ListingCatalog, "referrers=native listing=catalog", "Referrers, _catalog"},
		{ReferrersNative, ListingTags, "referrers=native listing=tags", "Referrers, no _catalog"},
		{ReferrersWriteOnceTags, ListingCatalog, "referrers=write-once-tags listing=catalog", "no Referrers, _catalog"},
		{ReferrersWriteOnceTags, ListingTags, "referrers=write-once-tags listing=tags", "no Referrers, no _catalog"},
	}
	for _, tc := range cases {
		c := Capabilities{Referrers: tc.r, Listing: tc.l}
		if c.Mode() != tc.mode || c.Evidence() != tc.ev {
			t.Errorf("%s/%s: Mode %q Evidence %q, want %q %q", tc.r, tc.l, c.Mode(), c.Evidence(), tc.mode, tc.ev)
		}
	}
}
