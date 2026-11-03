// Open the mike version dropdown on CLICK, not hover. Material opens
// .md-version__list on :hover, but that list sits directly over the nav tabs below
// it — so it pops open when the mouse merely passes over and, worse, swallows the
// tabs' clicks. Toggle an .md-version--open class from the trigger instead; close on
// an outside click or Escape. Delegated on document so it survives Material's instant
// navigation (the header, and its version selector, persist across page swaps).
document.addEventListener("click", function (e) {
  var version = document.querySelector(".md-version");
  if (!version) return;
  if (e.target.closest(".md-version__current")) {
    e.preventDefault();
    version.classList.toggle("md-version--open");
  } else if (!e.target.closest(".md-version__list")) {
    // A click anywhere outside the trigger and the open list closes it. Clicks on a
    // version link inside the list fall through so the navigation proceeds.
    version.classList.remove("md-version--open");
  }
});

document.addEventListener("keydown", function (e) {
  if (e.key !== "Escape") return;
  var version = document.querySelector(".md-version");
  if (version) version.classList.remove("md-version--open");
});
