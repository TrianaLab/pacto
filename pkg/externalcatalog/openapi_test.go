package externalcatalog

import (
	"strings"
	"testing"
)

func TestEntryOpenAPIValidation(t *testing.T) {
	d := "@sha256:" + strings.Repeat("a", 64)
	long := "https://h.example/" + strings.Repeat("a", maxOpenAPIBytes-len("https://h.example/")-len(d)+1) + d
	if len(long) != maxOpenAPIBytes+1 || !openAPIRE.MatchString(long) {
		t.Fatalf("the length row must match the pattern: %d bytes", len(long))
	}
	for v, ok := range map[string]bool{
		"oci://registry.example.com/vendors/stripe-openapi" + d:         true,
		"oci://registry.example.com:5000/vendors/stripe" + d:            true,
		"https://raw.example.com/stripe/openapi/spec3.json" + d:         true,
		"oci://registry.example.com/vendors/stripe:2024" + d:            false, // a tag
		"oci://registry.example.com/vendors/stripe":                     false, // no digest
		"https://h.example/spec.json@sha256:" + strings.Repeat("A", 64): false,
		"http://h.example/spec.json" + d:                                false,
		"https://user:token@h.example/spec.json" + d:                    false,
		"https://h.example/spec.json?X-Amz-Signature=x" + d:             false,
		"https://h.example/spec.json#frag" + d:                          false,
		`https://h.example/sp"ec.json` + d:                              false,
		`https://h.example/sp\ec.json` + d:                              false,
		"https:///spec.json" + d:                                        false, // no host
		long:                                                            false,
	} {
		if got := validOpenAPI(v); got != ok {
			t.Errorf("validOpenAPI(%q) = %v, want %v", v, got, ok)
		}
	}
	got := validateErr(t, head+"  - identity: stripe\n    class: vendor-api\n    openapi: \"https://user:token@h.example/s.json"+d+"\"\n")
	wantContains(t, got, "entries[0].openapi", "no credentials, query or fragment")
	if err := mustParse(t, head+"  - identity: stripe\n    class: vendor-api\n    openapi: oci://r.example/v/stripe"+d+"\n").Validate(); err != nil {
		t.Errorf("a valid link refused: %v", err)
	}
}

func TestMergeOrganizationOpenAPIReplacesDefault(t *testing.T) {
	d := "@sha256:" + strings.Repeat("b", 64)
	org := mustParse(t, head+"  - identity: stripe\n    class: vendor-api\n    openapi: oci://r.example/v/stripe"+d+"\n")
	c := Merge(mustParse(t, mergeDef), DefaultRef(), org, &Ref{Origin: OriginOrganization})
	if e, ref, _ := c.Entry("stripe"); e.OpenAPI != "oci://r.example/v/stripe"+d || ref.Origin != OriginOrganization {
		t.Errorf("Entry(stripe) = %+v %+v", e, ref)
	}
}

func TestDefaultCatalogHasNoOpenAPI(t *testing.T) {
	f, err := load(defaultBytes)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range f.Entries {
		if e.OpenAPI != "" {
			t.Errorf("default entry %s links %s", e.Identity, e.OpenAPI)
		}
	}
}
