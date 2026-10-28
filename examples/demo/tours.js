// tours.js — the demo's two guided tours as data, and the simulated delivery world
// their gates read: the engine's answers, the person signed in and the sign-in
// dialog the dashboard opens instead of leaving for a real login. tour.js runs them;
// boot.js offers them.
//
// Injected as a classic script after tour.js and before boot.js, and shipped with
// the demo and nothing else. It defines one page global, window.__pactoTours.
(function () {
  "use strict";

  var q = window.__pactoTour.q;
  var hashPath = window.__pactoTour.hashPath;

  var style = document.createElement("style");
  style.textContent =
    "#pacto-signin{max-width:min(92vw,28rem);border:1px solid #475569;border-radius:14px;background:#1e293b;color:#e2e8f0;" +
    "font:400 .875rem/1.45 system-ui,-apple-system,'Segoe UI',sans-serif;padding:1rem 1.1rem}" +
    "#pacto-signin::backdrop{background:rgba(15,23,42,.62)}" +
    "#pacto-signin h2{font-size:1rem;margin:0 0 .5rem}" +
    "#pacto-signin p{margin:.5rem 0}" +
    "#pacto-signin button{appearance:none;cursor:pointer;font:inherit;text-align:left;width:100%;border:1px solid #475569;" +
    "background:none;color:#e2e8f0;border-radius:10px;padding:.45rem .7rem}" +
    "#pacto-signin button[aria-current]{border-color:#a5b4fc}" +
    "#pacto-signin button:hover{background:rgba(148,163,184,.2)}" +
    "#pacto-signin button:focus-visible{outline:2px solid #a5b4fc;outline-offset:2px}";
  document.head.appendChild(style);

  // The fixture disclosure. It is owed on load, before anything is pressed, because
  // the fast path from the home page's primary call to action never passes the
  // explainer: the fleet is fabricated, and two of its properties (a degraded-source
  // banner, a config name that appears twice) read as bugs and are deliberate. This
  // strip is the only place that visitor is ever told, so the notice is the strip's
  // resting state and the tour never takes it away -- it reappears the moment the
  // tour exits.
  var NOTICE = "Demo: a fixture fleet running entirely in your browser. All of its data is made up, and two things that look like bugs are deliberate.";

  // The walkthrough. A visitor who arrives from the home page's call to action gets a
  // dashboard of a fleet they have never seen, and no amount of labelling tells them
  // what any of it is FOR. So the strip offers a tour: a few questions, one action per step, each driving
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
  // This lives in tours.js, which ships with the demo and nothing else, so a real
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
  var FLEET_TOUR = [{
    text: NOTICE + " Read that, then press Continue.",
    hash: "#/fleet",
    target: "#ov-answer"
  }, {
    text: "What is out there: every service Pacto found in its contracts, with its owner and what runs it, so type payments in the search box and press Enter.",
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
    text: "One service in full: what payments-service declares, every revision it has published and what was observed about the targets running it, so open payments-service from the list.",
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
    text: "About to ship a break: two revisions of payments-service are compared field by field, so first set Earlier revision to the oldest one, 1.0.0.",
    hash: "#/fleet/changes/payments-service",
    target: "#impact-old-rev",
    wait: "Waiting for the oldest revision to be chosen as the earlier one.",
    gate: function () { return oldestChosen(); },
    skip: function () {
      var s = q("#impact-old-rev");
      // The oldest revision is the last option, and against the newest (already the
      // default on the right-hand selector) it is the pair that actually breaks.
      if (s && s.options && s.options.length > 1) { setValue(s, s.options[s.options.length - 1].value); }
    }
  }, {
    text: "Now press Compare revisions: removing an API path is breaking and adding an optional one is not, and the answer says which this is.",
    hash: "#/fleet/changes/payments-service",
    // The button, then the comparison it produced: this is the step the whole tour is
    // for, and leaving the light on the form while the bubble says to read the verdict
    // points at the wrong half of the screen.
    target: ['#sec-revisions button[type=submit]', '[data-testid="changes-answer"]'],
    wait: "Waiting for the comparison to run.",
    hold: true,
    gate: function () { return !!q('[data-testid="changes-answer"]'); },
    skip: function () {
      var f = q("#sec-revisions");
      if (f) { f.requestSubmit(); }
    }
  }, {
    text: "Who the change reaches: links a contract declares and links Pacto observed are kept apart, so a consumer that declared nothing still shows up, and you start by typing orders in the search box.",
    hash: "#/fleet/graph",
    target: '[data-testid="graph-discovery"] input[type=search]',
    wait: "Waiting for a search that finds orders.",
    gate: function () { return !!q('[data-testid="graph-focus-link"]'); },
    skip: function () {
      var i = q('[data-testid="graph-discovery"] input[type=search]');
      if (i) { setValue(i, "orders"); } else { window.location.hash = "#/fleet/graph/service/orders-service"; }
    }
  }, {
    text: "Open orders-service from the results to see its neighborhood: what it calls, what calls it and what was only observed.",
    hash: "#/fleet/graph",
    target: ['[data-testid="graph-focus-link"]', '[data-testid="neighborhood-canvas"]'],
    wait: "Waiting for a focused neighborhood to render.",
    hold: true,
    gate: function () { return !!q('[data-testid="neighborhood-canvas"]'); },
    skip: function () {
      var a = q('[data-testid="graph-focus-link"]');
      // The results come back from a request, so on a step the reader skipped
      // immediately there is nothing to click yet: go where the link would have gone.
      if (a) { a.click(); } else { window.location.hash = "#/fleet/graph/service/orders-service"; }
    }
  }, {
    text: "You have now seen what runs, what state it is in and who a change reaches, all from contracts and observations.",
    href: "../examples/demo-tour/",
    linkText: "The same questions from a terminal"
  }];


  // ---- the delivery story -----------------------------------------------------
  //
  // A second, opt-in tour: the payments story (examples/story) in two acts --
  // pactify a plain service, then what that unlocks -- as five moments, with the
  // rest under "explore more". Unlike the fleet tour, this one IS simulated: there
  // is no cluster and no promoter, so the strip says so on every delivery screen.
  // Its gates read the engine's own answers rather than the DOM, through the same
  // __pactoServe the dashboard calls, so a gate means the story moved, not that a
  // label rendered. The engine answers from its worker, so every read is a promise.

  function serve(method, path, body) {
    if (typeof window.__pactoServe !== "function") { return Promise.resolve(null); }
    return Promise.resolve(window.__pactoServe(method, path, body ? JSON.stringify(body) : null, {})).then(function (r) {
      try { return { status: r.status, body: JSON.parse(r.body) }; } catch (e) { return { status: r.status, body: null }; }
    });
  }
  function refreshViews() { window.dispatchEvent(new Event("pacto:refresh")); }
  // Only the histories, and without any asOf: the clock and the names of who signed
  // in are not records, and with them no two reads a second or a sign-in apart would
  // ever compare equal.
  function world() {
    return serve("GET", "/api/delivery/history?limit=200").then(function (h) {
      return JSON.stringify(h && h.body && h.body.histories, function (k, v) { return k === "asOf" ? undefined : v; });
    });
  }
  function digestOf(svc, version) {
    return serve("GET", "/api/delivery/environments").then(function (r) {
      var rows = (r && r.body && r.body.environments) || [];
      for (var i = 0; i < rows.length; i++) {
        var row = rows[i];
        if (row.service === svc && row.candidateVersion === version) { return row.candidate; }
      }
      return "";
    });
  }

  // The facts the gates read. A gate runs every frame and cannot wait, so it
  // reads the last answer while a fresh one is asked for, at most twice a second.
  function noFacts() { return { at: 0, rows: {}, held: "", question: "", latest: "", accepted: false }; }
  var facts = noFacts();
  var factsAsked = 0;
  function fact() {
    var now = Date.now();
    if (now - facts.at < 500 || now - factsAsked < 500) { return facts; }
    factsAsked = now;
    var f = noFacts();
    Promise.all([
      serve("GET", "/api/delivery/environments"),
      serve("GET", "/api/delivery/history?namespace=prod&stage=payments")
    ]).then(function (r) {
      var env = r[0], h = r[1];
      ((env && env.body && env.body.environments) || []).forEach(function (row) { f.rows[row.environment + "/" + row.service] = row; });
      var entries = (h && h.body && h.body.histories && h.body.histories[0] && h.body.histories[0].entries) || [];
      // Newest first: the first promote is the latest one.
      entries.forEach(function (e) {
        if (e.type === "delivery.pacto.trianalab.io/promote" && e.operation && !f.latest) { f.latest = e.operation; f.accepted = e.verification === "AcceptedUnknown"; }
        if (e.phase === "Held" && !f.held) { f.held = e.operation; }
      });
      return f.held ? serve("GET", "/api/delivery/operations/prod/" + f.held) : null;
    }).then(function (op) {
      f.question = (op && op.body && op.body.operation && op.body.operation.status.question) || "";
      // A read asked before a reset (factsAsked = 0) must not overwrite a newer one.
      if (factsAsked === now) { f.at = Date.now(); facts = f; }
    }).catch(function () { /* asked again in half a second */ });
    return facts;
  }
  // Forget the facts: the next gate asks again at once.
  function resetFacts() { facts.at = 0; factsAsked = 0; }
  function row(env, svc) { return fact().rows[env + "/" + svc] || {}; }
  // The change the breaking-change moments are about: the held one, then the
  // promote that shipped it.
  function changeHash() {
    var f = fact();
    return "#/delivery/changes/prod/" + (f.held || f.latest);
  }
  function onChange() { return hashPath().indexOf("#/delivery/changes/prod/") === 0; }
  function propose(body) {
    return serve("POST", "/api/delivery/operations", body).then(function (r) { resetFacts(); refreshViews(); return r; });
  }

  // The controls the delivery steps point at.
  var PROPOSE_BTN = '[data-testid="propose-panel"] button[type=submit]';
  // Switch person sits in the person menu at the top right; it can be lit only
  // while that menu is open.
  var PERSON_MENU = '[data-testid="identity-trigger"]';
  var SWITCH_BTN = '#navbar-identity:popover-open [data-testid="sign-out"]';
  var NOTE = '[data-testid="decision-panel"] textarea';
  var APPROVE = '[data-testid="decide-approve"]';
  var RAISE_FRAUD = '[aria-label="Propose Advise for fraud in test"]';
  // A plan is open for this Stage ("prod/payments"), as Promote opens it.
  function planOpen(stage) { var h = q("#pp-title"); return !!h && h.textContent.indexOf(stage) >= 0; }
  function noteText() { var t = q(NOTE); return t ? t.value.trim() : ""; }
  function shipped(version) { return row("prod", "payments").version === version; }
  function clickOn(sel) { var b = q(sel); if (b) { b.click(); } }
  // A decision on the held change as Carol: what a skipped approve or accept
  // does in one go.
  function decide(decision, note) {
    var name = fact().held;
    if (!name) { return; }
    setPersona("carol", false).then(function () {
      return serve("POST", "/api/delivery/operations/prod/" + name + "/decision", { decision: decision, note: note });
    }).then(function () { resetFacts(); refreshViews(); });
  }

  function pactifyText() {
    var p;
    try { p = JSON.parse(window.__pactoPactifyPlan || ""); } catch (e) { return "The recorded plan did not load."; }
    var file = (p.files || []).filter(function (f) { return f.path === "pacto.yaml"; })[0] || {};
    function list(title, xs) { return xs && xs.length ? title + "\n" + xs.map(function (x) { return "  - " + x; }).join("\n") + "\n" : ""; }
    return "Recorded: what `pacto init --plan --owner risk` prints for the plain fraud service. A test keeps it current.\n\n" +
      list("Found:", p.found) + list("Left as Unknown:", p.unknown) + list("Still yours to do:", p.next) +
      "\npacto.yaml (" + (file.action || "add") + "):\n" + (file.content || "");
  }

  // Anchored on the end of the href so a same-named service in another domain (this
  // fixture publishes one) cannot satisfy a gate meant for the default-domain service.
  function paymentsLink() { return q('[data-testid="service-list"] a[href$="/fleet/services/payments-service"]'); }
  // The earlier revision is the oldest one (the last option, newest first).
  function oldestChosen() {
    var s = q("#impact-old-rev");
    return !!s && !!s.options && s.options.length > 1 && s.value === s.options[s.options.length - 1].value;
  }

  // Svelte 5 binds on the EVENT, not on the property: assigning .value alone leaves the
  // component's own state holding the old value, so the submit that follows sends the
  // old one and the skip silently does nothing. Both events, because a text input binds
  // on `input` and this app's revision selectors commit on `change`.
  function setValue(node, value) {
    node.value = value;
    node.dispatchEvent(new Event("input", { bubbles: true }));
    node.dispatchEvent(new Event("change", { bubbles: true }));
  }

  // One action per step, and the light on the control that action uses. A moment
  // that needs several actions (promote then propose; switch person, pick Carol,
  // write the note, approve) is several steps, each moving on
  // by itself when the tour sees it done. Only a step whose result is worth reading
  // holds for Continue, so nobody is moved off a screen they are still reading.
  var NOTE_APPROVE = "orders 1.4 is ready for payments 3";
  var NOTE_ACCEPT = "traffic is low at night; the error ratio is watched by hand";
  var DELIVERY_TOUR = [{
    text: "This is the payments story, simulated in your browser: Pacto's own code makes every plan, check and signed record from fixed data, and nothing reaches a cluster.",
    hash: "#/delivery/environments",
    target: '[data-testid="environments-board"]'
  }, {
    text: "Act 1, pactify a service: pacto init read the files of fraud, a plain service, and wrote its contract, so nobody had to write one by hand.",
    hash: "#/delivery/environments",
    target: ['[data-testid="environments-board"]', '[data-testid="env-row-fraud"]'],
    pre: pactifyText
  }, {
    text: "Act 2, a safe change ships on its own: payments 2.4.0 reached prod with nobody involved, because checkout and orders said they accept any payments 2.x.",
    hash: "#/delivery/history?namespace=prod&stage=payments",
    target: '[aria-label="History of prod/payments"]'
  }, {
    text: "A bad release is caught and rolled back: the health check failed after 2.4.0 shipped, so Pacto went back to 2.3.0 by itself and will not try 2.4.0 there again.",
    hash: "#/delivery/changes/prod/payments-promote-2",
    target: '[aria-labelledby="cd-actual"]'
  }, {
    text: "A breaking change asks only the affected teams: as pay-agent, press Promote on payments 3.0.0 in prod.",
    hash: "#/delivery/environments",
    target: '[aria-label="Promote payments in prod to 3.0.0"]',
    wait: "Waiting for the plan of payments 3.0.0 to open.",
    gate: function () { return planOpen("prod/payments"); },
    skip: function () { clickOn('[aria-label="Promote payments in prod to 3.0.0"]'); }
  }, {
    text: "The plan asks only checkout and orders, because only they accept nothing past 2.x, so press Propose promote.",
    hash: "#/delivery/environments",
    target: [PROPOSE_BTN, '[data-testid="decision-safer"]'],
    wait: "Waiting for payments 3.0.0 to be proposed for prod.",
    hold: true,
    gate: function () { return !!fact().held && onChange(); },
    skip: function () {
      digestOf("payments", "3.0.0").then(function (to) {
        return propose({ namespace: "prod", stage: "payments", to: to });
      }).then(function (r) {
        if (r && r.body && r.body.operation) { window.location.hash = "#/delivery/changes/prod/" + r.body.operation.metadata.name; }
      });
    }
  }, {
    text: "Pacto said what to update first, so press Promote on checkout 1.9.0 in prod.",
    hash: "#/delivery/environments",
    target: '[aria-label="Promote checkout in prod to 1.9.0"]',
    wait: "Waiting for the plan of checkout 1.9.0 to open.",
    gate: function () { return planOpen("prod/checkout"); },
    skip: function () { clickOn('[aria-label="Promote checkout in prod to 1.9.0"]'); }
  }, {
    text: "Press Propose promote: checkout ships with nobody asked, and then only orders is asked about payments 3.0.0, because checkout 1.9 also accepts it.",
    hash: "#/delivery/environments",
    target: [PROPOSE_BTN, ".page-hd-sub"],
    wait: "Waiting for checkout 1.9.0 to ship to prod.",
    hold: true,
    gate: function () { return row("prod", "checkout").version === "1.9.0" && fact().question.indexOf("Asked of: orders") >= 0; },
    skip: function () {
      digestOf("checkout", "1.9.0").then(function (to) { return propose({ namespace: "prod", stage: "checkout", to: to }); });
    }
  }, {
    text: "Only a person on the asked team decides, so open the person menu at the top right.",
    hash: changeHash,
    target: PERSON_MENU,
    wait: "Waiting for the person menu to open.",
    gate: function () { return !!q(SWITCH_BTN) || !!q('[data-testid="demo-persona-carol"]') || persona === "carol"; },
    skip: function () { var m = q("#navbar-identity"); if (m && m.showPopover) { m.showPopover(); } }
  }, {
    text: "Press Switch person.",
    hash: changeHash,
    // Back to the menu button if the menu was closed without a press.
    target: [PERSON_MENU, SWITCH_BTN],
    wait: "Waiting for the list of people to open.",
    gate: function () { return !!q('[data-testid="demo-persona-carol"]') || persona === "carol"; },
    skip: function () { setPersona("carol", false); }
  }, {
    text: "Pick Carol, who is on team orders.",
    hash: changeHash,
    // Back to the menu if the list was closed without a pick.
    target: [PERSON_MENU, SWITCH_BTN, '[data-testid="demo-persona-carol"]'],
    wait: "Waiting for Carol to be picked.",
    gate: function () { return persona === "carol"; },
    skip: function () {
      var b = q('[data-testid="demo-persona-carol"]');
      if (b) { b.click(); } else { setPersona("carol", false); }
    }
  }, {
    text: "Write in the note why 3.0.0 should ship, because approving a breaking change needs a reason.",
    hash: changeHash,
    target: NOTE,
    wait: "Waiting for a note.",
    gate: function () { return noteText().length >= 3 || shipped("3.0.0"); },
    skip: function () { var t = q(NOTE); if (t) { setValue(t, NOTE_APPROVE); } }
  }, {
    text: "Press Approve shipping 3.0.0 to send Carol's approval, and 3.0.0 ships.",
    hash: changeHash,
    target: APPROVE,
    wait: "Waiting for 3.0.0 to ship.",
    gate: function () { return shipped("3.0.0"); },
    skip: function () { decide("approve", NOTE_APPROVE); }
  }, {
    text: "Then Pacto could not see: only 3 traces arrived after 3.0.0 shipped, too few to judge, so the check says Unknown and the change waits for a person.",
    hash: changeHash,
    target: '[data-testid="verdict-unknown"]',
    wait: "Waiting for the checks after release to end.",
    hold: true,
    // The system moves here, not the reader, so an Unknown already on screen counts.
    observe: true,
    gate: function () { return !!q('[data-testid="decision-unknown"]') || (!fact().held && fact().accepted); },
    skip: function () { /* nothing to do: the checks end by themselves */ }
  }, {
    text: "Write in the note why accepting the unknown is safe.",
    hash: changeHash,
    target: NOTE,
    wait: "Waiting for a note.",
    gate: function () { return noteText().length >= 3 || (!fact().held && fact().accepted); },
    skip: function () { var t = q(NOTE); if (t) { setValue(t, NOTE_ACCEPT); } }
  }, {
    text: "Press Accept the unknown and keep 3.0.0: the record says a person accepted it, and the check stays Unknown.",
    hash: changeHash,
    target: APPROVE,
    wait: "Waiting for Carol to accept the unknown.",
    gate: function () { var f = fact(); return !f.held && f.accepted; },
    skip: function accept() {
      // The checks after release end a moment after 3.0.0 ships; ask once it waits.
      if (!fact().held) { setTimeout(accept, 500); return; }
      decide("accept-unknown", NOTE_ACCEPT);
    }
  }, {
    text: "Every step is a signed record: as Carol, press Roll back this change.",
    hash: changeHash,
    target: '[data-testid="change-rollback"]',
    wait: "Waiting for the rollback plan to open.",
    gate: function () { return planOpen("Roll back prod/payments"); },
    skip: function () { clickOn('[data-testid="change-rollback"]'); }
  }, {
    text: "Press Propose roll back, and automation holds off afterwards because a person chose this.",
    hash: changeHash,
    target: [PROPOSE_BTN, ".page-hd-sub"],
    wait: "Waiting for the rollback to finish.",
    hold: true,
    gate: function () { return !!row("prod", "payments").autoHold && row("prod", "payments").version === "2.3.0"; },
    skip: function () {
      var undoes = fact().latest;
      setPersona("carol", false).then(function () {
        return propose({ namespace: "prod", stage: "payments", type: "delivery.pacto.trianalab.io/rollback", undoes: undoes });
      });
      // Proposed through the API, so the plan the reader opened is still on screen with
      // its button live: close it, or "Done" sits next to a form asking to be pressed.
      clickOn('[data-testid="propose-panel"] .pp-btns button[type=button]');
    }
  }, {
    text: "History reads every step back from records signed in this tab, including a deliberate gap in test and a record signed by a key nobody trusts.",
    hash: "#/delivery/history",
    target: '[data-stage="prod/payments"]'
  }, {
    text: "How far each Stage runs on its own: fraud is new, so it observes, and Pacto only noted it would have shipped 1.0.0 to test with nobody involved.",
    hash: "#/delivery/autonomy",
    target: '[data-testid="autonomy-stages"] [data-stage="test/fraud"]'
  }, {
    text: "payments in prod stepped down a level by itself when 2.4.0 failed there, so only non-breaking changes ship there on their own now.",
    hash: "#/delivery/autonomy",
    target: '[data-testid="autonomy-stages"] [data-stage="prod/payments"]'
  }, {
    text: "One verified version earned fraud a proposal to move up a level, so press Propose on fraud's row.",
    hash: "#/delivery/autonomy",
    target: RAISE_FRAUD,
    wait: "Waiting for the plan of fraud's raise to open.",
    gate: function () { return planOpen("test/fraud"); },
    skip: function () { clickOn(RAISE_FRAUD); }
  }, {
    text: "The plan asks team risk, which owns fraud: every raise is one level, earned by evidence and confirmed by a person on the service's team.",
    hash: "#/delivery/autonomy",
    target: '[data-testid="propose-panel"]'
  }, {
    text: "pay-agent ships like any other service: its runner ran the eval suite and signed the count, and 1.1.0 passed 1 of 2, so it rolled back in test.",
    hash: "#/delivery/changes/test/pay-agent-promote-14",
    target: '[data-testid="verdict-runner"]'
  }, {
    text: "Explore more on your own, or see the same story on a real cluster.",
    links: [
      ["#/delivery/publish", "The refused 2.4.3 and the bump it needed"],
      ["#/delivery/history?namespace=test&stage=payments", "The claim check that caught payments calling fraud"],
      ["#/delivery/autonomy", "What stands in the way of each next level"]
    ],
    href: "../examples/kubernetes-demo/",
    linkText: "The same story on a real cluster"
  }];

  // ---- simulated sign-in and persona switch -----------------------------------

  var PERSONAS = [
    ["pay-agent", "pay-agent", "an agent working for a developer; it can propose changes but cannot decide them"],
    ["alice", "Alice", "team payments; changes that break other teams go to those teams"],
    ["carol", "Carol", "team orders; asked when a change breaks orders"]
  ];
  var persona = "pay-agent";
  // Resolves to "" or the reason the engine refused the switch.
  function setPersona(name, fresh) {
    var asked = typeof window.__pactoPersona === "function" ? window.__pactoPersona(name, fresh) : "the engine is not loaded";
    return Promise.resolve(asked).then(function (err) {
      if (!err) { persona = name; resetFacts(); refreshViews(); }
      return err;
    });
  }

  // The dashboard calls this instead of leaving for /auth/login (see signIn in
  // the frontend's lib/session.svelte.ts): leaving would reload the page and lose the tab's
  // whole simulation. fresh is a decision asking for a new login: the same person
  // signs in again. Otherwise it is a switch to another person's existing session.
  window.__pactoSignIn = function (hash, fresh) {
    var dlg = document.createElement("dialog");
    dlg.id = "pacto-signin";
    dlg.setAttribute("data-testid", "demo-signin");
    dlg.setAttribute("aria-labelledby", "pacto-signin-title");
    var who = PERSONAS.filter(function (p) { return p[0] === persona; })[0];
    var html = '<h2 id="pacto-signin-title">' + (fresh ? "Simulated sign-in" : "Switch person") + "</h2>" +
      "<p>" + (fresh
        ? "A real install sends you to your company login, and the cluster reads the sign-in time from it."
        : "Each person here is already signed in, and a person on the asked team decides with that sign-in.") + "</p>";
    if (fresh) {
      html += '<p><button type="submit" value="' + who[0] + '" data-testid="demo-signin-submit">Sign in again as ' + who[1] + "</button></p>";
    } else {
      html += PERSONAS.map(function (p) {
        return '<p><button type="submit" value="' + p[0] + '" data-testid="demo-persona-' + p[0] + '"' + (p[0] === persona ? ' aria-current="true"' : "") +
          "><strong>" + p[1] + "</strong> — " + p[2] + "</button></p>";
      }).join("");
    }
    html += '<p><button type="submit" value="" data-testid="demo-signin-cancel">Cancel</button></p>';
    dlg.innerHTML = '<form method="dialog">' + html + "</form>";
    dlg.addEventListener("close", function () {
      if (dlg.returnValue) { setPersona(dlg.returnValue, !!fresh); }
      dlg.parentNode.removeChild(dlg);
      if (hash && window.location.hash !== hash) { window.location.hash = hash; }
    });
    document.body.appendChild(dlg);
    dlg.showModal();
  };

  window.__pactoTours = { notice: NOTICE, fleet: FLEET_TOUR, delivery: DELIVERY_TOUR, world: world, setPersona: setPersona };
})();
