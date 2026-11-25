//go:build js && wasm

package main

import (
	"context"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"syscall/js"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/runrecord/stats"
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
	// ?fleet=N (engine.js hands it over as __pactoFleet) swaps the narrative
	// fleet for a generated one of N services, to see the map at real sizes.
	var snap *fleet.FleetSnapshot
	var byRef map[string]*contract.Bundle
	var cur atomic.Pointer[fleet.Query] // a claim signed in the tab refolds it
	if n := demoFleetSize(js.Global().Get("__pactoFleet").String()); n > 0 {
		snap, err = buildGeneratedFleet(n)
	} else {
		snap, byRef, err = buildDemoFleet(src, partners)
	}
	if err != nil {
		panic(err)
	} else {
		q := fleet.NewQuery(snap)
		cur.Store(q)
		opts.Fleet = func(context.Context) (*fleet.Query, error) { return cur.Load(), nil }
		opts.Impact = demoImpactProvider(byRef)
		// The Metrics page's run records, generated once at load so a session's
		// numbers hold still; the newest coverage point is the query the
		// Overview reads.
		coverage := stats.CoverageOf(q)
		if opts.Stats, err = demoStats(time.Now(), &coverage); err != nil {
			panic(err)
		}
		// The observed capability is derived from the snapshot's observed edges;
		// the demo carries them, so no flag is needed.
	}

	// The payments story's delivery, simulated in the tab (source_delivery.go).
	// The promoter's steps are spaced out so a reader sees a change run,
	// verify and conclude.
	// After each step the views are asked to read again (pacto:refresh), so
	// the change page moves with the promoter instead of at the next poll.
	refresh := func() {
		if g := js.Global(); g.Get("dispatchEvent").Type() == js.TypeFunction { // not in the Node smoke
			g.Call("dispatchEvent", g.Get("Event").New("pacto:refresh"))
		}
	}
	d, err := newDemoDelivery(time.Now, func(f func()) {
		time.AfterFunc(1500*time.Millisecond, func() { f(); refresh() })
	})
	if err != nil {
		panic(err)
	}
	d.wire(&opts)
	// Declare an inferred service from its page (source_declare.go): nobody
	// signs in the demo, so Save draft keeps a draft-<n> in the tab.
	// A claim of an external system is signed with the simulated sign-in and
	// refolds the narrative fleet with every claimed revision of the tab.
	declarer := newDemoDeclarer(func(r *http.Request) string { return d.State(r).User })
	declarer.claimed = refold(src, partners, byRef, &cur, refresh)
	opts.Declare = declarer
	// Create a service from the golden path's page (source_create_service.go):
	// nobody signs, so Save draft keeps a draft-<n> in the tab.
	opts.CreateService = newDemoCreator(func(r *http.Request) string { return d.State(r).User })
	// The production cluster is a hub with two members and one pending cluster.
	opts.ClusterJoins = func(context.Context) (delivery.ClusterJoinList, error) { return demoClusterJoins(), nil }
	if opts.InstallationConfig, err = demoInstallationConfig(); err != nil {
		panic(err)
	}
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

	handler = demoHandler(opts)

	js.Global().Set("__pactoServe", js.FuncOf(serve))
	if cb := js.Global().Get("__pactoOnReady"); cb.Type() == js.TypeFunction {
		cb.Invoke()
	}

	select {} // keep the Go runtime alive so __pactoServe stays callable
}

// refold rebuilds the narrative fleet with the tab's claimed revisions and
// swaps it in: every view reads it from the next request on. A generated
// fleet (no byRef) is left as it is.
func refold(src, partners *EmbedSource, byRef map[string]*contract.Bundle, cur *atomic.Pointer[fleet.Query], refresh func()) func([]fleet.RawRevision) error {
	if byRef == nil {
		return nil
	}
	return func(claims []fleet.RawRevision) error {
		s, refs, err := buildDemoFleet(src, partners, claims...)
		if err != nil {
			return err
		}
		maps.Copy(byRef, refs)
		cur.Store(fleet.NewQuery(s))
		refresh()
		return nil
	}
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

// demoImpactProvider returns the impact provider the demo wires into the
// dashboard. It analyzes against the snapshot the dashboard hands it, the SAME
// published snapshot the Operational Graph serves (so snapshotIds match, section 2.2), resolving the two revisions from the
// in-memory ref index. Observed edges are folded into that snapshot by
// buildDemoFleet, so include-observed is a real capability driven by the SAME
// pipeline the Operational Graph uses (surfacing a shadow consumer), not a placebo
// and not a second, divergent source of observed data.
func demoImpactProvider(byRef map[string]*contract.Bundle) func(context.Context, *fleet.FleetSnapshot, string, string, bool) (*impact.Result, error) {
	return func(ctx context.Context, snap *fleet.FleetSnapshot, oldRef, newRef string, includeObserved bool) (*impact.Result, error) {
		oldB, newB := byRef[oldRef], byRef[newRef]
		if oldB == nil || newB == nil {
			return nil, fmt.Errorf("unknown revision reference (old=%q new=%q)", oldRef, newRef)
		}
		// An inferred contract is read like the CLI reads it: by its location.
		inferred, _ := contract.InferredRef(newRef)
		return impact.Analyze(ctx, oldB.Contract, newB.Contract, oldB.FS, newB.FS, snap, impact.Options{
			IncludeObserved:  includeObserved,
			InferredProvider: inferred,
		}), nil
	}
}
