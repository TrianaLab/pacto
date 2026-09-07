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

  // Material 9.7 wraps each code block's copy and select buttons in a bare <nav>
  // (`md-code__nav`, built in the theme's bundle, not a partial). That makes every
  // code block a navigation landmark with no name, so any page carrying two of
  // them fails landmark-unique -- which is every page on this site.
  //
  // Naming them would satisfy the rule and make the page worse: a screen reader's
  // landmark menu would grow one entry per code block. Two buttons are not a
  // region to navigate to, so drop the landmark instead and leave the buttons
  // themselves untouched.
  function unlandmarkCodeNavs() {
    var navs = document.querySelectorAll("nav.md-code__nav");
    for (var i = 0; i < navs.length; i++) {
      var nav = navs[i];
      if (nav.getAttribute("role")) continue;
      if (nav.getAttribute("aria-label") || nav.getAttribute("aria-labelledby")) continue;
      nav.setAttribute("role", "presentation");
    }
  }

  // The breadcrumb trail is the one place this file overwrites a name the theme
  // set, because the name the theme set is the bug: partials/path.html labels it
  // `lang.t('nav')`, the same string the primary navigation uses, so the two
  // collide as landmark-unique on every page deep enough to have a trail. It is a
  // breadcrumb, and "Breadcrumb" is what it should have been called.
  function nameBreadcrumb() {
    var path = document.querySelector("nav.md-path");
    var primary = document.querySelector("nav.md-nav--primary");
    if (!path || !primary) return;
    if (path.getAttribute("aria-label") === primary.getAttribute("aria-label")) {
      path.setAttribute("aria-label", "Breadcrumb");
    }
  }

  function apply() {
    name('[data-md-component="search"][role="dialog"]', "Search");
    name('[data-md-component="progress"][role="progressbar"]', "Page loading progress");
    nameSubNavs();
    unlandmarkCodeNavs();
    nameBreadcrumb();
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
