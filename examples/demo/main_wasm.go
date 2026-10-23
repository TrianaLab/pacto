//go:build js && wasm

package main

import (
	"context"
	"embed"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall/js"
	"time"

	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// embeddedBundles holds the demo contracts baked into the wasm binary at build
// time. They live in ./bundles, embedded directly (no copy step).
//
//go:embed bundles
var embeddedBundles embed.FS

// embeddedPartners holds a SECOND domain's contracts. They are kept out of
// ./bundles deliberately: one of them is another "platform-app-config", and the
// legacy name-keyed source cannot hold two services with the same name. Only the
// operational graph, which identifies a service by domain AND name, can — which
// is the whole reason this domain exists in the demo.
//
//go:embed partners
var embeddedPartners embed.FS

// handler is the in-memory dashboard router (the same Huma operations the real
// `pacto dashboard` server serves), driven per request from JavaScript.
var handler http.Handler

// version is the Pacto version this demo was built from. Injected at build time
// via -ldflags "-X main.version=..." (see the Makefile) and surfaced through
// /health so the dashboard navbar shows it, exactly like the real server.
var version = "dev"

func main() {
	root, err := fs.Sub(embeddedBundles, "bundles")
	if err != nil {
		panic(err)
	}
	src, err := NewEmbedSource(root)
	if err != nil {
		panic(err)
	}
	partnersRoot, err := fs.Sub(embeddedPartners, "partners")
	if err != nil {
		panic(err)
	}
	partners, err := NewEmbedSource(partnersRoot)
	if err != nil {
		panic(err)
	}

	// nil UI fs: the static host serves the UI, and every answer comes from the
	// fleet snapshot built below out of the embedded contracts — no OCI, no server.
	opts := dashboard.Options{Version: version} // version is surfaced via /health → shown in the navbar

	// Wire the operational graph (fleet) and impact provider from the embedded
	// bundles + deterministic targets, so the Operational Graph and Impact pages
	// prove the full product story in the browser with no server and no OCI.
	if snap, byRef, err := buildDemoFleet(src, partners); err != nil {
		panic(err)
	} else {
		q := fleet.NewQuery(snap)
		opts.Fleet = func(context.Context) (*fleet.Query, error) { return q, nil }
		opts.Impact = demoImpactProvider(snap, byRef)
		// The observed capability is derived from the snapshot's observed edges;
		// the demo carries them, so no flag is needed.
	}

	// The payments story's delivery, simulated in the tab (source_delivery.go).
	// The promoter's steps are spaced out so a reader sees a change run,
	// verify and conclude.
	// After each step the views are asked to read again (pacto:refresh), so
	// the change page moves with the promoter instead of at the next poll.
	refresh := func() {
		js.Global().Call("dispatchEvent", js.Global().Get("Event").New("pacto:refresh"))
	}
	d, err := newDemoDelivery(time.Now, func(f func()) {
		time.AfterFunc(1500*time.Millisecond, func() { f(); refresh() })
	})
	if err != nil {
		panic(err)
	}
	d.wire(&opts)
	js.Global().Set("__pactoDeliveryKey", d.KeyHint())
	js.Global().Set("__pactoPactifyPlan", pactifyPlan)
	// __pactoPersona(name, fresh) switches who the tab acts as: pay-agent,
	// alice or carol; fresh is a simulated sign-in made now.
	js.Global().Set("__pactoPersona", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) < 1 {
			return "name a persona"
		}
		if err := d.SetPersona(args[0].String(), len(args) > 1 && args[1].Truthy()); err != nil {
			return err.Error()
		}
		return ""
	}))

	mux := http.NewServeMux()
	api := humago.New(mux, dashboard.APIConfig())
	dashboard.NewServer(opts).RegisterOperations(api)
	handler = mux

	js.Global().Set("__pactoServe", js.FuncOf(serve))
	if cb := js.Global().Get("__pactoOnReady"); cb.Type() == js.TypeFunction {
		cb.Invoke()
	}

	select {} // keep the Go runtime alive so __pactoServe stays callable
}

// serve answers a single API request in memory. JS calls it as
// __pactoServe(method, path, body?, headers?) and receives { status, body,
// contentType }. The headers are not read here: the simulated sign-in is the
// tab's current persona.
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
