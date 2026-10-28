// tour.js — the demo's guided-tour runner: the spotlight overlay, the bubble and
// the gate-driven state machine that moves a tour from step to step. The tours
// themselves (their steps and the simulated world their gates read) are in
// tours.js; the strip that offers them is in boot.js.
//
// Injected as a classic script before tours.js and boot.js, and shipped with the
// demo and nothing else. It defines one page global, window.__pactoTour.
(function () {
  "use strict";

  var style = document.createElement("style");
  style.textContent =
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
    "#pacto-tour-exit{margin-left:auto}" +
    "#pacto-tour-pre{display:block;margin:.5rem 0 0;max-height:min(40vh,18rem);overflow:auto;white-space:pre-wrap;" +
    "background:#0f172a;color:#e2e8f0;border-radius:8px;padding:.5rem .6rem;font:400 .75rem/1.4 ui-monospace,SFMono-Regular,Menlo,monospace}" +
    "#pacto-tour-pre[hidden],#pacto-tour-explore[hidden]{display:none}" +
    "#pacto-tour-explore{display:flex;flex-direction:column;gap:.3rem;margin-top:.45rem}";
  document.head.appendChild(style);

  // The tour being run (an array of steps) and the page's hooks for it; see startTour.
  var TOUR = [];
  var hooks = {};
  var overlay, spot, bubble, stepNum, textEl, hintEl, autoEl, backBtn, nextBtn, skipBtn, moreLink, preEl, exploreEl;
  var step = 0;
  var running = false;
  var raf = 0;
  // The element the spotlight is currently on. Kept so the tour scrolls a target into
  // view when it CHANGES rather than on every frame, which would fight the reader.
  var lastTarget = null;

  // ---- the guided tour -------------------------------------------------------

  function q(sel) { return document.querySelector(sel); }
  // The route WITHOUT its query. A step's own action can add one -- step 2 commits
  // `?text=payments` -- and the step after it lives on the same screen, so comparing
  // the whole hash would navigate back and throw the reader's own search away.
  function hashPath() { return window.location.hash.split("?")[0]; }

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
    // A target folded away on the page cannot be lit: open every fold around it.
    for (var d = found && found.closest("details:not([open])"); d; d = d.parentElement && d.parentElement.closest("details:not([open])")) { d.open = true; }
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
  var GATE_OPEN = "Done. Moving on.";
  // The same observation on a step that holds, which has to say the opposite thing: the
  // tour saw the action and is deliberately NOT taking the screen away. Announcing the
  // stay matters as much as announcing a move -- a reader who cannot see the result
  // otherwise has no way to know the tour is now waiting on them.
  var HOLD_OPEN = "Done. That is the answer. Read it, then press Continue.";

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
  // Arrived by moving forward rather than by Back. A step whose action the reader
  // already did before reaching it (switched to Carol early, say) is passed by: the
  // tour saw nothing happen, but there is nothing left to wait for. Back never does
  // this, so re-reading a step never bounces the reader forward again.
  var forward = false;
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
    } else if ((armed || forward || s.observe) && !openSince) {
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

    // A step's longer evidence (the recorded pacto init plan), scrollable so the
    // bubble never grows past the window.
    preEl = document.createElement("pre");
    preEl.id = "pacto-tour-pre";
    preEl.setAttribute("data-testid", "demo-tour-pre");
    preEl.setAttribute("tabindex", "0");
    preEl.hidden = true;

    // "Explore more": the beats the guided path leaves out, one link each.
    exploreEl = document.createElement("nav");
    exploreEl.id = "pacto-tour-explore";
    exploreEl.setAttribute("aria-label", "Explore more");
    exploreEl.setAttribute("data-testid", "demo-tour-explore");
    exploreEl.hidden = true;

    hintEl = document.createElement("span");
    hintEl.id = "pacto-tour-hint";
    hintEl.setAttribute("aria-live", "polite");

    // Shown on exactly the steps that have no forward button, which is where a reader
    // would otherwise stand waiting for one. It is not a live region: it does not
    // change, and every step change reads it out as part of the bubble's description.
    autoEl = document.createElement("span");
    autoEl.id = "pacto-tour-auto";
    autoEl.setAttribute("data-testid", "demo-tour-auto");
    autoEl.textContent = "No button needed. The tour moves on when you do it.";

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
    about.href = hooks.docsHref;
    about.textContent = "About this demo";

    bubble.appendChild(stepNum);
    bubble.appendChild(textEl);
    bubble.appendChild(preEl);
    bubble.appendChild(exploreEl);
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
    forward = i > step;
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
    var h = typeof s.hash === "function" ? s.hash() : s.hash;
    // A step whose route carries a query wants exactly that query; one without
    // leaves the reader's own query alone (step 2 of the fleet tour commits one).
    if (h && (h.indexOf("?") >= 0 ? window.location.hash !== h : hashPath() !== h)) { window.location.hash = h; }
    preEl.hidden = !s.pre;
    preEl.textContent = s.pre ? s.pre() : "";
    exploreEl.hidden = !s.links;
    exploreEl.textContent = "";
    (s.links || []).forEach(function (l) {
      var a = document.createElement("a");
      a.href = l[0];
      a.textContent = l[1];
      exploreEl.appendChild(a);
    });
    place(resolve(s.target));
    // Keeps a keyboard reader with the tour after the app re-renders the whole main
    // region underneath it.
    bubble.focus();
  }

  // Runs tour, an array of steps. h.docsHref is the explainer the bubble links to;
  // h.onStart(bubble) runs once the first step is drawn and h.onExit(bubble) once the
  // overlay is gone, so the page can make room for the bubble and give it back.
  function startTour(tour, h) {
    if (running) { return; }
    TOUR = tour;
    hooks = h;
    running = true;
    buildOverlay();
    // On the document rather than on anything the tour owns: the action a step asks for
    // happens in the APP, so the only place that sees the reader still working is the
    // page as a whole. keydown and pointerdown only -- they are what "still doing
    // something" means, and unlike `input` no component synthesizes them while it
    // renders, so nothing the app does to itself can hold the tour still.
    document.addEventListener("keydown", noteInput, true);
    document.addEventListener("pointerdown", noteInput, true);
    goStep(0);
    hooks.onStart(bubble);
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
    // The Escape handler goes with the bubble it is bound to; these two are the only
    // things the tour ever put on the document, and a tour that is over has no business
    // still watching what the reader types.
    document.removeEventListener("keydown", noteInput, true);
    document.removeEventListener("pointerdown", noteInput, true);
    var b = bubble;
    overlay.parentNode.removeChild(overlay);
    overlay = spot = bubble = null;
    lastTarget = null;
    skipUntil = 0;
    armed = false;
    forward = false;
    openSince = 0;
    confirmAt = 0;
    hooks.onExit(b);
  }

  window.__pactoTour = { q: q, hashPath: hashPath, running: function () { return running; }, start: startTour };
})();
