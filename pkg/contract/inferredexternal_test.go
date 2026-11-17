package contract

import (
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

func externalRecord() *InferredRecord {
	rec := sampleRecord()
	major := 11
	rec.Inputs.Catalog = []string{"default " + digestOf("catalog")}
	rec.Inputs.Sources = append(rec.Inputs.Sources, InferredSourceFile{Path: "chart/values.yaml", Digest: digestOf("values")})
	rec.Fields = append(rec.Fields, Inferred{Field: "dependencies[stripe]", Value: "stripe", Source: "chart/values.yaml:3", Confidence: ConfidenceLikely,
		External: &InferredExternal{Identity: "stripe", Catalog: "default", Sources: []InferredExternalSource{
			{Kind: "helmValues", Path: "chart/values.yaml", Line: 9, Key: "STRIPE_URL", Host: "api.stripe.com"},
			{Kind: "helmValues", Path: "chart/values.yaml", Line: 3, Key: "STRIPE_API_BASE", Host: "api.stripe.com:443"},
		}, Client: &InferredClient{SDK: "stripe", Major: &major}}},
		Inferred{Field: "dependencies[sqs]", Value: "aws/sqs", Source: "deploy/app.yaml:1", Confidence: ConfidencePossible,
			External: &InferredExternal{Identity: "aws/sqs", Catalog: "organization", Sources: []InferredExternalSource{{Kind: "env", Path: "deploy/app.yaml"}},
				Resource: &InferredResource{Type: "aws_sqs_queue", Name: "orders", Missing: []string{"account", "region"}}}})
	return rec
}

func TestInferredRecordExternalRoundTrip(t *testing.T) {
	rec := externalRecord()
	got, err := ParseInferredRecord(rec.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	var stripe *InferredExternal
	for _, f := range got.Fields {
		if f.Field == "dependencies[stripe]" {
			stripe = f.External
		}
	}
	if stripe == nil || stripe.Sources[0].Line != 3 || *stripe.Client.Major != 11 {
		t.Fatalf("sources are not sorted by line, or the client is lost: %+v", stripe)
	}
	if rec.Fields[2].External.Sources[0].Line != 9 {
		t.Error("Marshal sorted the caller's sources")
	}
	if err := VerifyInferredRecord(fstest.MapFS{
		"openapi/v1.yaml": {Data: []byte("openapi: 3.0.0\n")}, "deploy/app.yaml": {Data: []byte("kind: Deployment\n")}, "chart/values.yaml": {Data: []byte("values")},
	}, got); err != nil {
		t.Fatal(err)
	}
	got.Fields[1].External.Sources[0].Path = "other.yaml"
	if err := VerifyInferredRecord(fstest.MapFS{}, got); err == nil {
		t.Error("an external source the inputs do not list verified")
	}
}

func TestInferredInputsCatalogKey(t *testing.T) {
	rec := externalRecord()
	if !strings.Contains(string(rec.Marshal()), "catalog:\n        - default sha256:") {
		t.Fatalf("no catalog input:\n%s", rec.Marshal())
	}
	for _, bad := range []string{"default " + digestOf("x") + " (embedded)", "vendor " + digestOf("x"), "default sha256:abc"} {
		rec.Inputs.Catalog = []string{bad}
		if _, err := ParseInferredRecord(rec.Marshal()); err == nil || !strings.Contains(err.Error(), "inputs.catalog[0]") {
			t.Errorf("%q: %v", bad, err)
		}
	}
	prev, next := externalRecord(), externalRecord()
	next.Inputs.Catalog = []string{"default " + digestOf("other")}
	unchanged, what := inferredOnly(t, prev, next)
	if !unchanged || !strings.Contains(what, "catalog default sha256:") {
		t.Errorf("a catalog change: %v %q", unchanged, what)
	}
}

// inferredOnly is InferredUnchanged over two bundles that differ only in
// their records.
func inferredOnly(t *testing.T, prev, next *InferredRecord) (bool, string) {
	t.Helper()
	bundle := func(rec *InferredRecord) *Bundle {
		return &Bundle{Contract: &Contract{}, FS: fstest.MapFS{
			"openapi/v1.yaml": {Data: []byte("openapi: 3.0.0\n")}, "deploy/app.yaml": {Data: []byte("kind: Deployment\n")}, "chart/values.yaml": {Data: []byte("values")},
			InferredRecordFile: {Data: rec.Marshal()},
		}}
	}
	return InferredUnchanged(bundle(prev), bundle(next), func(*Bundle) (string, error) { return "d", nil })
}

func TestInferredRecordValidatesClientAndResource(t *testing.T) {
	neg := -1
	for name, edit := range map[string]func(x *InferredExternal){
		"negative major":     func(x *InferredExternal) { x.Client = &InferredClient{SDK: "stripe", Major: &neg} },
		"client with no sdk": func(x *InferredExternal) { x.Client = &InferredClient{} },
		"unknown missing": func(x *InferredExternal) {
			x.Resource = &InferredResource{Type: "aws_sqs_queue", Missing: []string{"id"}}
		},
		"resource no type": func(x *InferredExternal) { x.Resource = &InferredResource{Name: "orders"} },
	} {
		refused(t, name, edit)
	}
}

func TestInferredRecordRefusesExternalShapes(t *testing.T) {
	for name, edit := range map[string]func(x *InferredExternal){
		"unknown kind":       func(x *InferredExternal) { x.Sources[0].Kind = "dns" },
		"host with userinfo": func(x *InferredExternal) { x.Sources[0].Host = "admin:pw@db.example.com" },
		"path with ..":       func(x *InferredExternal) { x.Sources[0].Path = "../secrets.env" },
		"key is a URL":       func(x *InferredExternal) { x.Sources[0].Key = "postgres://x" },
		"negative line":      func(x *InferredExternal) { x.Sources[0].Line = -2 },
		"bad identity":       func(x *InferredExternal) { x.Identity = "Not An Identity" },
		"bad origin":         func(x *InferredExternal) { x.Catalog = "vendor" },
		"no sources":         func(x *InferredExternal) { x.Sources = nil },
	} {
		refused(t, name, edit)
	}
	rec := externalRecord()
	rec.Fields[2].Field = "configuration.STRIPE"
	if _, err := ParseInferredRecord(rec.Marshal()); err == nil || !strings.Contains(err.Error(), "want a dependencies[<name>] field") {
		t.Errorf("external on a configuration field: %v", err)
	}
}

// TestHandEditedRecordWithCredentialRefused: a record edited to carry a
// credential in a host or a query in a source path is refused at parse, so
// at pack and push.
func TestHandEditedRecordWithCredentialRefused(t *testing.T) {
	refused(t, "userinfo host", func(x *InferredExternal) { x.Sources[0].Host = "admin:pw@db.example.com" })
	refused(t, "query in path", func(x *InferredExternal) { x.Sources[0].Path = "chart/values.yaml?password=s3cr3t" })
}

// refused edits the stripe external of a valid record and expects the parse
// to fail naming it.
func refused(t *testing.T, name string, edit func(*InferredExternal)) {
	t.Helper()
	rec := externalRecord()
	edit(rec.Fields[2].External)
	if _, err := ParseInferredRecord(rec.Marshal()); err == nil || !strings.Contains(err.Error(), ".external") {
		t.Errorf("%s: %v", name, err)
	}
}

func TestExternalPathsNone(t *testing.T) {
	if got := externalPaths(Inferred{}); got != nil {
		t.Error(got)
	}
	if got := externalPaths(externalRecord().Fields[3]); !reflect.DeepEqual(got, []string{"deploy/app.yaml"}) {
		t.Error(got)
	}
}
