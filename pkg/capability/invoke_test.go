package capability

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/trianalab/pacto/v2/pkg/openapi"
)

func TestInvokePathQueryAndAuth(t *testing.T) {
	var gotPath, gotQuery, gotAuth, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("verbose")
		gotAuth = r.Header.Get("Authorization")
		gotKey = r.Header.Get("X-API-Key")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	doc := &openapi.Doc{SecuritySchemes: map[string]openapi.SecurityScheme{
		"bearerAuth": {Type: "http", Scheme: "bearer"},
		"apiKey":     {Type: "apiKey", In: "header", Name: "X-API-Key"},
	}, Security: []openapi.SecurityRequirement{{"bearerAuth": {}}}}
	op := openapi.Operation{
		Method: "GET", Path: "/users/{id}",
		Parameters: []openapi.Parameter{
			{Name: "id", In: "path", Required: true},
			{Name: "verbose", In: "query"},
			{Name: "absent", In: "query"}, // not supplied in args
		},
		Security: []openapi.SecurityRequirement{{"apiKey": {}}},
	}
	res, err := Invoke(context.Background(), srv.Client(), op, doc, srv.URL+"/",
		map[string]any{"id": "42", "verbose": true},
		Credentials{"apiKey": "secret", "bearerAuth": "tok"})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if res.StatusCode != 200 || res.Body != `{"ok":true}` {
		t.Fatalf("result = %+v", res)
	}
	if res.Headers["Content-Type"] == "" {
		t.Fatalf("expected response headers, got %v", res.Headers)
	}
	if gotPath != "/users/42" || gotQuery != "true" {
		t.Fatalf("path=%q query=%q", gotPath, gotQuery)
	}
	if gotKey != "secret" {
		t.Fatalf("apiKey header = %q", gotKey)
	}
	if gotAuth != "" { // op-level security overrides global → only apiKey applies
		t.Fatalf("unexpected Authorization %q", gotAuth)
	}
}

func TestInvokeQueryApiKeyAndBasicFallback(t *testing.T) {
	var gotKey, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.URL.Query().Get("api_key")
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(204)
	}))
	defer srv.Close()

	doc := &openapi.Doc{SecuritySchemes: map[string]openapi.SecurityScheme{
		"q":     {Type: "apiKey", In: "query", Name: "api_key"},
		"basic": {Type: "http", Scheme: "basic"},
		"oidc":  {Type: "openIdConnect"},
		"empty": {Type: "apiKey", In: "header", Name: "X-Empty"},
	}}
	// global security used because op.Security is nil
	doc.Security = []openapi.SecurityRequirement{{"q": {}, "basic": {}, "oidc": {}, "empty": {}, "unknownName": {}}}
	op := openapi.Operation{Method: "GET", Path: "/x"}
	_, err := Invoke(context.Background(), srv.Client(), op, doc, srv.URL, nil,
		Credentials{"q": "K", "basic": "dXNlcjpwYXNz", "oidc": "T", "empty": ""})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if gotKey != "K" {
		t.Fatalf("query api key = %q", gotKey)
	}
	// last applied scheme wins for Authorization; both basic and oidc set it.
	if gotAuth == "" {
		t.Fatalf("expected an Authorization header from basic/oidc, got empty")
	}
}

func TestInvokeBodyAndNon2xx(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type = %q", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("X-Trace") != "abc" {
			t.Errorf("header param not sent: %q", r.Header.Get("X-Trace"))
		}
		w.WriteHeader(422)
		_, _ = io.WriteString(w, "bad")
	}))
	defer srv.Close()
	op := openapi.Operation{Method: "POST", Path: "/refunds",
		Parameters:  []openapi.Parameter{{Name: "X-Trace", In: "header"}},
		RequestBody: &openapi.RequestBody{Required: true}}
	res, err := Invoke(context.Background(), srv.Client(), op, &openapi.Doc{}, srv.URL,
		map[string]any{"body": map[string]any{"amount": 10}, "X-Trace": "abc"}, nil)
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}
	if res.StatusCode != 422 || res.Body != "bad" {
		t.Fatalf("non-2xx result = %+v", res)
	}
	if body["amount"].(float64) != 10 {
		t.Fatalf("server body = %v", body)
	}
}

func TestInvokeNoBodyArgSkipsBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "" {
			t.Errorf("no body expected, content-type = %q", r.Header.Get("Content-Type"))
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	op := openapi.Operation{Method: "POST", Path: "/x", RequestBody: &openapi.RequestBody{}}
	// nil client → DefaultClient path
	if _, err := Invoke(context.Background(), nil, op, nil, srv.URL, nil, nil); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
}

func TestInvokeHTTPBearer(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
	}))
	defer srv.Close()
	doc := &openapi.Doc{
		SecuritySchemes: map[string]openapi.SecurityScheme{"b": {Type: "http", Scheme: "bearer"}},
		Security:        []openapi.SecurityRequirement{{"b": {}}},
	}
	op := openapi.Operation{Method: "GET", Path: "/x"}
	if _, err := Invoke(context.Background(), srv.Client(), op, doc, srv.URL, nil, Credentials{"b": "tok"}); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if gotAuth != "Bearer tok" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
}

func TestInvokeUnmarshalableBody(t *testing.T) {
	op := openapi.Operation{Method: "POST", Path: "/x", RequestBody: &openapi.RequestBody{Required: true}}
	_, err := Invoke(context.Background(), nil, op, nil, "http://x",
		map[string]any{"body": make(chan int)}, nil)
	if err == nil {
		t.Fatal("expected marshal error for unmarshalable body")
	}
}

type errBody struct{}

func (errBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (errBody) Close() error             { return nil }

type errRoundTripper struct{}

func (errRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Body: errBody{}, Header: http.Header{}}, nil
}

func TestInvokeReadError(t *testing.T) {
	client := &http.Client{Transport: errRoundTripper{}}
	op := openapi.Operation{Method: "GET", Path: "/x"}
	if _, err := Invoke(context.Background(), client, op, nil, "http://x", nil, nil); err == nil {
		t.Fatal("expected read error")
	}
}

func TestInvokeTransportError(t *testing.T) {
	op := openapi.Operation{Method: "GET", Path: "/x"}
	_, err := Invoke(context.Background(), http.DefaultClient, op, &openapi.Doc{},
		"http://127.0.0.1:0", nil, nil)
	if err == nil {
		t.Fatal("expected transport error")
	}
}

func TestInvokeBadMethod(t *testing.T) {
	op := openapi.Operation{Method: "bad method", Path: "/x"}
	if _, err := Invoke(context.Background(), nil, op, nil, "http://x", nil, nil); err == nil {
		t.Fatal("expected request-build error for invalid method")
	}
}
