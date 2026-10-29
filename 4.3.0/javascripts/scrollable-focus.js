// A wide table and a long command line are the two things this site is full of,
// and Material makes both scroll sideways inside a container that nothing can
// focus. A mouse can drag them; a keyboard cannot reach them at all, so the
// right-hand half of a CRD reference table or of a `kubectl` invocation is
// simply unavailable to a reader who does not use a pointer (WCAG 2.1.1).
//
// Overflow depends on the viewport, so this is a measurement, not a class: the
// same table needs a tab stop at 390px and none at 1440px, and a reader who
// rotates a phone must get the same answer as one who loaded it that way. The
// tab stop is added only while the element actually overflows and removed again
// when it stops, so no page grows stops it does not need.
(function () {
  var SELECTOR = ".md-typeset__scrollwrap, .md-typeset code.md-code__content";

  function apply() {
    var els = document.querySelectorAll(SELECTOR);
    for (var i = 0; i < els.length; i++) {
      var el = els[i];
      var overflows = el.scrollWidth > el.clientWidth;
      if (overflows && !el.hasAttribute("tabindex")) {
        el.setAttribute("tabindex", "0");
      } else if (!overflows && el.getAttribute("tabindex") === "0") {
        // Only ever take back the one we added.
        el.removeAttribute("tabindex");
      }
    }
  }

  var queued = false;
  function schedule() {
    if (queued) return;
    queued = true;
    requestAnimationFrame(function () {
      queued = false;
      apply();
    });
  }

  // `document$` is Material's own hook and emits the first document as well as
  // every one instant navigation swaps in.
  if (window.document$ && typeof window.document$.subscribe === "function") {
    window.document$.subscribe(schedule);
  } else if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", schedule);
  } else {
    schedule();
  }
  window.addEventListener("resize", schedule);
})();
