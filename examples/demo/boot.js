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

  // Status strip. The Svelte shell paints in about a second, but app.wasm is tens
  // of megabytes and every API call queues behind it, so on an ordinary connection
  // a visitor sees a dashboard whose content area is an empty heading for roughly
  // nine seconds. Nothing on the page said why, said the fleet is fabricated, or
  // offered a way back to the documentation this demo is linked from. One strip
  // answers all three, and it lives HERE rather than in the dashboard app because
  // none of it is true of a real deployment.
  (function demoStatusStrip() {
    // The explainer, not the docs root. Two things about this fixture are deliberate
    // and read as bugs -- it opens on a degraded-source banner, and one config name
    // appears twice -- and that page's first paragraphs say so. The home page's
    // primary call to action jumps straight here, so this strip is the only place a
    // visitor who arrived that way is ever told. Relative, so it resolves under both
    // /demo/ and /<version>/demo/; the page it lands on carries the full docs nav.
    var DOCS_HREF = "../examples/dashboard-demo/";
    var el, label, meter;
    // The strip mounts on DOMContentLoaded, but the engine can resolve or fail
    // before that. Hold the latest message so the outcome is never dropped.
    var pending = "Loading the Pacto engine — the panels fill in when it lands.";
    // A download this large is long enough that one unchanging line reads as a hung
    // tab. A counter is the cheapest honest proof of life: instantiateStreaming
    // exposes no byte progress, and teeing the response to count bytes would cost
    // more than it tells anyone. The transfer size comes from the response's own
    // Content-Length rather than a number written here, so it is right on a
    // gzip-serving host and right on one that does not compress. Both live in a
    // span marked aria-hidden -- the per-second update is noise to a screen reader,
    // and only `label` carries anything worth announcing.
    var t0 = Date.now();
    var sizeText = "";
    var finished = false;
    var ticker = setInterval(paint, 1000);

    function paint() {
      // `finished` matters because the engine can resolve before DOMContentLoaded:
      // mount() paints once, and without the flag that first paint would revive a
      // counter for a download that is already over.
      if (!meter || finished) { return; }
      meter.textContent = sizeText + Math.round((Date.now() - t0) / 1000) + "s";
    }
    // Called with the wasm response's Content-Length, which may be absent (a
    // chunked or unknown-length response); then the counter simply runs alone.
    window.__pactoDemoSize = function (bytes) {
      var n = Number(bytes);
      if (n > 0) { sizeText = Math.round(n / 1e6) + " MB · "; paint(); }
    };

    function done() { finished = true; clearInterval(ticker); if (meter) { meter.textContent = ""; } }

    function mount() {
      var style = document.createElement("style");
      style.textContent =
        // pointer-events:none on the container, auto on the link. The strip floats over
        // the dashboard, and at phone widths it wraps tall enough to sit on top of the
        // app's own controls -- a notice must never swallow a tap meant for the thing
        // underneath it. Only the link is clickable.
        "#pacto-demo-strip{position:fixed;left:50%;bottom:12px;transform:translateX(-50%);" +
        "z-index:9999;max-width:min(92vw,46rem);display:flex;gap:.6rem;align-items:center;" +
        "padding:.5rem .9rem;border-radius:999px;background:#1e293b;color:#e2e8f0;" +
        "font:400 .8125rem/1.4 system-ui,-apple-system,'Segoe UI',sans-serif;" +
        "box-shadow:0 2px 12px rgba(15,23,42,.35);pointer-events:none}" +
        "#pacto-demo-strip a{color:#a5b4fc;text-decoration:underline;white-space:nowrap;" +
        "pointer-events:auto}" +
        "#pacto-demo-meter{color:#94a3b8;font-variant-numeric:tabular-nums;white-space:nowrap}" +
        // display:flex above beats the hidden attribute, so say so explicitly or
        // dismissing the strip would do nothing at all.
        "#pacto-demo-strip[hidden]{display:none}" +
        "#pacto-demo-close{pointer-events:auto;appearance:none;border:0;background:none;" +
        "color:#94a3b8;font:inherit;line-height:1;cursor:pointer;border-radius:999px;" +
        "padding:.2rem .4rem;margin:-.2rem -.3rem -.2rem 0}" +
        "#pacto-demo-close:hover{color:#e2e8f0;background:rgba(148,163,184,.2)}" +
        "#pacto-demo-strip a:focus-visible,#pacto-demo-close:focus-visible{outline:2px solid #a5b4fc;outline-offset:2px}" +
        "@media (max-width:480px){#pacto-demo-strip{bottom:8px;max-width:96vw;" +
        "font-size:.75rem;padding:.4rem .7rem}}";
      document.head.appendChild(style);

      el = document.createElement("div");
      el.id = "pacto-demo-strip";
      el.setAttribute("data-testid", "demo-strip");
      // role=status + aria-live so the loading -> ready transition is announced
      // rather than silently swapped under a screen reader.
      el.setAttribute("role", "status");
      el.setAttribute("aria-live", "polite");
      label = document.createElement("span");
      meter = document.createElement("span");
      meter.id = "pacto-demo-meter";
      meter.setAttribute("aria-hidden", "true");
      var link = document.createElement("a");
      link.href = DOCS_HREF;
      link.textContent = "About this demo";
      // The strip is a notice, and a notice the reader has read is in the way. It sits
      // over the bottom of the dashboard, which on a short window is where the content
      // is -- so give it the one control every persistent notice owes the reader.
      // Dismissed for the tab only: a reload is a fresh visitor as far as this fixture
      // knows, and the label is the only thing on the page saying the fleet is invented.
      var close = document.createElement("button");
      close.id = "pacto-demo-close";
      close.type = "button";
      close.setAttribute("data-testid", "demo-strip-close");
      close.setAttribute("aria-label", "Dismiss this notice");
      close.textContent = "×";
      close.addEventListener("click", function () { el.hidden = true; });
      el.appendChild(label);
      el.appendChild(meter);
      el.appendChild(link);
      el.appendChild(close);
      document.body.appendChild(el);
      label.textContent = pending;
      paint();
    }

    function say(text) { pending = text; if (label) { label.textContent = text; } }

    if (document.readyState === "loading") {
      document.addEventListener("DOMContentLoaded", mount);
    } else {
      mount();
    }
    window.__pactoReady.then(function () {
      done();
      // Names the two deliberate oddities rather than only labelling the fixture.
      // The visitor who arrived from the home page's primary call to action lands
      // on a degraded-source banner and a config name that appears twice; told
      // nothing, they read both as a broken product and leave. A pill cannot hold
      // the explanation, but it can say the oddities are intended and put the page
      // that explains them one click away.
      say("Demo — a fixture fleet running entirely in your browser. Nothing here is a real system, and two things that look like bugs are deliberate.");
    });
    window.__pactoDemoFailed = function () {
      done();
      say("The Pacto engine did not load, so every panel will stay empty. Try reloading.");
      // A dismissed notice stays dismissed for anything the reader has already been
      // told. This is not that: without the strip, a failed load is a dashboard whose
      // every panel is permanently empty and nothing anywhere saying why.
      if (el) { el.hidden = false; }
    };
  })();

  function instantiate(url, importObject) {
    // Fetch first and hand the Response (not the promise) to instantiateStreaming,
    // which accepts either. The only reason to split it is to read Content-Length
    // on the way past, so the strip can state the real transfer size instead of a
    // number hard-coded here that would be wrong on any host that compresses
    // differently. Streaming compilation is unaffected — the body is untouched.
    function announce(r) {
      if (window.__pactoDemoSize) { window.__pactoDemoSize(r.headers.get("content-length")); }
      return r;
    }
    if (typeof WebAssembly.instantiateStreaming === "function") {
      return WebAssembly.instantiateStreaming(realFetch(url).then(announce), importObject).catch(function () {
        // Fallback when the host serves app.wasm with the wrong MIME type.
        return realFetch(url)
          .then(function (r) { return r.arrayBuffer(); })
          .then(function (buf) { return WebAssembly.instantiate(buf, importObject); });
      });
    }
    return realFetch(url)
      .then(announce)
      .then(function (r) { return r.arrayBuffer(); })
      .then(function (buf) { return WebAssembly.instantiate(buf, importObject); });
  }

  var go = new Go();
  instantiate(new URL("app.wasm", scriptURL), go.importObject)
    .then(function (result) { go.run(result.instance); })
    .catch(function (err) {
      console.error("Pacto engine failed to load:", err);
      // Say so on the page. Without this the dashboard shell renders and every
      // panel stays empty forever, which reads as a broken product rather than a
      // failed download.
      if (window.__pactoDemoFailed) { window.__pactoDemoFailed(); }
    });

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
