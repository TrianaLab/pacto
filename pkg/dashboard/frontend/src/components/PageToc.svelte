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
    scan();
    const mo = new MutationObserver(scan);
    mo.observe(document.body, { childList: true, subtree: true, attributes: true, attributeFilter: ['data-toc', 'id'] });
    return () => mo.disconnect();
  });

  // Open first, THEN scroll. A target inside a closed disclosure has no layout of its
  // own, so scrolling to it lands on whatever collapsed row happens to occupy that
  // pixel -- the reader is told they arrived somewhere they cannot see.
  function go(id) {
    const el = document.getElementById(id);
    if (!el) return;
    for (let p = el; p; p = p.parentElement) if (p.tagName === 'DETAILS') p.open = true;
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
        <span class="toc-title t-meta">{label}</span>
        <span class="toc-count t-meta">{entries.length}</span>
      </summary>
      <!-- Buttons, not links. An href="#section-id" would introduce a SECOND meaning for
           the URL fragment in a hash-routed application: clicking one would leave the
           route, and Back would walk through jump targets instead of pages. -->
      <ul class="toc-list">
        {#each entries as e (e.id)}
          <li><button type="button" class="toc-link" onclick={() => go(e.id)}>{e.label}</button></li>
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
  .toc-title { font-weight: 600; }
  .toc-count { margin-left: auto; color: var(--c-text-3); }

  .toc-list { list-style: none; margin: 0; padding: 0 var(--sp-2) var(--sp-2); display: flex; flex-direction: column; }
  .toc-link {
    display: block; width: 100%; text-align: left;
    padding: var(--sp-2) var(--sp-2); min-height: var(--touch-min);
    background: none; border: none; border-left: 2px solid transparent;
    color: var(--c-text-2); font: inherit; font-size: var(--text-sm); cursor: pointer;
  }
  .toc-link:hover { color: var(--c-accent); background: var(--c-surface-hover); border-left-color: var(--c-accent); }

  /* Wide viewports: the same control, parked beside the content. `align-self: start` on
     the grid item is what lets `sticky` do anything at all -- a stretched grid child is
     already as tall as the row and has nowhere to stick to. */
  @media (min-width: 1100px) {
    .toc { position: sticky; top: calc(var(--navbar-h) + var(--sp-4)); align-self: start; }
  }
</style>
