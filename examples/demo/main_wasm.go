//go:build js && wasm

package main

import (
	"embed"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall/js"

	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/trianalab/pacto/pkg/dashboard"
)

// embeddedBundles holds the demo contracts baked into the wasm binary at build
// time. They live in ./bundles, embedded directly (no copy step).
//
//go:embed bundles
var embeddedBundles embed.FS

// handler is the in-memory dashboard router (the same Huma operations the real
// `pacto dashboard` server serves), driven per request from JavaScript.
var handler http.Handler

func main() {
	root, err := fs.Sub(embeddedBundles, "bundles")
	if err != nil {
		panic(err)
	}
	src, err := NewEmbedSource(root)
	if err != nil {
		panic(err)
	}

	// nil UI fs and nil resolver: the static host serves the UI, and the graph
	// resolves from the embedded contracts' declared dependencies — no OCI.
	srv := dashboard.NewServer(src, nil)
	mux := http.NewServeMux()
	api := humago.New(mux, dashboard.APIConfig())
	srv.RegisterOperations(api)
	handler = mux

	js.Global().Set("__pactoServe", js.FuncOf(serve))
	if cb := js.Global().Get("__pactoOnReady"); cb.Type() == js.TypeFunction {
		cb.Invoke()
	}

	select {} // keep the Go runtime alive so __pactoServe stays callable
}

// serve answers a single API request in memory. JS calls it as
// __pactoServe(method, path, body?) and receives { status, body, contentType }.
func serve(_ js.Value, args []js.Value) any {
	method := args[0].String()
	path := args[1].String()

	var body io.Reader
	if len(args) > 2 && args[2].Type() == js.TypeString {
		body = strings.NewReader(args[2].String())
	}

	req := httptest.NewRequest(method, path, body)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	res := rec.Result()
	out, _ := io.ReadAll(res.Body)
	return map[string]any{
		"status":      res.StatusCode,
		"body":        string(out),
		"contentType": res.Header.Get("Content-Type"),
	}
}
