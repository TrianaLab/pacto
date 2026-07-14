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
