// boot.js — loads the Pacto engine (app.wasm) and routes the dashboard's API
// calls into it, so the whole dashboard runs client-side with no backend.
//
// Injected as a classic script BEFORE the app's ES module, so the fetch shim is
// installed before the Svelte app issues its first request. Loaded after
// wasm_exec.js (which defines the global `Go`).
(function () {
  "use strict";

  // Canonical demo entry: the public WASM Live Demo is deliberately Fleet-capable, so
  // its HOME is the product Operational Overview (#/fleet), not the superseded legacy
  // landing. When the demo is reached with NO meaningful hash route (bare /demo/, "#",
  // or the legacy "#/"), canonicalize to #/fleet HERE -- in the demo bootstrap, before
  // the Svelte app's ES module runs and reads the hash -- so the user never sees a
  // legacy-landing flash. An explicit deep link (any other non-empty hash, e.g.
  // "#/fleet/graph", "#/fleet/services/<key>", "#/readiness") is preserved untouched.
  // This lives in the DEMO bootstrap ONLY (boot.js ships solely with the demo), so a
  // generic non-Fleet dashboard is never forced to assume Fleet exists.
  (function canonicalizeDemoEntry() {
    var h = window.location.hash;
    // Canonicalize with history.replaceState (not a hash assignment): a REPLACE leaves no
    // legacy URL in history, so Back never bounces off a route that immediately
    // re-canonicalizes, and it does not reload the document. The app's ES module runs
    // AFTER this and reads the already-canonical location.hash.
    function canon(hash) { window.history.replaceState(null, "", hash); }
    if (h === "" || h === "#" || h === "#/") {
      canon("#/fleet");
      return;
    }
    // The superseded legacy LIST roots map to their product equivalents (the demo is
    // Fleet-capable, so these concepts live in the product IA). Doing it here, before the
    // Svelte app runs, means the demo never even briefly mounts a legacy screen. The app
    // itself performs the same capability-gated redirect for live Fleet hosts; this is
    // the demo's flash-free fast path. Name-bearing legacy detail URLs (#/services/<name>,
    // #/owners/<id>) are left for the app to resolve through the Product API; any product
    // or other deep link is preserved untouched.
    var LEGACY = { "#/services": "#/fleet/services", "#/graph": "#/fleet/graph", "#/owners": "#/fleet/owners" };
    var path = h.split("?")[0];
    if (LEGACY[path]) {
      canon(LEGACY[path]);
    }
  })();

  // Capture the real fetch before we shadow window.fetch below.
  var realFetch = window.fetch.bind(window);

  // Resolve sibling assets (app.wasm) relative to THIS script's URL so it works
  // under any base path and from the 404.html SPA fallback at a deep link.
  var scriptURL = (document.currentScript && document.currentScript.src) || window.location.href;

  // Readiness gate: the Go runtime calls __pactoOnReady() once __pactoServe is
  // installed. API calls made before then queue on this promise.
  var resolveReady;
  window.__pactoReady = new Promise(function (resolve) { resolveReady = resolve; });
  window.__pactoOnReady = function () { resolveReady(); };

  function instantiate(url, importObject) {
    if (typeof WebAssembly.instantiateStreaming === "function") {
      return WebAssembly.instantiateStreaming(realFetch(url), importObject).catch(function () {
        // Fallback when the host serves app.wasm with the wrong MIME type.
        return realFetch(url)
          .then(function (r) { return r.arrayBuffer(); })
          .then(function (buf) { return WebAssembly.instantiate(buf, importObject); });
      });
    }
    return realFetch(url)
      .then(function (r) { return r.arrayBuffer(); })
      .then(function (buf) { return WebAssembly.instantiate(buf, importObject); });
  }

  var go = new Go();
  instantiate(new URL("app.wasm?v=8078852d2fdb", scriptURL), go.importObject)
    .then(function (result) { go.run(result.instance); })
    .catch(function (err) { console.error("Pacto engine failed to load:", err); });

  // Only the dashboard's own endpoints are served by wasm; all other requests
  // (assets, lazy chunks) go to the network as usual.
  function isApiPath(pathname) {
    return pathname.indexOf("/api/") === 0 || pathname === "/health" || pathname === "/metrics";
  }

  window.fetch = function (input, init) {
    init = init || {};
    var isRequest = typeof Request !== "undefined" && input instanceof Request;
    var rawURL = typeof input === "string" ? input : input.url;
    var u = new URL(rawURL, window.location.href);
    if (!isApiPath(u.pathname)) {
      return realFetch(input, init);
    }
    var method = (init.method || (isRequest ? input.method : "GET") || "GET").toUpperCase();
    // The body may be carried on init.body (a string) OR on a Request object -- the
    // generated openapi-fetch client passes a Request whose body is NOT on init, so a
    // POST body must be read from the Request itself (its text() is async). Reading it
    // here is what lets POST operations (e.g. the product Impact analysis) work in the
    // in-browser demo, not only query-param GETs.
    var bodyPromise;
    if (init.body != null) {
      bodyPromise = Promise.resolve(String(init.body));
    } else if (isRequest && method !== "GET" && method !== "HEAD") {
      bodyPromise = input.clone().text();
    } else {
      bodyPromise = Promise.resolve(null);
    }
    return window.__pactoReady
      .then(function () { return bodyPromise; })
      .then(function (body) {
        var res = window.__pactoServe(method, u.pathname + u.search, body && body.length ? body : null);
        return new Response(res.body, {
          status: res.status,
          headers: { "Content-Type": res.contentType || "application/json" },
        });
      });
  };
})();
