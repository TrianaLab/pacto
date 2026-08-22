// Two of Material's own landmarks ship without an accessible name, so a screen
// reader announces "dialog" and "progress bar" with nothing after them. Both
// live in the header partial, which is the theme's to own -- naming them here
// costs four lines and survives a theme upgrade, where vendoring the partial to
// add one attribute would silently freeze that markup at today's version.
//
// The header persists across instant navigation, so this runs once. The guard
// is for the case where it does not: never overwrite a name the theme has
// learned to set for itself.
(function () {
  function name(selector, label) {
    var el = document.querySelector(selector);
    if (el && !el.getAttribute("aria-label") && !el.getAttribute("aria-labelledby")) {
      el.setAttribute("aria-label", label);
    }
  }
  function apply() {
    name('[data-md-component="search"][role="dialog"]', "Search");
    name('[data-md-component="progress"][role="progressbar"]', "Page loading progress");
  }
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", apply);
  } else {
    apply();
  }
})();
