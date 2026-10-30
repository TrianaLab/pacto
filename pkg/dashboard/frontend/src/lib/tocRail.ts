/**
 * Geometry for the "On this page" rail (PageToc.svelte) when it holds more entries
 * than the viewport has room for: the list scrolls within itself, from wherever the
 * rail starts down to the fold, and keeps the current entry in view.
 */

/** railMaxHeight is how tall the rail's list may grow: from its top edge to the fold,
 *  less `gap`, but never below `floor` (a few rows stay usable on a short screen). */
export function railMaxHeight(viewport: number, top: number, gap: number, floor = 120): number {
  return Math.max(floor, Math.floor(viewport - top - gap));
}

/** revealScrollTop is the list scrollTop that shows an entry (its offset and height
 *  inside the list), moving the list as little as possible. An entry taller than the
 *  list shows its top. */
export function revealScrollTop(scrollTop: number, viewHeight: number, itemTop: number, itemHeight: number): number {
  if (itemTop < scrollTop || itemHeight > viewHeight) return itemTop;
  if (itemTop + itemHeight > scrollTop + viewHeight) return itemTop + itemHeight - viewHeight;
  return scrollTop;
}

const FOLD_GAP = 16;

/** railToFold is a Svelte action for a sticky contents rail that is its own scroll
 *  container: while the node is sticky its height runs from its top edge to the fold
 *  (refreshed once per frame on scroll and resize, as it rises to its sticky place),
 *  and each time `key` changes the current entry (`[aria-current="true"]`) is kept in
 *  view by scrolling the rail alone, never the page. Where the rail is not sticky (a
 *  narrow layout puts it in the page flow) it keeps its natural height. */
export function railToFold(node: HTMLElement, _key?: unknown) {
  let frame = 0;
  const fit = () => {
    node.style.maxHeight = getComputedStyle(node).position === 'sticky'
      ? `${railMaxHeight(window.innerHeight, node.getBoundingClientRect().top, FOLD_GAP)}px`
      : '';
  };
  const reveal = () => {
    const cur = node.querySelector<HTMLElement>('[aria-current="true"]');
    if (cur) node.scrollTop = revealScrollTop(node.scrollTop, node.clientHeight, cur.offsetTop, cur.offsetHeight);
  };
  const schedule = () => {
    if (!frame) frame = requestAnimationFrame(() => { frame = 0; fit(); });
  };
  fit();
  reveal();
  window.addEventListener('scroll', schedule, { passive: true });
  window.addEventListener('resize', schedule);
  return {
    update() { fit(); reveal(); },
    destroy() {
      if (frame) cancelAnimationFrame(frame);
      window.removeEventListener('scroll', schedule);
      window.removeEventListener('resize', schedule);
    },
  };
}
