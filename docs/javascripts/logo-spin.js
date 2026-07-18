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

// Smooth "back to top" — Material's top button (href="#") otherwise jumps
// instantly (and can trigger an instant-navigation re-render). Intercept it and
// animate the scroll instead. Delegated on document so it survives instant nav.
document.addEventListener("click", function (e) {
  var top = e.target.closest('[data-md-component="top"]');
  if (!top) return;
  e.preventDefault();
  var reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  window.scrollTo({ top: 0, behavior: reduce ? "auto" : "smooth" });
});
