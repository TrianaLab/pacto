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

  // Readiness gate: resolved once the engine in the worker is up and
  // __pactoServe is installed. API calls made before then queue on this promise.
  var resolveReady;
  window.__pactoReady = new Promise(function (resolve) { resolveReady = resolve; });

  // Status strip. The Svelte shell paints in about a second, but app.wasm is tens
  // of megabytes and every API call queues behind it, so on an ordinary connection
  // a visitor sees a dashboard whose content area is an empty heading for roughly
  // nine seconds. Nothing on the page said why, said the fleet is fabricated, or
  // offered a way back to the documentation this demo is linked from. One strip
  // answers all three, and it lives HERE rather than in the dashboard app because
  // none of it is true of a real deployment.
  (function demoStatusStrip() {
    var T = window.__pactoTour;
    var tours = window.__pactoTours;

    // The explainer, not the docs root. Two things about this fixture are deliberate
    // and read as bugs -- it opens on a degraded-source banner, and one config name
    // appears twice -- and that page's first paragraphs say so. The home page's
    // primary call to action jumps straight here, so this strip is the only place a
    // visitor who arrived that way is ever told. Relative, so it resolves under both
    // /demo/ and /<version>/demo/; the page it lands on carries the full docs nav.
    var DOCS_HREF = "../examples/dashboard-demo/";

    // The fixture disclosure; tours.js says why it is owed (the fleet tour opens on it).
    var NOTICE = tours.notice;

    // What the strip says on a delivery screen: the delivery tour is simulated.
    var DELIVERY_NOTICE = "Simulated delivery: no cluster and no promoter. Plans, checks and signatures are Pacto's own code on the payments story's fixed data (the health results and what the node agent saw are recorded too), records are signed with a key made in this tab, and what you do here is lost on reload.";

    // Every record in the tab. The delivery tour's copy describes the world the
    // replay leaves at load, so a tour started after anything moved it reloads
    // first: there is no way to undo a signed record in place.
    var WORLD_AT_LOAD = "";
    var REPLAY_KEY = "pacto-demo-delivery-tour";
    // Set by the ready path after a reload the tour asked for. The tour starts from
    // whichever of the ready path and mount() runs second, since either can.
    var replayPending = false;
    function replayTour() {
      if (ready && el && replayPending) { replayPending = false; startTour(tours.delivery); }
    }

    var el, label, meter, startBtn, deliveryBtn, sizeObs;
    // The tour bubble while a tour runs, which floats and so needs room (see reserve).
    var tourBubble = null;
    // Set when the engine resolves. mount() reads it because the two can happen in
    // either order, and only the ready path may offer the tour.
    var ready = false;
    // The strip mounts on DOMContentLoaded, but the engine can resolve or fail
    // before that. Hold the latest message so the outcome is never dropped.
    var pending = "Loading the Pacto engine. The panels fill in when it lands.";
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
        // A band across the top of the page, in the page's own flow: it never floats, so it
        // can never cover what the dashboard drew. It used to float over the foot of the
        // page, and on a short window that is exactly where the content was.
        "#pacto-demo-strip{position:relative;box-sizing:border-box;width:100%;" +
        "display:flex;gap:.6rem;align-items:center;" +
        "padding:.5rem .9rem;background:#1e293b;color:#e2e8f0;" +
        "font:400 .8125rem/1.4 system-ui,-apple-system,'Segoe UI',sans-serif}" +
        "#pacto-demo-strip a{color:#a5b4fc;text-decoration:underline;white-space:nowrap}" +
        "#pacto-demo-strip button{appearance:none;cursor:pointer;" +
        "font:inherit;line-height:1}" +
        "#pacto-demo-start{border:1px solid #a5b4fc;background:none;color:#c7d2fe;" +
        "border-radius:999px;padding:.25rem .8rem;white-space:nowrap}" +
        "#pacto-demo-start:hover{background:rgba(148,163,184,.2)}" +
        "#pacto-demo-start:focus-visible{outline:2px solid #a5b4fc;outline-offset:2px}" +
        // The strip wraps once the invitation is in it, so the text can no longer be
        // vertically centred against a single row of controls.
        "#pacto-demo-strip{flex-wrap:wrap;justify-content:center}" +
        "#pacto-demo-strip>span:first-child{flex:1 1 18rem;min-width:0}" +
        "#pacto-demo-meter{color:#94a3b8;font-variant-numeric:tabular-nums;white-space:nowrap}" +
        // display:flex above beats the hidden attribute, so say so explicitly or
        // dismissing the strip would do nothing at all.
        "#pacto-demo-strip[hidden]{display:none}" +
        "#pacto-demo-close{appearance:none;border:0;background:none;" +
        "color:#94a3b8;font:inherit;line-height:1;cursor:pointer;border-radius:999px;" +
        "padding:.2rem .4rem;margin:-.2rem -.3rem -.2rem 0}" +
        "#pacto-demo-close:hover{color:#e2e8f0;background:rgba(148,163,184,.2)}" +
        "#pacto-demo-strip a:focus-visible,#pacto-demo-close:focus-visible{outline:2px solid #a5b4fc;outline-offset:2px}" +
        "@media (max-width:480px){#pacto-demo-strip{font-size:.75rem;padding:.4rem .7rem}}" +
        "#pacto-demo-delivery{border:1px solid #a5b4fc;background:none;color:#c7d2fe;border-radius:999px;padding:.25rem .8rem;white-space:nowrap}" +
        "#pacto-demo-delivery:hover{background:rgba(148,163,184,.2)}" +
        "#pacto-demo-delivery:focus-visible{outline:2px solid #a5b4fc;outline-offset:2px}";
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

      // The invitation, and nothing more than an invitation. It appears only once the
      // engine has landed (a tour of an empty dashboard is a dead tour) and pressing it
      // is the ONLY thing that starts the tour, so a visitor who came to poke around is
      // never grabbed and the first paint is never allowed to navigate.
      startBtn = document.createElement("button");
      startBtn.id = "pacto-demo-start";
      startBtn.type = "button";
      startBtn.setAttribute("data-testid", "demo-tour-start");
      startBtn.textContent = "Take the guided tour";
      startBtn.hidden = true;
      startBtn.addEventListener("click", function () { startTour(tours.fleet); });

      // The delivery story is its own tour, offered beside the first one.
      deliveryBtn = document.createElement("button");
      deliveryBtn.id = "pacto-demo-delivery";
      deliveryBtn.type = "button";
      deliveryBtn.setAttribute("data-testid", "demo-delivery-start");
      deliveryBtn.textContent = "Play the delivery story";
      deliveryBtn.hidden = true;
      deliveryBtn.addEventListener("click", function () { startTour(tours.delivery); });

      var link = document.createElement("a");
      link.href = DOCS_HREF;
      link.textContent = "About this demo";
      // The strip is a notice, and a notice the reader has read takes space the
      // dashboard could use -- so give it the one control every persistent notice owes
      // the reader.
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
      el.appendChild(startBtn);
      el.appendChild(deliveryBtn);
      el.appendChild(link);
      el.appendChild(close);
      // First in the body, so it sits above the dashboard rather than over it.
      document.body.insertBefore(el, document.body.firstChild);
      // The tour bubble floats, so the page reserves its height; observed rather than
      // measured once, because each step's text wraps to a different number of lines.
      if (window.ResizeObserver) { sizeObs = new window.ResizeObserver(reserve); }
      // The engine can resolve before DOMContentLoaded, in which case `pending` already
      // holds the notice and the invitation is owed straight away. Painting the held
      // message covers both orderings with one line.
      label.textContent = pending;
      if (ready) { offerTour(); }
      paint();
      replayTour();
    }

    function offerTour() {
      if (startBtn) { startBtn.hidden = false; }
      if (deliveryBtn) { deliveryBtn.hidden = typeof window.__pactoPersona !== "function"; }
    }

    // On a delivery screen the strip says what is simulated there, and names the
    // key the tab signs with.
    function notice() {
      var delivery = window.location.hash.indexOf("#/delivery") === 0;
      say(delivery ? DELIVERY_NOTICE + " Signing key: " + (window.__pactoDeliveryKey || "none") + "." : NOTICE);
    }

    function say(text) { pending = text; if (label) { label.textContent = text; } }

    // The gap keeps the last line of the page off the floating box's own shadow rather
    // than flush against it. Cleared, not zeroed, once nothing is floating: the page owns
    // its padding again and this leaves no trace of having borrowed it.
    //
    // While the tour runs the bubble is the thing that floats, and it owes the page the
    // same clearance the strip does -- at 320px a coach mark parked at the foot of the
    // window sits on top of whatever the dashboard drew there, and a tour that makes the
    // control it is pointing at unreachable is worse than no tour.
    function reserve() {
      var box = tourBubble;
      var h = box ? box.getBoundingClientRect().height + 24 : 0;
      document.body.style.paddingBottom = h ? h + "px" : "";
    }

    function startTour(tour, worldChecked) {
      if (T.running() || !el) { return; }
      if (tour === tours.delivery && !worldChecked) {
        tours.world().then(function (w) {
          if (w !== WORLD_AT_LOAD) {
            try { window.sessionStorage.setItem(REPLAY_KEY, "1"); } catch (e) { /* the reader presses it again after load */ }
            window.location.reload();
            return;
          }
          // The steps say "as pay-agent": a reader who switched person first would
          // otherwise propose as Carol, who then may not approve her own change.
          return tours.setPersona("pay-agent", false).then(function () { startTour(tour, true); });
        });
        return;
      }
      T.start(tour, {
        docsHref: DOCS_HREF,
        onStart: function (bubble) {
          // One announcer at a time: the strip is a role=status live region and the bubble
          // is now the thing that speaks. It comes straight back on exit.
          el.hidden = true;
          tourBubble = bubble;
          if (sizeObs) { sizeObs.observe(bubble); }
          reserve();
        },
        onExit: function (bubble) {
          if (sizeObs) { sizeObs.unobserve(bubble); }
          tourBubble = null;
          el.hidden = false;
          reserve();
          startBtn.focus();
        }
      });
    }

    if (document.readyState === "loading") {
      document.addEventListener("DOMContentLoaded", mount);
    } else {
      mount();
    }
    window.__pactoReady.then(function () {
      done();
      // The notice names the two deliberate oddities rather than only labelling the
      // fixture. The visitor who arrived from the home page's primary call to action
      // lands on a degraded-source banner and a config name that appears twice; told
      // nothing, they read both as a broken product and leave. The strip cannot hold
      // the explanation, but it can say the oddities are intended and put the page that
      // explains them one click away.
      //
      // An invitation rather than a tour that opens itself: the reader came to look at a
      // dashboard, and a walkthrough that starts on its own takes the page away from
      // them. Nothing happens -- and nothing navigates -- until the button is pressed.
      ready = true;
      notice();
      window.addEventListener("hashchange", notice);
      offerTour();
      tours.world().then(function (w) {
        WORLD_AT_LOAD = w;
        try { replayPending = !!window.sessionStorage.getItem(REPLAY_KEY); window.sessionStorage.removeItem(REPLAY_KEY); } catch (e) { /* no storage, no replay */ }
        replayTour();
      });
    });
    window.__pactoDemoFailed = function () {
      done();
      say("The Pacto engine did not load, so every panel will stay empty. Try reloading.");
      // Every screen the tour visits is served by the engine, so with no engine there is
      // nothing to walk through.
      if (startBtn) { startBtn.hidden = true; }
      if (deliveryBtn) { deliveryBtn.hidden = true; }
      // A dismissed notice stays dismissed for anything the reader has already been
      // told. This is not that: without the strip, a failed load is a dashboard whose
      // every panel is permanently empty and nothing anywhere saying why.
      if (el) { el.hidden = false; }
    };
  })();

  // The engine runs in a Web Worker (engine.js), so its boot and its heavier
  // answers never stall the page: the skeletons keep moving and the counter keeps
  // counting. Calls go over postMessage and come back as promises; the worker
  // gets this script's own cache-busting query.
  var worker = new Worker(new URL("engine.js" + new URL(scriptURL).search, scriptURL));
  var calls = {};
  var nextCall = 0;
  function call(op, args) {
    return new Promise(function (resolve, reject) {
      var id = ++nextCall;
      calls[id] = { resolve: resolve, reject: reject };
      worker.postMessage({ id: id, op: op, args: args });
    });
  }
  function failed() { if (window.__pactoDemoFailed) { window.__pactoDemoFailed(); } }
  worker.onmessage = function (e) {
    var m = e.data;
    if (m.id) {
      var c = calls[m.id];
      delete calls[m.id];
      if ("error" in m) { c.reject(new Error(m.error)); } else { c.resolve(m.result); }
    } else if (m.type === "size") {
      if (window.__pactoDemoSize) { window.__pactoDemoSize(m.bytes); }
    } else if (m.type === "refresh") {
      window.dispatchEvent(new Event("pacto:refresh"));
    } else if (m.type === "ready") {
      window.__pactoDeliveryKey = m.deliveryKey;
      window.__pactoPactifyPlan = m.pactifyPlan;
      // __pactoPersona(name, fresh) resolves to "" or the reason it was refused.
      if (m.persona) { window.__pactoPersona = function (name, fresh) { return call("persona", [name, !!fresh]); }; }
      // __pactoServe(method, path, body?, headers?) resolves to { status, body,
      // contentType }. A test may wrap it; the fetch shim reads it at call time.
      window.__pactoServe = function (method, path, body, headers) { return call("serve", [method, path, body, headers || {}]); };
      resolveReady();
    } else if (m.type === "failed") {
      failed();
    }
  };
  worker.onerror = function (e) {
    console.error("Pacto engine failed to load:", e.message);
    failed();
  };

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
    // The request headers (names lowercased) ride along as a fourth argument.
    // The engine ignores them; a test that wraps __pactoServe reads them.
    var headers = {};
    new Headers(isRequest ? input.headers : undefined).forEach(function (v, k) { headers[k] = v; });
    new Headers(init.headers || {}).forEach(function (v, k) { headers[k] = v; });
    return window.__pactoReady
      .then(function () { return bodyPromise; })
      .then(function (body) {
        return window.__pactoServe(method, u.pathname + u.search, body && body.length ? body : null, headers);
      })
      .then(function (res) {
        return new Response(res.body, {
          status: res.status,
          headers: { "Content-Type": res.contentType || "application/json" },
        });
      });
  };
})();
