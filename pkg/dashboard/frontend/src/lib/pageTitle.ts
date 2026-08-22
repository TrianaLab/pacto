/**
 * Keep `document.title` equal to the page the user is actually on.
 *
 * Every route used to render the same title, "Pacto Dashboard". That is a real
 * accessibility defect, not a cosmetic one: WCAG 2.4.2 asks a page to be
 * identifiable by its title, and a screen reader announces the title on
 * navigation. It also broke the two places a title is the ONLY label the user
 * gets -- browser tabs and history entries -- so ten open Pacto tabs were ten
 * identical strings.
 *
 * The title is MIRRORED from the rendered `<h1>` rather than maintained
 * separately. A second copy of the page name in a route table would be free to
 * drift from the heading, and the heading is the name the user can see; deriving
 * one from the other makes them impossible to disagree. It also covers the legacy
 * non-Fleet views for free, without touching them.
 *
 * A MutationObserver is what makes this work with async data: an h1 like
 * "Revision: payments-service 2.1.0" only exists once the detail request lands,
 * so an effect keyed on the route would run too early and set a stale title.
 */

/** The suffix every title carries, so a tab is recognizable as Pacto at a glance. */
const SUFFIX = 'Pacto';

/** Fallback for the moment before the first h1 exists (initial WASM boot). */
const FALLBACK = 'Pacto Dashboard';

/** titleFor is the pure part: the document title for a given heading text. */
export function titleFor(heading: string | null | undefined): string {
  const h = (heading || '').replace(/\s+/g, ' ').trim();
  if (!h) return FALLBACK;
  // A heading that already names the product would read as "Pacto - Pacto".
  if (h === SUFFIX || h === FALLBACK) return FALLBACK;
  return `${h} - ${SUFFIX}`;
}

/**
 * syncPageTitle mirrors `root`'s first `<h1>` into `document.title` and keeps it
 * in sync. Returns a teardown. Writes only when the value actually changes, so a
 * busy subtree (a graph re-render) costs one string compare, not a title write.
 */
export function syncPageTitle(root: Element | null): () => void {
  if (!root || typeof MutationObserver === 'undefined') return () => {};
  const apply = () => {
    const next = titleFor(root.querySelector('h1')?.textContent);
    if (document.title !== next) document.title = next;
  };
  apply();
  const obs = new MutationObserver(apply);
  obs.observe(root, { childList: true, subtree: true, characterData: true });
  return () => obs.disconnect();
}
