<script>
  // "On this page": ONE navigator, shared by every long product page.
  //
  // Entries are DISCOVERED from the rendered DOM -- every element carrying an id and a
  // `data-toc` label -- and never from a hand-written list beside the page. A contents
  // list that offers a section the page did not render is worse than no contents list:
  // it promises information that does not exist. Sections come and go with the data (a
  // band gated on a non-empty population, an entity block that only some kinds have), so
  // the list is rebuilt from what is actually on screen.
  //
  // It is the SAME control on both form factors: a sticky rail beside the content where
  // there is room for one, and a closed disclosure under the page title where there is
  // not. A <details> is both, which is why there is no second implementation and no
  // ARIA of our own -- the open/closed state is native, and so is keyboard and touch.
  let { label = 'On this page', minEntries = 3, wide = '(min-width: 1100px)' } = $props();

  let entries = $state([]);
  let open = $state(false);
  let current = $state('');

  // The rail is open by default; the mobile disclosure is closed by default, because
  // there it costs a screenful. After a reader touches it, their choice stands.
  let touched = false;
  $effect(() => {
    const mq = window.matchMedia?.(wide);
    if (!mq) return;
    const apply = () => { if (!touched) open = mq.matches; };
    apply();
    mq.addEventListener('change', apply);
    return () => mq.removeEventListener('change', apply);
  });

  function scan() {
    // The observer watches a GLOBAL and its records are delivered asynchronously, so a
    // mutation recorded just before teardown still arrives after it -- possibly after the
    // host has torn the document down entirely.
    if (typeof document === 'undefined') return;
    const seen = new Set();
    const next = [];
    for (const el of document.querySelectorAll('[data-toc][id]')) {
      if (seen.has(el.id)) continue;
      seen.add(el.id);
      next.push({ id: el.id, label: el.getAttribute('data-toc') });
    }
    // Re-assigning on every mutation would re-render the rail constantly; the list only
    // changes when a section appears, disappears or is renamed.
    const same = next.length === entries.length && next.every((e, i) => e.id === entries[i].id && e.label === entries[i].label);
    if (!same) entries = next;
  }

  $effect(() => {
    const rescan = () => { scan(); schedule(); };
    rescan();
    const mo = new MutationObserver(rescan);
    mo.observe(document.body, { childList: true, subtree: true, attributes: true, attributeFilter: ['data-toc', 'id'] });
    return () => mo.disconnect();
  });

  // WHICH entry is current: ONE rule, evaluated from geometry, with no intersection
  // ratios and no ties to break.
  //
  //   the current section is the LAST one whose top edge has reached the reading line;
  //   above the first section, the first section is current.
  //
  // The reading line is where a section COMES TO REST when it is scrolled to -- its own
  // `scroll-margin-top`, which exists so a heading clears the sticky app bar. Reading it
  // from the CSS rather than restating the offset here is what makes clicking an entry
  // and scrolling to it agree: the pixel the browser parks the section at is the pixel
  // that makes it current.
  //
  // The alternative -- an IntersectionObserver over the sections -- was rejected on
  // purpose. Several sections intersect the viewport at once on any desktop page, so
  // "which of the visible ones is THE one" needs a tie-break anyway, and a ratio-based
  // one oscillates: a tall section and a short section crossing the same edge swap
  // places frame by frame as the reader scrolls slowly. Ordering by a single line
  // cannot tie, so it cannot oscillate.
  function pick() {
    // A pinned choice is the reader's own, made a moment ago by clicking an entry; the
    // programmatic smooth scroll on its way there must not walk `current` through every
    // section it passes, and a short last section that cannot reach the line must not
    // undo the click at all.
    if (pinned || typeof document === 'undefined' || entries.length === 0) return;
    let next = '';
    for (const e of entries) {
      const el = document.getElementById(e.id);
      // No layout means the section is inside a collapsed disclosure (or otherwise not
      // rendered). It stays in the list and stays clickable -- go() opens its ancestors
      // -- but a box with no position on the page cannot be the one being read, and
      // treating its zeroed rect as "top: 0" would make it beat every real section.
      if (!el || el.getClientRects().length === 0) continue;
      // EACH section against its OWN line. There is a shared default for `[data-toc]`,
      // but nothing forces a section to keep it -- one under a sticky sub-header parks
      // lower -- and measuring every section against the FIRST visible one's margin
      // answers for a pixel the browser does not park the others at.
      const line = parseFloat(getComputedStyle(el).scrollMarginTop) || 0;
      if (!next || el.getBoundingClientRect().top <= line + 1) next = e.id;
    }
    current = next;
  }

  // Coalesced to one evaluation per frame: a scroll fires far more often than the page
  // can paint, and the answer cannot change between paints.
  let frame = 0;
  let pinned = false;
  function schedule() {
    if (frame || typeof requestAnimationFrame === 'undefined') return;
    frame = requestAnimationFrame(() => { frame = 0; pick(); });
  }

  // The same three events scrollRestore treats as "the reader is driving now". Any of
  // them releases a pinned choice, because from then on the geometry is the truth again.
  const DRIVING = ['wheel', 'touchstart', 'keydown'];
  $effect(() => {
    const release = () => { pinned = false; schedule(); };
    window.addEventListener('scroll', schedule, { passive: true });
    window.addEventListener('resize', schedule);
    for (const t of DRIVING) window.addEventListener(t, release, { passive: true });
    return () => {
      if (frame) cancelAnimationFrame(frame);
      window.removeEventListener('scroll', schedule);
      window.removeEventListener('resize', schedule);
      for (const t of DRIVING) window.removeEventListener(t, release);
    };
  });

  // Open first, THEN scroll. A target inside a closed disclosure has no layout of its
  // own, so scrolling to it lands on whatever collapsed row happens to occupy that
  // pixel -- the reader is told they arrived somewhere they cannot see.
  function go(id) {
    const el = document.getElementById(id);
    if (!el) return;
    for (let p = el; p; p = p.parentElement) if (p.tagName === 'DETAILS') p.open = true;
    // Answer the click immediately and hold that answer. The section is current because
    // the reader chose it, not because a scroll animation eventually gets there.
    current = id;
    pinned = true;
    const reduce = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
    el.scrollIntoView?.({ behavior: reduce ? 'auto' : 'smooth', block: 'start' });
    // Send the keyboard caret with the viewport, so the next Tab continues from the
    // section the reader asked for rather than from the next rail entry.
    el.setAttribute('tabindex', '-1');
    el.focus({ preventScroll: true });
  }
