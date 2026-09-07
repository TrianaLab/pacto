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

    // The fixture disclosure. It is owed on load, before anything is pressed, because
    // the fast path from the home page's primary call to action never passes the
    // explainer: the fleet is fabricated, and two of its properties (a degraded-source
    // banner, a config name that appears twice) read as bugs and are deliberate. This
    // strip is the only place that visitor is ever told, so the notice is the strip's
    // resting state and the tour never takes it away -- it reappears the moment the
    // tour exits.
    var NOTICE = "Demo — a fixture fleet running entirely in your browser. Nothing here is a real system, and two things that look like bugs are deliberate.";

    // The walkthrough. A visitor who arrives from the home page's call to action gets a
    // dashboard of a fleet they have never seen, and no amount of labelling tells them
    // what any of it is FOR. So the strip offers a tour: six questions, each one driving
    // the real app to the screen that answers it. Nothing is simulated -- every step is a
    // route this dashboard already serves, read against the fixture.
    //
    // It is a HARD gate rather than a slideshow. A stepper whose text advances while the
    // reader watches teaches nothing; each step here names one thing to do and the tour
    // does not move until the app is in the state that proves it was done.
    //
    // And when it IS in that state, the tour moves ITSELF. The gate is an observation, so
    // a Next button after it asks the reader to confirm a thing the tour just watched them
    // do -- the step twice, once for real and once for the stepper. A step with no action
    // to observe (the opening notice, the closing hand-off) keeps a real button, because
    // there is nothing there to watch for and an auto-advance would flash past unread.
    // That is decided by whether the step HAS a gate, never by its number.
    //
    // Except where the gate IS the answer. On three of these steps the thing the reader
    // did produces the screen the step was about -- the service in full, the field-by-field
    // comparison that says Breaking, the neighborhood the change reaches -- and the step
    // after it navigates away. Moving on a second later shows them the breaking change and
    // then takes it away before it can be read, which is the one outcome worse than making
    // them press a button. So those steps HOLD: the tour still detects the action, still
    // says so, and then hands over a Continue the reader presses when they have finished
    // looking. The button is never a confirmation that the step happened -- the tour
    // already knows that -- only a way to say "I have read this".
    //
    // Every gated step still carries `skip`: a gate the reader cannot clear is a trap, so
    // skip performs the same action programmatically, landing them on the screen a
    // performer reaches rather than one behind it.
    //
    // Opt-in, never automatic: nothing below runs until "Take the guided tour" is
    // pressed. A visitor who wants to poke around is never grabbed, and -- because the
    // route change lives behind that press -- the first paint still never navigates.
    //
    // This lives in boot.js, which ships with the demo and nothing else, so a real
    // deployment cannot show it even by mistake -- there is no flag to set wrong.
    //
    // Per step: `hash` is the route the step needs (the last has none, it is about where
    // to go next rather than about a screen); `target` is what the spotlight cuts out,
    // and when it is a list the LAST selector that resolves wins, so the light follows
    // the action from a search box to the result it produced; `gate` is the predicate
    // that both proves the step was done and moves the tour on, so a step that HAS one
    // has no forward button at all; `hold` marks the gates whose result is the point, which
    // reveal a Continue instead of moving; `wait` is what an unsatisfied gate is waiting
    // for, because a tour that sits there wanting something it has not named is a wall.
    var TOUR = [{
      text: NOTICE + " Read that, then press Continue.",
      hash: "#/fleet",
      target: "#sec-attention"
    }, {
      text: "Step 2 — what is out there. Every service Pacto found, with its owner, how many contract revisions it has published and how many deployed targets are running them. All of it read from contracts. Type payments in the search box and press Enter.",
      hash: "#/fleet/services",
      target: '[data-testid="svc-search"]',
      wait: "Waiting for a search that narrows the list to payments-service.",
      gate: function () { return /[?&]text=[^&]/.test(window.location.hash) && !!paymentsLink(); },
      skip: function () {
        var i = q('[data-testid="svc-search"]');
        if (!i) { window.location.hash = "#/fleet/services?text=payments"; return; }
        setValue(i, "payments");
        // Blur first: the suggestion popup closes on blur, and left open it covers the
        // very list the next step asks the reader to click in.
        i.blur();
        if (i.form) { i.form.requestSubmit(); }
      }
    }, {
      text: "Step 3 — one service in full. What payments-service declares, every revision it has published and what was observed about the targets running it. Compliance has four states, and “not evaluated” is not one of the passing ones. Open payments-service from the list.",
      hash: "#/fleet/services",
      // The link, then -- once it has been followed and the link no longer exists -- the
      // summary it opened. Without the second entry the light goes out at the moment the
      // step asks the reader to look at something.
      target: ['[data-testid="service-list"] a[href$="/fleet/services/payments-service"]', "#sec-operational-summary"],
      wait: "Waiting for the payments-service page to open.",
      hold: true,
      gate: function () { return hashPath() === "#/fleet/services/payments-service"; },
      skip: function () {
        var a = paymentsLink();
        if (a) { a.click(); } else { window.location.hash = "#/fleet/services/payments-service"; }
      }
    }, {
      text: "Step 4 — about to ship a break. Two revisions compared field by field. Removing an API path is Breaking; adding an optional one is not. The classification rules are a published table, not a heuristic. Choose the earlier revision, then press Compare revisions.",
      hash: "#/fleet/changes/payments-service",
      // The whole form, so the two selectors and the submit sit inside one hole; the
      // reader is asked to use all three and a light on only one of them misleads. Then
      // the comparison itself: this is the step the whole tour is for, and leaving the
      // light on the form while the bubble says to read the verdict points at the wrong
      // half of the screen.
      target: ["#sec-revisions", '[data-testid="changes-what-changed"]'],
      wait: "Waiting for the comparison to run.",
      hold: true,
      gate: function () { return !!q('[data-testid="changes-what-changed"]'); },
      skip: function () {
        var s = q("#impact-old-rev");
        var f = q("#sec-revisions");
        // The oldest revision is the last option, and against the newest (already the
        // default on the right-hand selector) it is the pair that actually breaks.
        if (s && s.options.length > 1) { setValue(s, s.options[s.options.length - 1].value); }
        if (f) { f.requestSubmit(); }
      }
    }, {
      text: "Step 5 — who the change reaches. Edges a contract declares and edges something observed are kept apart rather than averaged, so a consumer that declared nothing still turns up. Search orders, then open the result.",
      hash: "#/fleet/graph",
      target: ['[data-testid="graph-discovery"] input[type=search]', '[data-testid="graph-focus-link"]', '[data-testid="neighborhood-canvas"]'],
      wait: "Waiting for a focused neighborhood to render.",
      hold: true,
      gate: function () { return !!q('[data-testid="neighborhood-canvas"]'); },
      skip: function () {
        var i = q('[data-testid="graph-discovery"] input[type=search]');
        if (i) { setValue(i, "orders"); }
        var a = q('[data-testid="graph-focus-link"]');
        // The results come back from a request, so on a step the reader skipped
        // immediately there is nothing to click yet: go where the link would have gone.
        if (a) { a.click(); } else { window.location.hash = "#/fleet/graph/service/orders-service"; }
      }
    }, {
      text: "That is the whole loop: what is out there, what state it is in, what a change breaks and who it reaches — from contracts plus observations, with nothing guessed.",
      href: "../examples/demo-tour/",
      linkText: "The same six questions from a terminal"
    }];

    var el, label, meter, startBtn, sizeObs;
    var overlay, spot, bubble, stepNum, textEl, hintEl, autoEl, backBtn, nextBtn, skipBtn, moreLink;
    var step = 0;
    var running = false;
    var raf = 0;
    // The element the spotlight is currently on. Kept so the tour scrolls a target into
    // view when it CHANGES rather than on every frame, which would fight the reader.
    var lastTarget = null;
    // Set when the engine resolves. mount() reads it because the two can happen in
    // either order, and only the ready path may offer the tour.
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
        // The invitation. Same pointer-events:auto rule as the link: the strip stays
        // transparent to taps meant for the dashboard under it, and only the things a
        // reader can actually press are pressable.
        "#pacto-demo-strip button{pointer-events:auto;appearance:none;cursor:pointer;" +
        "font:inherit;line-height:1}" +
        "#pacto-demo-start{border:1px solid #a5b4fc;background:none;color:#c7d2fe;" +
        "border-radius:999px;padding:.25rem .8rem;white-space:nowrap}" +
        "#pacto-demo-start:hover{background:rgba(148,163,184,.2)}" +
        "#pacto-demo-start:focus-visible{outline:2px solid #a5b4fc;outline-offset:2px}" +
        // The strip wraps once the invitation is in it, so the text can no longer be
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
        "font-size:.75rem;padding:.4rem .7rem}}" +
        // The tour overlay. Two elements, and the dimming is ONE declaration: a 100vmax
        // spread box-shadow on the cut-out is the dark layer and the hole in it at the
        // same time. An SVG mask or four dim panels would be more code for the same
        // picture and would have to be kept in sync with the rect. It ignores pointers,
        // so the control it highlights stays completely usable -- which is the whole
        // point of a tour that makes you do the step rather than watch it.
        "#pacto-tour-overlay{position:fixed;inset:0;z-index:10000;pointer-events:none}" +
        "#pacto-tour-spot{position:absolute;border-radius:10px;pointer-events:none;" +
        "box-shadow:0 0 0 100vmax rgba(15,23,42,.62);outline:2px solid #a5b4fc}" +
        "#pacto-tour-spot[hidden]{display:none}" +
        // box-sizing so the viewport clamp in place() is arithmetic on the real width.
        "#pacto-tour-bubble{position:absolute;pointer-events:auto;box-sizing:border-box;" +
        "width:min(92vw,26rem);background:#1e293b;color:#e2e8f0;border-radius:14px;" +
        "padding:.7rem .9rem;box-shadow:0 10px 30px rgba(15,23,42,.55);" +
        "font:400 .8125rem/1.45 system-ui,-apple-system,'Segoe UI',sans-serif}" +
        "#pacto-tour-bubble:focus-visible{outline:2px solid #a5b4fc;outline-offset:2px}" +
        "#pacto-tour-bubble a{color:#a5b4fc;text-decoration:underline}" +
        "#pacto-tour-step{display:block;color:#94a3b8;font-variant-numeric:tabular-nums}" +
        "#pacto-tour-text{display:block;margin-top:.25rem}" +
        "#pacto-tour-more{display:inline-block;margin-top:.45rem}" +
        // display:inline-block above beats the UA's [hidden] rule, so say so explicitly
        // or the hand-off link would sit in every step instead of only the last.
        "#pacto-tour-more[hidden]{display:none}" +
        // The gate's reason. Amber and in the flow rather than a tooltip: the reader has
        // to be able to see what the tour is waiting for without hovering anything.
        "#pacto-tour-hint{display:block;margin-top:.45rem;color:#fcd34d}" +
        "#pacto-tour-hint:empty{display:none}" +
        // The same line carries the confirmation once the gate has settled, and amber for
        // good news reads as a second warning, so that state is green.
        "#pacto-tour-hint.ok{color:#86efac}" +
        // What stands where the forward button does on the steps that have one. A gated
        // step has no button, and an empty gap where a control was is a reader waiting for
        // something that is never coming -- so the row says, in the row, that it will move
        // by itself. Its own full-width line rather than squeezed between Back and Skip:
        // at 26rem there is not enough left over for a sentence, and a control row three
        // text lines tall to fit one reads worse than an extra line does.
        "#pacto-tour-auto{flex:1 1 100%;color:#94a3b8}" +
        // The fixture disclosure, kept reachable while the strip that usually carries it
        // is hidden. Quiet and last: it is a way out, not a step.
        "#pacto-tour-about{display:inline-block;margin-top:.5rem;font-size:.75rem}" +
        "#pacto-tour-ctl{display:flex;flex-wrap:wrap;gap:.4rem;margin-top:.6rem}" +
        "#pacto-tour-ctl button{appearance:none;cursor:pointer;font:inherit;line-height:1;" +
        "border:1px solid #475569;background:none;color:#e2e8f0;border-radius:999px;" +
        "padding:.3rem .7rem;white-space:nowrap}" +
        "#pacto-tour-ctl button:hover:not(:disabled){background:rgba(148,163,184,.2)}" +
        "#pacto-tour-ctl button:focus-visible{outline:2px solid #a5b4fc;outline-offset:2px}" +
        "#pacto-tour-ctl button:disabled{cursor:not-allowed;opacity:.45}" +
        "#pacto-tour-next{border-color:#a5b4fc;color:#c7d2fe}" +
        "#pacto-tour-exit{margin-left:auto}";
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
      startBtn.addEventListener("click", startTour);

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
      el.appendChild(startBtn);
      el.appendChild(link);
      el.appendChild(close);
      document.body.appendChild(el);
      // The strip floats over the foot of the page, and its controls are the part of
      // it that must stay clickable -- which at phone widths means they sit on top of
      // whatever the dashboard drew there and swallow taps meant for it.
      // pointer-events cannot fix that: a button that ignores pointers is not a
      // button. Reserving the strip's own height at the end of the document lets the
      // page scroll clear of it instead, so nothing underneath is ever unreachable.
      // Observed rather than measured once: the height changes with the message, because
      // each one wraps to a different number of lines.
      if (window.ResizeObserver) { sizeObs = new window.ResizeObserver(reserve); sizeObs.observe(el); }
      // The engine can resolve before DOMContentLoaded, in which case `pending` already
      // holds the notice and the invitation is owed straight away. Painting the held
      // message covers both orderings with one line.
      label.textContent = pending;
      if (ready) { offerTour(); }
      paint();
    }

    function offerTour() { if (startBtn) { startBtn.hidden = false; } }

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
      var box = running ? bubble : (el && !el.hidden ? el : null);
      var h = box ? box.getBoundingClientRect().height + 24 : 0;
      document.body.style.paddingBottom = h ? h + "px" : "";
    }

    // ---- the guided tour -------------------------------------------------------

    function q(sel) { return document.querySelector(sel); }
    // The route WITHOUT its query. A step's own action can add one -- step 2 commits
    // `?text=payments` -- and the step after it lives on the same screen, so comparing
    // the whole hash would navigate back and throw the reader's own search away.
    function hashPath() { return window.location.hash.split("?")[0]; }
    // Anchored on the end of the href so a same-named service in another domain (this
    // fixture publishes one) cannot satisfy a gate meant for the default-domain service.
    function paymentsLink() { return q('[data-testid="service-list"] a[href$="/fleet/services/payments-service"]'); }

    // Svelte 5 binds on the EVENT, not on the property: assigning .value alone leaves the
    // component's own state holding the old value, so the submit that follows sends the
    // old one and the skip silently does nothing. Both events, because a text input binds
    // on `input` and this app's revision selectors commit on `change`.
    function setValue(node, value) {
      node.value = value;
      node.dispatchEvent(new Event("input", { bubbles: true }));
      node.dispatchEvent(new Event("change", { bubbles: true }));
    }

    // A step may name several targets; the LAST one that resolves wins, so the spotlight
    // follows the action from a search box to the result it produced without the step
    // needing to know when that happened.
    function resolve(sel) {
      if (!sel) { return null; }
      var list = typeof sel === "string" ? [sel] : sel;
      var found = null;
      for (var i = 0; i < list.length; i++) {
        var e = q(list[i]);
        if (e) { found = e; }
      }
      return found;
    }

    function clamp(v, lo, hi) { return hi < lo ? lo : (v < lo ? lo : (v > hi ? hi : v)); }

    var SPOT_PAD = 6;
    var BUBBLE_GAP = 12;
    // The dim IS the cut-out: it is drawn by a spread shadow on the hole, so a hole the
    // size of the window leaves the shadow nowhere to fall and the page is not dimmed at
    // all. At 320px wide, step 1's section is twice the height of the window and starts
    // above it, which is exactly that case -- an undimmed page with a stray outline round
    // it. So the hole is the target's VISIBLE part, and when that still runs the whole way
    // across an axis it keeps this much rim, because a rim is what says the page is held.
    var DIM_RIM = 16;

    // Put the cut-out over the target and the bubble beside it: below when there is room
    // and above otherwise, then clamped into the viewport so it is never half off-screen
    // at 320px. clientWidth/clientHeight rather than innerWidth/innerHeight because a
    // classic scrollbar is not addressable space for a fixed box.
    function place(t) {
      var vw = document.documentElement.clientWidth;
      var vh = document.documentElement.clientHeight;
      var b = bubble.getBoundingClientRect();
      var left, top;
      // The hole is the part of the target that is ON SCREEN, never the raw rect: a rect
      // that overhangs the window makes a hole bigger than the window, and a hole bigger
      // than the window has no dim in it at all.
      var x0 = 0, x1 = 0, y0 = 0, y1 = 0;
      if (t) {
        var r = t.getBoundingClientRect();
        x0 = clamp(r.left - SPOT_PAD, 0, vw);
        x1 = clamp(r.right + SPOT_PAD, 0, vw);
        y0 = clamp(r.top - SPOT_PAD, 0, vh);
        y1 = clamp(r.bottom + SPOT_PAD, 0, vh);
        // A target that reaches both edges of an axis (step 1's section on a phone) would
        // leave that axis undimmed, so keep the rim. Only ever narrows the hole, and only
        // for a target already bigger than the window, so a target that fits is untouched.
        if (x1 - x0 >= vw) { x0 = DIM_RIM; x1 = vw - DIM_RIM; }
        if (y1 - y0 >= vh) { y0 = DIM_RIM; y1 = vh - DIM_RIM; }
      }
      if (!t || x1 <= x0 || y1 <= y0) {
        // Pointing at nothing is worse than not pointing: with no target -- or a target
        // scrolled clean off the window, which is a hole of no area and would draw as a
        // stray line on an edge -- the light goes out and the bubble takes the middle of
        // the screen until the app renders it.
        spot.hidden = true;
        left = (vw - b.width) / 2;
        top = (vh - b.height) / 2;
      } else {
        spot.hidden = false;
        spot.style.left = x0 + "px";
        spot.style.top = y0 + "px";
        spot.style.width = (x1 - x0) + "px";
        spot.style.height = (y1 - y0) + "px";
        // Beside the HOLE, not beside the raw rect: with a target taller than the window
        // the raw bottom is off-screen and the bubble would be flung to a corner.
        left = x0;
        top = y1 + BUBBLE_GAP + b.height <= vh - BUBBLE_GAP
          ? y1 + BUBBLE_GAP
          : y0 - BUBBLE_GAP - b.height;
      }
      bubble.style.left = Math.round(clamp(left, BUBBLE_GAP, vw - b.width - BUBBLE_GAP)) + "px";
      bubble.style.top = Math.round(clamp(top, BUBBLE_GAP, vh - b.height - BUBBLE_GAP)) + "px";
    }

    // Only when it is actually off-screen, and only when the target CHANGES: a tour that
    // re-scrolls every frame takes the page away from the reader mid-drag.
    function ensureVisible(t) {
      var r = t.getBoundingClientRect();
      if (r.top < 0 || r.bottom > document.documentElement.clientHeight) { t.scrollIntoView({ block: "center" }); }
    }

    // What the live region says once the gate has settled. It names the move as well as
    // the result: a view that changes with no warning is disorienting to everyone and a
    // WCAG failure for a reader who cannot watch it happen.
    var GATE_OPEN = "Done — moving on.";
    // The same observation on a step that holds, which has to say the opposite thing: the
    // tour saw the action and is deliberately NOT taking the screen away. Announcing the
    // stay matters as much as announcing a move -- a reader who cannot see the result
    // otherwise has no way to know the tour is now waiting on them.
    var HOLD_OPEN = "Done — that is the answer. Read it, then press Continue.";

    // Three numbers stand between the gate reporting satisfied and the view changing.
    //
    // SETTLE_MS -- the gate must HOLD, with no keystroke and no pointer press, for this
    //   long. Step 2 asks for a typed search, and its gate is open as soon as one
    //   committed character narrows the list; firing on that yanks the reader away
    //   mid-word. The quiet period runs from the later of the gate opening and the
    //   reader's last input, so someone still typing is never interrupted.
    // CONFIRM_MS -- how long the bubble says so before the screen moves. Without it the
    //   view changes at the same instant the reader finishes, and nothing connects what
    //   they did to what it produced.
    // SKIP_WAIT -- the bound on skip. Skip fires the step's action and then lets the same
    //   gate everything else uses do the moving, because every action here is
    //   asynchronous: a submit that runs an analysis and only then writes the URL, a link
    //   click that loads a neighborhood. Move the route out from under one and the app's
    //   own late write lands on the screen AFTER it, leaving the URL describing a page
    //   nobody is on. But a gate that can never open must not strand the reader behind a
    //   button that now looks broken, so when this deadline passes the tour goes anyway.
    var SETTLE_MS = 750;
    var CONFIRM_MS = 350;
    var SKIP_WAIT = 6000;
    var skipUntil = 0;
    // The gate has been seen CLOSED on this visit to the step, so an open one from here is
    // something the reader DID rather than a state they walked in on. Stepping Back onto a
    // step whose result is still on screen would otherwise bounce straight forward again.
    var armed = false;
    var openSince = 0;
    var confirmAt = 0;
    // The reader's last keystroke or pointer press, anywhere on the page. Capture phase so
    // a widget that stops propagation cannot hide the fact that someone is mid-action.
    var lastInput = 0;
    function noteInput() { lastInput = Date.now(); }

    // A monotonic id for one VISIT to one step, and the visit that has already spent
    // itself. Skip fires the step's own action, which satisfies the very gate the
    // auto-advance is watching -- so without this the reader who pressed Skip would be
    // carried two steps and lose the one they paid a button press for. Keyed on the visit
    // rather than the step index because Back returns to a step that has already advanced
    // once and must be allowed to advance again.
    var visit = 0;
    var leftVisit = -1;

    // The ONLY way the tour moves forward. Every path -- the settled gate, the skip
    // deadline, the Continue button -- comes through here, so "this step has already
    // handed off" is one fact in one place rather than a race between three of them.
    function advance() {
      if (!running || leftVisit === visit) { return; }
      leftVisit = visit;
      goStep(step + 1);
    }

    function skipStep() {
      // Already waiting: a second press would fire the action again underneath the first.
      if (skipUntil) { return; }
      var s = TOUR[step];
      if (s.skip) { s.skip(); }
      skipUntil = Date.now() + SKIP_WAIT;
      skipBtn.disabled = true;
    }

    // ponytail: one rAF loop instead of scroll + resize + MutationObserver + a per-gate
    // subscription. The app renders asynchronously and every gate watches a different
    // thing, so the listener version is four sources of truth that each have to be torn
    // down; re-reading three rects and one predicate per frame is cheap, always correct
    // and has exactly one cancel. Upgrade path if it ever costs anything: throttle to
    // every other frame, or subscribe once hashchange + ResizeObserver cover the gates.
    function tick() {
      if (!running) { return; }
      raf = window.requestAnimationFrame(tick);
      var s = TOUR[step];
      var t = resolve(s.target);
      if (t !== lastTarget) {
        lastTarget = t;
        if (t) { ensureVisible(t); }
      }
      place(t);
      if (!s.gate) { return; }
      var now = Date.now();
      var open = s.gate();
      if (!open) {
        // Seeing it shut is what arms the step. Everything downstream is then measuring a
        // transition the reader caused, never a state that was already true.
        armed = true;
        openSince = 0;
        confirmAt = 0;
      } else if (armed && !openSince) {
        openSince = now;
      }
      if (openSince && !confirmAt && now - openSince >= SETTLE_MS && now - lastInput >= SETTLE_MS) {
        confirmAt = now;
      }
      // Guarded because the hint is a live region: writing the same string sixty times a
      // second would announce it sixty times a second.
      //
      // It flips only once the gate has SETTLED, not the moment it opens: a green "Done"
      // that appears while someone is still typing is a lie the tour then has to take
      // back. And it says something rather than emptying the region -- clearing it
      // announces nothing at all, so the reader who cannot see the screen would be the one
      // told least about the only thing that happens on the step.
      var msg = confirmAt ? (s.hold ? HOLD_OPEN : GATE_OPEN) : s.wait;
      if (hintEl.textContent !== msg) {
        hintEl.textContent = msg;
        hintEl.className = confirmAt ? "ok" : "";
      }
      // A step whose gate produced the answer hands over the forward control rather than
      // the screen. Guarded on the button still being hidden so this is one flip per visit
      // and not a write every frame.
      if (confirmAt && s.hold) {
        if (nextBtn.hidden) {
          nextBtn.hidden = false;
          // Both of these belong to a step that has not happened yet. The action is done,
          // so an offer to perform it is noise; and the skip deadline was only ever a floor
          // under a gate that never opens -- left armed it would move the reader off the
          // result six seconds later, which is the whole defect this branch exists to fix.
          skipBtn.hidden = true;
          skipUntil = 0;
        }
        return;
      }
      // One move per frame. The confirmation coming due and the skip deadline expiring can
      // land on the same tick, and both of them mean the same thing.
      if (confirmAt && now - confirmAt >= CONFIRM_MS) { advance(); return; }
      // A skip whose gate never opened (see SKIP_WAIT). The gate itself needs no branch
      // here: it advances the tour for the reader who did the step and for the reader who
      // pressed Skip by exactly the same path.
      if (skipUntil && now > skipUntil) { advance(); }
    }

    function ctlButton(id, testid, text, fn) {
      var b = document.createElement("button");
      b.id = id;
      b.type = "button";
      b.setAttribute("data-testid", testid);
      b.textContent = text;
      b.addEventListener("click", fn);
      return b;
    }

    function buildOverlay() {
      overlay = document.createElement("div");
      overlay.id = "pacto-tour-overlay";
      overlay.setAttribute("data-testid", "demo-tour-overlay");

      spot = document.createElement("div");
      spot.id = "pacto-tour-spot";
      spot.setAttribute("data-testid", "demo-tour-spotlight");
      spot.hidden = true;

      bubble = document.createElement("div");
      bubble.id = "pacto-tour-bubble";
      bubble.setAttribute("data-testid", "demo-tour-bubble");
      // role=dialog and NOT aria-modal: the page behind stays live on purpose, because
      // the reader is meant to operate the thing in the spotlight. The step text is the
      // description, so moving focus here on a step change reads the name and the step
      // in one go -- which is also why the text carries no aria-live of its own, and why
      // the strip (role=status) is hidden for the duration. One announcer at a time.
      bubble.setAttribute("role", "dialog");
      bubble.setAttribute("aria-label", "Guided tour");
      // The step text plus, on a step with no button, the line saying none is coming. A
      // hidden element contributes nothing to a description, so on the two steps that DO
      // have a button the second id costs nothing and says nothing.
      bubble.setAttribute("aria-describedby", "pacto-tour-text pacto-tour-auto");
      bubble.setAttribute("tabindex", "-1");
      // Escape exits the tour, and it is bound HERE rather than on the document because
      // the tour does not own this key -- the app does. The dashboard's nav drawer, its
      // command palette and every type=search field all close or clear on Escape, and a
      // document-level handler tore the whole tour down with each of them. Step 5 asks the
      // reader to type into a search box, so the tour was telling them to do the thing
      // that killed it. On the bubble it fires only while focus is inside the bubble,
      // which every step change puts it back into, so the affordance survives intact.
      bubble.addEventListener("keydown", function (e) { if (e.key === "Escape") { exitTour(); } });

      stepNum = document.createElement("span");
      stepNum.id = "pacto-tour-step";
      stepNum.setAttribute("data-testid", "demo-tour-step");

      textEl = document.createElement("span");
      textEl.id = "pacto-tour-text";
      textEl.setAttribute("data-testid", "demo-tour-text");

      // Only the last step has somewhere further to send the reader, so this anchor is
      // separate from the strip's "About this demo" rather than a mutation of it: the
      // explainer link is owed the whole time, and swapping its target would take it away.
      moreLink = document.createElement("a");
      moreLink.id = "pacto-tour-more";
      moreLink.setAttribute("data-testid", "demo-tour-more");
      moreLink.hidden = true;

      hintEl = document.createElement("span");
      hintEl.id = "pacto-tour-hint";
      hintEl.setAttribute("aria-live", "polite");

      // Shown on exactly the steps that have no forward button, which is where a reader
      // would otherwise stand waiting for one. It is not a live region: it does not
      // change, and every step change reads it out as part of the bubble's description.
      autoEl = document.createElement("span");
      autoEl.id = "pacto-tour-auto";
      autoEl.setAttribute("data-testid", "demo-tour-auto");
      autoEl.textContent = "No button needed — the tour moves on when you do it.";

      backBtn = ctlButton("pacto-tour-back", "demo-tour-back", "Back", function () { goStep(step - 1); });
      // One forward control, and it exists only where the tour cannot see the step happen.
      // On the opening notice that is Continue; on the terminal hand-off it is Done, and
      // since there is no step seven, finishing IS leaving -- a Done that did nothing would
      // be worse than no Done at all. Through advance() like everything else, so the
      // one-move-per-visit rule holds for the button too.
      nextBtn = ctlButton("pacto-tour-next", "demo-tour-next", "Continue", function () {
        if (step === TOUR.length - 1) { exitTour(); } else { advance(); }
      });
      skipBtn = ctlButton("pacto-tour-skip", "demo-tour-skip", "Skip step", skipStep);
      var exitBtn = ctlButton("pacto-tour-exit", "demo-tour-exit", "Exit tour", exitTour);

      var ctl = document.createElement("div");
      ctl.id = "pacto-tour-ctl";
      ctl.appendChild(autoEl);
      ctl.appendChild(backBtn);
      ctl.appendChild(nextBtn);
      ctl.appendChild(skipBtn);
      ctl.appendChild(exitBtn);

      // The fixture disclosure is owed for the whole visit, and the strip that normally
      // carries it is hidden while the tour speaks -- so from step 2 on, the reader had no
      // way left to reach the page that says this fleet is invented and that two of its
      // oddities are deliberate. The bubble carries the same link, to the same place, on
      // every step; the last step's hand-off to the CLI tour is a different destination
      // and stays a separate anchor.
      var about = document.createElement("a");
      about.id = "pacto-tour-about";
      about.setAttribute("data-testid", "demo-tour-about");
      about.href = DOCS_HREF;
      about.textContent = "About this demo";

      bubble.appendChild(stepNum);
      bubble.appendChild(textEl);
      bubble.appendChild(moreLink);
      bubble.appendChild(hintEl);
      bubble.appendChild(ctl);
      bubble.appendChild(about);
      overlay.appendChild(spot);
      overlay.appendChild(bubble);
      document.body.appendChild(overlay);
    }

    // Drive the tour to step i and, if the app is not already on that screen, drive the
    // dashboard with it. Allowed to navigate at all only because nothing reaches here
    // until "Take the guided tour" was pressed -- the first paint still never moves the
    // route, so a deep link into a revision or a graph is still the page the visitor
    // chose.
    function goStep(i) {
      if (i < 0 || i >= TOUR.length || !running) { return; }
      step = i;
      visit++;
      lastTarget = null;
      // A fresh visit has observed nothing yet, so it cannot possibly be finished. This is
      // also what makes a same-tick double advance impossible: the step we just arrived on
      // has no armed gate and no skip deadline to trip over.
      armed = false;
      openSince = 0;
      confirmAt = 0;
      var s = TOUR[i];
      stepNum.textContent = (i + 1) + " / " + TOUR.length;
      textEl.textContent = s.text;
      backBtn.disabled = i === 0;
      // A gate is an observation, so a step that has one needs no button and gets the line
      // that says so instead; a step with nothing to observe keeps a real control. Decided
      // by the step, never by its number -- the only thing the number decides is the word
      // on the button, because the last step has nowhere further to send anyone.
      // A gated step arrives with no forward button either way. On a holding one tick()
      // reveals it once the gate settles; on the rest it never appears, because the tour
      // will have moved by then.
      nextBtn.hidden = !!s.gate;
      nextBtn.textContent = i === TOUR.length - 1 ? "Done" : "Continue";
      // Only where no button is coming at all. On a holding step one is, so promising
      // otherwise would be a promise the tour breaks a moment later.
      autoEl.hidden = !s.gate || !!s.hold;
      // Nothing to skip on an ungated step: there is a live button there already, and a
      // second control that does the same thing is a choice the reader has to stop and make.
      skipBtn.hidden = !s.gate;
      // Whatever a skip was waiting for, it was waiting for the step we just left.
      skipUntil = 0;
      skipBtn.disabled = false;
      hintEl.textContent = s.gate ? s.wait : "";
      hintEl.className = "";
      if (s.href) {
        moreLink.href = s.href;
        moreLink.textContent = s.linkText;
        moreLink.hidden = false;
      } else {
        moreLink.hidden = true;
      }
      if (s.hash && hashPath() !== s.hash) { window.location.hash = s.hash; }
      place(resolve(s.target));
      // Keeps a keyboard reader with the tour after the app re-renders the whole main
      // region underneath it.
      bubble.focus();
    }

    function startTour() {
      if (running || !el) { return; }
      running = true;
      // One announcer at a time: the strip is a role=status live region and the bubble is
      // about to become the thing that speaks. It comes straight back on exit.
      el.hidden = true;
      buildOverlay();
      if (sizeObs) { sizeObs.observe(bubble); }
      // On the document rather than on anything the tour owns: the action a step asks for
      // happens in the APP, so the only place that sees the reader still working is the
      // page as a whole. keydown and pointerdown only -- they are what "still doing
      // something" means, and unlike `input` no component synthesizes them while it
      // renders, so nothing the app does to itself can hold the tour still.
      document.addEventListener("keydown", noteInput, true);
      document.addEventListener("pointerdown", noteInput, true);
      goStep(0);
      reserve();
      raf = window.requestAnimationFrame(tick);
    }

    // Leaves the reader wherever they got to -- a tour that undoes the visitor's own
    // navigation on the way out has taken something from them.
    function exitTour() {
      if (!running) { return; }
      running = false;
      // A rAF loop that outlives the tour is a leak that keeps measuring elements the
      // reader can no longer see.
      window.cancelAnimationFrame(raf);
      if (sizeObs) { sizeObs.unobserve(bubble); }
      // The Escape handler goes with the bubble it is bound to; these two are the only
      // things the tour ever put on the document, and a tour that is over has no business
      // still watching what the reader types.
      document.removeEventListener("keydown", noteInput, true);
      document.removeEventListener("pointerdown", noteInput, true);
      overlay.parentNode.removeChild(overlay);
      overlay = spot = bubble = null;
      lastTarget = null;
      skipUntil = 0;
      armed = false;
      openSince = 0;
      confirmAt = 0;
      el.hidden = false;
      reserve();
      startBtn.focus();
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
      say(NOTICE);
      offerTour();
    });
    window.__pactoDemoFailed = function () {
      done();
      say("The Pacto engine did not load, so every panel will stay empty. Try reloading.");
      // Every screen the tour visits is served by the engine, so with no engine there is
      // nothing to walk through.
      if (startBtn) { startBtn.hidden = true; }
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
  instantiate(new URL("app.wasm?v=0b05f4db6d1a", scriptURL), go.importObject)
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
