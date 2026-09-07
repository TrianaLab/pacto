// Some of Material's own landmarks ship without an accessible name, so a screen
// reader announces "dialog", "progress bar" or bare "navigation" with nothing
// after them. They live in the theme's partials, which are the theme's to own --
// naming them here costs a few lines and survives a theme upgrade, where
// vendoring a partial to add one attribute would silently freeze that markup at
// today's version.
//
// The guard everywhere is the same: never overwrite a name the theme has learned
// to set for itself.
(function () {
  function name(selector, label) {
    var el = document.querySelector(selector);
    if (el && !el.getAttribute("aria-label") && !el.getAttribute("aria-labelledby")) {
      el.setAttribute("aria-label", label);
    }
  }

  // A nav entry that is both a page and a section renders as an <a> carrying the
  // title plus a sibling <label> carrying only the expand icon -- and the
  // sub-navigation points its aria-labelledby at that icon-only label. The
  // result is a navigation landmark with an empty name, three of them on this
  // site, which axe reports as landmark-unique the moment the mobile drawer
  // renders them all. The title is right there in the sibling link.
  function nameSubNavs() {
    var navs = document.querySelectorAll("nav.md-nav[aria-labelledby]");
    for (var i = 0; i < navs.length; i++) {
      var nav = navs[i];
      if (nav.getAttribute("aria-label")) continue;
      var label = document.getElementById(nav.getAttribute("aria-labelledby"));
      if (!label || label.textContent.trim()) continue;
      var title = label.parentElement && label.parentElement.querySelector("a .md-ellipsis");
      if (title && title.textContent.trim()) {
        nav.setAttribute("aria-label", title.textContent.trim());
      }
    }
  }

  function apply() {
    name('[data-md-component="search"][role="dialog"]', "Search");
    name('[data-md-component="progress"][role="progressbar"]', "Page loading progress");
    nameSubNavs();
  }

  // The header persists across instant navigation; the navigation drawer does
  // not, so re-run on every document the theme swaps in. `document$` is
  // Material's own hook and emits the first document too.
  if (window.document$ && typeof window.document$.subscribe === "function") {
    window.document$.subscribe(apply);
  } else if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", apply);
  } else {
    apply();
  }
})();
