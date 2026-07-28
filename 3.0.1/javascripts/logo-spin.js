// Spin the Pacto logo on click — the header mark and the landing-page hero mark.
// Delegated on document so it keeps working across Material's instant navigation.
// Respects prefers-reduced-motion.
document.addEventListener("click", function (e) {
  var hit = e.target.closest(".md-header__button.md-logo, .pacto-hero__logo");
  if (!hit) return;
  if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
  var el = hit.classList.contains("pacto-hero__logo") ? hit : hit.querySelector("img");
  if (!el) return;
  el.classList.remove("pacto-spin");
  void el.offsetWidth; // reflow so the animation restarts on every click
  el.classList.add("pacto-spin");
});

// Fast, smooth "back to top". Material's button is an href="#" anchor; intercept
// it in the capture phase (so we run before Material's own handler), cancel the
// jump, and animate the scroll ourselves with a short duration. Delegated on
// document so it survives instant navigation. Instant under reduced-motion.
document.addEventListener(
  "click",
  function (e) {
    var btn = e.target.closest('[data-md-component="top"]');
    if (!btn) return;
    e.preventDefault();
    var start = window.pageYOffset || document.documentElement.scrollTop;
    if (start <= 0) return;
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
      window.scrollTo(0, 0);
      return;
    }
    var dur = 320; // ms — snappier than the browser default
    var t0 = 0;
    function frame(ts) {
      if (!t0) t0 = ts;
      var p = Math.min((ts - t0) / dur, 1);
      var eased = 1 - Math.pow(1 - p, 3); // ease-out cubic
      window.scrollTo(0, Math.round(start * (1 - eased)));
      if (p < 1) requestAnimationFrame(frame);
    }
    requestAnimationFrame(frame);
  },
  true
);
