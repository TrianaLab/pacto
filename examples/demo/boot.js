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

    // The walkthrough, once the engine has landed. A visitor who arrives from the
    // home page's call to action gets a dashboard of a fleet they have never seen,
    // and no amount of labelling tells them what any of it is FOR. So the strip
    // stops being a notice and becomes a stepper: six questions, each one driving
    // the real app to the screen that answers it. Nothing is simulated -- every step
    // is a route this dashboard already serves, read against the fixture.
    //
    // Step 1 is the notice the strip used to show, word for word, because the
    // obligations it carries do not go away: the fleet is fabricated, and two of its
    // properties read as bugs and are not. Making it the first step is how the tour
    // gets to exist without dropping any of that.
    //
    // This lives in boot.js, which ships with the demo and nothing else, so a real
    // deployment cannot show it even by mistake -- there is no flag to set wrong.
    // `hash` is the route the step navigates to; the last step has none, because it
    // is about where to go next rather than about a screen.
    var TOUR = [{
      text: "Demo — a fixture fleet running entirely in your browser. Nothing here is a real system, and two things that look like bugs are deliberate.",
      hash: "#/fleet"
    }, {
      text: "Step 2 — what is out there. Every service Pacto found, with its owner, how many contract revisions it has published and how many deployed targets are running them. All of it read from contracts.",
      hash: "#/fleet/services"
    }, {
      text: "Step 3 — one service in full. What payments-service declares, every revision of it, and what was observed about the targets running it. Compliance has four states, and “not evaluated” is not one of the passing ones.",
      hash: "#/fleet/services/payments-service"
    }, {
      text: "Step 4 — about to ship a break. Two revisions compared field by field. Removing an API path is Breaking; adding an optional one is not. The classification rules are a published table, not a heuristic.",
      hash: "#/fleet/changes/payments-service"
    }, {
      text: "Step 5 — who the change reaches. Edges a contract declares and edges something observed are kept apart rather than averaged, so a consumer that declared nothing still turns up.",
      hash: "#/fleet/graph"
    }, {
      text: "That is the whole loop: what is out there, what state it is in, what a change breaks and who it reaches — from contracts plus observations, with nothing guessed.",
      href: "../examples/demo-tour/",
      linkText: "The same six questions from a terminal"
    }];

    var el, label, meter, stepNum, backBtn, nextBtn, moreLink;
    var step = 0;
    // Set when the engine resolves. mount() reads it because the two can happen in
    // either order, and only the ready path owns the tour.
    var ready = false;
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
        // The stepper's own controls. Same pointer-events:auto rule as the link: the
        // strip stays transparent to taps meant for the dashboard under it, and only
        // the things a reader can actually press are pressable.
        "#pacto-demo-strip button{pointer-events:auto;appearance:none;cursor:pointer;" +
        "font:inherit;line-height:1}" +
        "#pacto-demo-back,#pacto-demo-next{border:1px solid #475569;background:none;" +
        "color:#e2e8f0;border-radius:999px;padding:.2rem .7rem;white-space:nowrap}" +
        "#pacto-demo-next{border-color:#a5b4fc;color:#c7d2fe}" +
        "#pacto-demo-back:hover,#pacto-demo-next:hover{background:rgba(148,163,184,.2)}" +
        "#pacto-demo-back:focus-visible,#pacto-demo-next:focus-visible{outline:2px solid #a5b4fc;outline-offset:2px}" +
        "#pacto-demo-step{color:#94a3b8;font-variant-numeric:tabular-nums;white-space:nowrap}" +
        // The strip wraps once the stepper is in it, so the text can no longer be
        // vertically centred against a single row of controls.
        // A pill radius on a box that now wraps to three lines reads as a lozenge, so
        // soften it; the loading state is one line and looks the same either way.
        "#pacto-demo-strip{flex-wrap:wrap;justify-content:center;border-radius:14px}" +
        "#pacto-demo-strip>span:first-child{flex:1 1 18rem;min-width:0}" +
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
      stepNum = document.createElement("span");
      stepNum.id = "pacto-demo-step";
      // The counter duplicates "Step N —" already in the step text, so it is decoration
      // to a screen reader and noise inside a live region that re-announces on every
      // step. The text is the accessible copy.
      stepNum.setAttribute("aria-hidden", "true");
      stepNum.hidden = true;

      backBtn = stepButton("pacto-demo-back", "demo-tour-back", "Back", -1);
      nextBtn = stepButton("pacto-demo-next", "demo-tour-next", "Next", 1);

      // Only the last step has somewhere further to send the reader, so this anchor is
      // separate from "About this demo" rather than a mutation of it: the explainer
      // link is owed on every step, and swapping its target would take it away.
      moreLink = document.createElement("a");
      moreLink.id = "pacto-demo-more";
      moreLink.setAttribute("data-testid", "demo-tour-more");
      moreLink.hidden = true;

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
      close.addEventListener("click", function () { el.hidden = true; reserve(); });
      el.appendChild(label);
      el.appendChild(meter);
      el.appendChild(stepNum);
      el.appendChild(backBtn);
      el.appendChild(nextBtn);
      el.appendChild(moreLink);
      el.appendChild(link);
      el.appendChild(close);
      document.body.appendChild(el);
      // The strip floats over the foot of the page, and its controls are the part of
      // it that must stay clickable -- which at phone widths means they sit on top of
      // whatever the dashboard drew there and swallow taps meant for it.
      // pointer-events cannot fix that: a button that ignores pointers is not a
      // button. Reserving the strip's own height at the end of the document lets the
      // page scroll clear of it instead, so nothing underneath is ever unreachable.
      // Observed rather than measured once: the height changes on every step, because
      // each step's text wraps to a different number of lines.
      if (window.ResizeObserver) { new window.ResizeObserver(reserve).observe(el); }
      // The engine can resolve before DOMContentLoaded, in which case the tour was
      // asked to start against a strip that did not exist yet. Start it here instead
      // of painting the held message, or a fast load would leave the reader with
      // step 1's text and no way to reach step 2.
      if (ready) { goStep(0); } else { label.textContent = pending; }
      paint();
    }

    function stepButton(id, testid, text, delta) {
      var b = document.createElement("button");
      b.id = id;
      b.type = "button";
      b.setAttribute("data-testid", testid);
      b.textContent = text;
      b.hidden = true;
      b.addEventListener("click", function () { goStep(step + delta, true); });
      return b;
    }

    // Drive the tour to step i, and -- when the reader asked for it -- drive the
    // dashboard with it. The route change is a real hash navigation, so the browser's
    // own Back button walks the tour backwards for free and every step is a URL the
    // reader can keep.
    //
    // `navigate` is what separates "the reader pressed Next" from "the strip is
    // painting itself for the first time". Only a press may move the route: the first
    // paint happens on whatever page the visitor opened, and a deep link into a
    // revision or a graph is a page they chose. Navigating there would throw it away
    // and drop them on the overview.
    function goStep(i, navigate) {
      if (i < 0 || i >= TOUR.length || !el) { return; }
      step = i;
      var s = TOUR[i];
      say(s.text);
      stepNum.textContent = (i + 1) + " / " + TOUR.length;
      stepNum.hidden = false;
      backBtn.hidden = i === 0;
      nextBtn.hidden = i === TOUR.length - 1;
      if (s.href) {
        moreLink.href = s.href;
        moreLink.textContent = s.linkText;
        moreLink.hidden = false;
      } else {
        moreLink.hidden = true;
      }
      if (navigate && s.hash && window.location.hash !== s.hash) { window.location.hash = s.hash; }
      // Moving focus to the strip keeps a keyboard reader with the tour after the app
      // re-renders the whole main region underneath it. Only on an explicit step
      // change, never on the first paint, or arriving at the demo would steal focus
      // from the page the reader is still reading.
      if (navigate) { el.setAttribute("tabindex", "-1"); el.focus(); }
    }

    function say(text) { pending = text; if (label) { label.textContent = text; } }

    // The gap keeps the last line of the page off the strip's own shadow rather than
    // flush against it. Cleared, not zeroed, once the strip is gone: the page owns its
    // padding again and this leaves no trace of having borrowed it.
    function reserve() {
      var h = el && !el.hidden ? el.getBoundingClientRect().height + 24 : 0;
      document.body.style.paddingBottom = h ? h + "px" : "";
    }

    if (document.readyState === "loading") {
      document.addEventListener("DOMContentLoaded", mount);
    } else {
      mount();
    }
    window.__pactoReady.then(function () {
      done();
      // Step 1 names the two deliberate oddities rather than only labelling the
      // fixture. The visitor who arrived from the home page's primary call to action
      // lands on a degraded-source banner and a config name that appears twice; told
      // nothing, they read both as a broken product and leave. The strip cannot hold
      // the explanation, but it can say the oddities are intended, put the page that
      // explains them one click away, and then walk the reader through what the rest
      // of the screens are for.
      //
      // goStep(0) rather than an auto-advancing or auto-opening tour: the reader came
      // to look at a dashboard, and a walkthrough that moves on its own takes the page
      // away from them. Nothing happens -- and nothing navigates -- until Next is
      // pressed.
      ready = true;
      if (el) { goStep(0); } else { say(TOUR[0].text); }
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
