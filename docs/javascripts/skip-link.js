// Make "Skip to content" actually skip. Two things stand between the link and
// the promise it makes, and both leave the reader back in the header.
//
// First, Material points the link at the page's first heading, and a heading is
// not a focusable element: the browser scrolls the viewport there and leaves
// document.activeElement on <body>, so the very next Tab restarts at the top.
// Giving the target a programmatic tab stop and focusing it fixes that.
//
// Second -- and this is why the obvious one-liner is not enough -- the link's
// href is a full URL, so `navigation.instant` treats activating it as a
// navigation: it refetches the page (observed as a second `GET /quickstart/`)
// and replaces the body. The heading we just focused is detached in the
// process and focus drops back to <body> about 200ms later, silently, after
// everything already looked correct. Capturing the click first does not help;
// the theme's own listener is registered before this file loads. So we let the
// re-render happen and put the focus back on the new node once it lands.
(function () {
  var pendingId = null;
  var pendingUntil = 0;

  function focusTarget(id) {
    var el = document.getElementById(id);
    if (!el) return false;
    // -1 keeps the heading out of the normal tab sequence; it only ever
    // receives focus from this link. Tabbing on continues into the content.
    el.setAttribute("tabindex", "-1");
    el.focus();
    return document.activeElement === el;
  }

  // Delegated on document so it survives every re-render.
  document.addEventListener("click", function (e) {
    var skip = e.target.closest(".md-skip");
    if (!skip || !skip.hash) return;
    var id = decodeURIComponent(skip.hash.slice(1));
    focusTarget(id);
    pendingId = id;
    // Bounded, so a navigation that never re-renders cannot leave this armed
    // and steal the focus back minutes later.
    pendingUntil = Date.now() + 2000;
  });

  new MutationObserver(function () {
    if (!pendingId) return;
    if (Date.now() > pendingUntil) {
      pendingId = null;
      return;
    }
    // Step in only while nothing holds the focus. If the reader has already
    // tabbed on, this re-render is not ours to correct.
    if (document.activeElement && document.activeElement !== document.body) {
      pendingId = null;
      return;
    }
    if (focusTarget(pendingId)) pendingId = null;
  }).observe(document.documentElement, { childList: true, subtree: true });
})();
