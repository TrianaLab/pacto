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
    if (document.activeElement === el) return true;
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
    // Step aside only if the reader has already tabbed on -- that re-render is
    // not ours to correct. Our own target still holding the focus is not that:
    // it is the normal state on the first batch of the re-render, and reading
    // it as "the reader moved on" disarmed this observer before the detach it
    // exists to correct ever arrived. Measured on the built site, that guard
    // fired on batch 1 of every single load, on both pages, which left the
    // heading unrestored whenever the swap did detach it -- rarely, and only
    // under load, which is the worst way for an accessibility fix to fail.
    var active = document.activeElement;
    if (active && active !== document.body && active.id !== pendingId) {
      pendingId = null;
      return;
    }
    // Stay armed for the rest of the window rather than disarming on the first
    // success, so a heading replaced a second time is still put back.
    focusTarget(pendingId);
  }).observe(document.documentElement, { childList: true, subtree: true });
})();
