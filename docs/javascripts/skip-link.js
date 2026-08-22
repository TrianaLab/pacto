// Make "Skip to content" actually skip. Material points the skip link at the
// page's first heading, and a heading is not a focusable element: the browser
// scrolls the viewport there and leaves document.activeElement on <body>, so the
// very next Tab restarts at the top of the page and the reader is back in the
// header they were trying to skip. Give the target a programmatic tab stop on the
// way in and hand it the focus, which is what the link promises.
//
// Delegated on document so it survives Material's instant navigation.
document.addEventListener("click", function (e) {
  var skip = e.target.closest(".md-skip");
  if (!skip || !skip.hash) return;
  var target = document.getElementById(decodeURIComponent(skip.hash.slice(1)));
  if (!target) return;
  // -1 keeps the heading out of the normal tab sequence; it only ever receives
  // focus from this link. Tabbing on from here continues into the content.
  target.setAttribute("tabindex", "-1");
  target.focus();
});
