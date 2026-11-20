package contract

import (
	"bytes"
	"strings"
	"testing"
)

const catalogDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func surfaceRecord() *InferredRecord {
	return &InferredRecord{
		Format: InferredRecordFormat,
		Inputs: InferredInputs{Pacto: "4.6.0", Discovery: "1", Catalog: []string{"default " + catalogDigest}},
		Surface: &InferredSurface{
			Identity:     "stripe",
			Class:        "vendor-api",
			AsOf:         "2026-11-17T10:00:00Z",
			Completeness: &SurfaceCompleteness{State: "partial", Limitations: []string{"Z_LIMIT", "EXTERNALS_UNREPORTED"}},
			Catalog:      &InferredCatalogMatch{Origin: "default", Digest: catalogDigest, Entry: "stripe"},
			Hosts: []SurfaceValue{
				{Value: "files.stripe.com", Sources: []string{"pods", "config"}, Catalog: catalogDigest},
				{Value: "api.stripe.com:443", Sources: []string{"config"}, FirstSeen: "2026-11-01T00:00:00Z", LastSeen: "2026-11-17T00:00:00Z"},
			},
			Ports:   []SurfaceValue{{Value: "443", Sources: []string{"config"}}},
			Clients: []SurfaceValue{{Value: "npm:stripe/14", Sources: []string{"repo"}}, {Value: "github.com/stripe/stripe-go/76", Sources: []string{"repo"}}},
		},
	}
}

func TestInferredSurfaceRoundTrip(t *testing.T) {
	data := surfaceRecord().Marshal()
	got, err := ParseInferredRecord(data)
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, data)
	}
	if got.Surface == nil || got.Surface.Identity != "stripe" || len(got.Surface.Hosts) != 2 || got.Surface.Completeness.State != "partial" {
		t.Fatalf("surface = %+v", got.Surface)
	}
	if !bytes.Equal(got.Marshal(), data) {
		t.Fatalf("round trip changed the bytes:\n%s\n%s", data, got.Marshal())
	}
	// A record without a surface still parses and never writes the key.
	plain := surfaceRecord()
	plain.Surface = nil
	if bytes.Contains(plain.Marshal(), []byte("surface")) {
		t.Fatal("a record without a surface wrote the key")
	}
}

func TestInferredSurfaceMarshalDeterministic(t *testing.T) {
	a := surfaceRecord()
	b := surfaceRecord()
	s := b.Surface
	s.Hosts[0], s.Hosts[1] = s.Hosts[1], s.Hosts[0]
	s.Clients[0], s.Clients[1] = s.Clients[1], s.Clients[0]
	s.Hosts[1].Sources = []string{"config", "pods"}
	s.Completeness.Limitations = []string{"EXTERNALS_UNREPORTED", "Z_LIMIT"}
	if !bytes.Equal(a.Marshal(), b.Marshal()) {
		t.Fatalf("order changed the bytes:\n%s\n%s", a.Marshal(), b.Marshal())
	}
	out := string(a.Marshal())
	if strings.Index(out, "api.stripe.com") > strings.Index(out, "files.stripe.com") ||
		strings.Index(out, "- config") > strings.Index(out, "- pods") {
		t.Fatalf("lists are not sorted:\n%s", out)
	}
	// Marshal never sorts the caller's record in place.
	if a.Surface.Hosts[0].Value != "files.stripe.com" {
		t.Fatal("Marshal changed the caller's record")
	}
}

func TestParseInferredSurfaceRefuses(t *testing.T) {
	for name, tc := range map[string]struct {
		edit func(*InferredSurface)
		want string
	}{
		"bad identity":        {func(s *InferredSurface) { s.Identity = "Stripe" }, "surface.identity"},
		"dotted identity":     {func(s *InferredSurface) { s.Identity = "host/a..b" }, "surface.identity"},
		"unknown class":       {func(s *InferredSurface) { s.Class = "saas" }, "surface.class"},
		"missing asOf":        {func(s *InferredSurface) { s.AsOf = "" }, "surface.asOf"},
		"local asOf":          {func(s *InferredSurface) { s.AsOf = "2026-11-17T10:00:00+01:00" }, "surface.asOf"},
		"missing complete":    {func(s *InferredSurface) { s.Completeness = nil }, "surface.completeness"},
		"unknown state":       {func(s *InferredSurface) { s.Completeness.State = "failed" }, "surface.completeness"},
		"bad catalog origin":  {func(s *InferredSurface) { s.Catalog.Origin = "vendor" }, "surface.catalog"},
		"bad catalog digest":  {func(s *InferredSurface) { s.Catalog.Digest = "md5:x" }, "surface.catalog"},
		"empty catalog entry": {func(s *InferredSurface) { s.Catalog.Entry = "" }, "surface.catalog"},
		"empty sources":       {func(s *InferredSurface) { s.Hosts[0].Sources = nil }, "].sources"},
		"blank source":        {func(s *InferredSurface) { s.Ports[0].Sources = []string{"a b"} }, "surface.ports[0].sources"},
		"bad host":            {func(s *InferredSurface) { s.Hosts[0].Value = "user@api.stripe.com" }, "surface.hosts["},
		"bad port":            {func(s *InferredSurface) { s.Ports[0].Value = "443x" }, "surface.ports[0].value"},
		"bad client":          {func(s *InferredSurface) { s.Clients[0].Value = "stripe" }, "surface.clients["},
		"value twice":         {func(s *InferredSurface) { s.Ports = append(s.Ports, s.Ports[0]) }, "listed twice"},
		"bad value catalog":   {func(s *InferredSurface) { s.Hosts[0].Catalog = "x" }, "catalog is not"},
		"one time only":       {func(s *InferredSurface) { s.Hosts[1].LastSeen = "" }, "both firstSeen and lastSeen"},
		"bad time":            {func(s *InferredSurface) { s.Hosts[1].FirstSeen = "yesterday" }, "RFC 3339"},
		"first after last":    {func(s *InferredSurface) { s.Hosts[1].FirstSeen = "2026-11-18T00:00:00Z" }, "after lastSeen"},
	} {
		t.Run(name, func(t *testing.T) {
			r := surfaceRecord()
			tc.edit(r.Surface)
			if _, err := ParseInferredRecord(r.Marshal()); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	data := strings.Replace(string(surfaceRecord().Marshal()), "surface:\n", "surface:\n    consumers: [checkout]\n", 1)
	if _, err := ParseInferredRecord([]byte(data)); err == nil || !strings.Contains(err.Error(), "consumers") {
		t.Fatalf("unknown key: err = %v\n%s", err, data)
	}
}