</script>

{#if entries.length >= minEntries}
  <nav class="toc" aria-label={label} data-testid="page-toc">
    <details class="toc-box disclosure" bind:open>
      <!-- The handler is on the SUMMARY, the interactive element, so a keyboard toggle
           (Enter/Space, which the browser delivers here as a click) counts the same as a
           tap. On <details> it would be a mouse listener on a non-interactive element. -->
      <summary class="toc-summary" onclick={() => { touched = true; }}>
        <span class="disclosure-caret" aria-hidden="true">&#9656;</span>
        <span class="toc-title t-label">{label}</span>
        <span class="toc-count t-meta">{entries.length}</span>
      </summary>
      <!-- Buttons, not links. An href="#section-id" would introduce a SECOND meaning for
           the URL fragment in a hash-routed application: clicking one would leave the
           route, and Back would walk through jump targets instead of pages. -->
      <ul class="toc-list">
        {#each entries as e (e.id)}
          <!-- aria-current is the state itself, not a description of it: a screen reader
               announces "current" on the one entry, and it stays right when the list is
               rebuilt. The visual cue is a left marker AND a heavier weight, so it does
               not depend on telling accent from grey. -->
          <li><button
            type="button"
            class="toc-link"
            class:current={e.id === current}
            aria-current={e.id === current ? 'true' : undefined}
            onclick={() => go(e.id)}
          >{e.label}</button></li>
        {/each}
      </ul>
    </details>
  </nav>
{/if}

<style>
  /* Caret, hit area and open/closed behaviour come from the shared `.disclosure` in
     components.css -- this is the same control as every other product disclosure. */
  .toc { min-width: 0; }
  .toc-box { border: 1px solid var(--c-border); border-radius: var(--radius-md); background: var(--c-surface); }
  .toc-summary { padding: 0 var(--sp-3); list-style: none; }
  /* The control's name is a LABEL naming what sits beside it, which is a role we
     already have. It used to be t-meta re-weighted to 600 locally -- the one thing the
     role system forbids, and enough on its own to make t-meta render at two weights
     across the product. A role carries its own weight or it carries nothing. */
  .toc-title { min-width: 0; }
  .toc-count { margin-left: auto; color: var(--c-text-3); }

  .toc-list { list-style: none; margin: 0; padding: 0 var(--sp-2) var(--sp-2); display: flex; flex-direction: column; }
  .toc-link {
    display: block; width: 100%; text-align: left;
    padding: var(--sp-2) var(--sp-2); min-height: var(--touch-min);
    background: none; border: none; border-left: 2px solid transparent;
    color: var(--c-text-2); font: inherit; font-size: var(--text-sm); cursor: pointer;
  }
  .toc-link:hover { color: var(--c-accent); background: var(--c-surface-hover); border-left-color: var(--c-accent); }
  /* The marker APPEARS where there was none and the label thickens: both survive
     greyscale, so "where am I" is never carried by hue alone. */
  .toc-link.current { color: var(--c-text); font-weight: 600; border-left-color: var(--c-accent); }

  /* Wide viewports: the same control, parked beside the content. `align-self: start` on
     the grid item is what lets `sticky` do anything at all -- a stretched grid child is
     already as tall as the row and has nowhere to stick to. */
  @media (min-width: 1100px) {
    .toc { position: sticky; top: calc(var(--navbar-h) + var(--sp-4)); align-self: start; }
  }
</style>
