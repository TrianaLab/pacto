<script>
  import { onDestroy } from 'svelte';
  import { api } from './lib/api.ts';
  import { hashForHref, fleetEntityUrl } from './lib/router.ts';
  import EntityIdentity from './components/EntityIdentity.svelte';

  // Global entity search (requirement F): discovery across services, revisions,
  // deployments, owners and sources via /api/fleet/entities (the generated SDK
  // facade). It is a backend query, NOT a preloaded fleet list, so it respects the
  // backend's bounds and shows truncation. Keyboard model mirrors the command
  // palette: arrows move, Enter opens the exact entity, Escape closes, focus is
  // restored. Same-named entities across domains are NOT collapsed -- each result
  // carries enough identity (domain / scope / parent service) to disambiguate.
  let { open = false, onClose } = $props();

  let query = $state('');
  let results = $state([]);
  let total = $state(0);
  let truncated = $state(false);
  let loading = $state(false);
  let error = $state(null);
  let selectedIdx = $state(0);
  let inputEl = $state(null);
  let prevFocus = null;
  let debounceTimer = null;
  // seq is the active-search generation. A response may touch the UI only if its
  // generation still matches (mySeq === seq). It is advanced on EVERY transition that
  // ends the current search -- a new/changed query, a cleared query, an open, a close
  // and destroy -- so a response from an abandoned search can never repopulate the UI
  // (the A4 stale-request race). seq is a plain (non-reactive) counter deliberately.
  let seq = 0;

  // Reset + focus on open; restore focus on close. Any open/close transition
  // invalidates in-flight responses from the prior modal state.
  $effect(() => {
    seq++;
    if (open) {
      query = ''; results = []; total = 0; truncated = false; loading = false; error = null; selectedIdx = 0;
      prevFocus = document.activeElement;
      queueMicrotask(() => inputEl?.focus());
    } else if (prevFocus) {
      prevFocus.focus?.();
      prevFocus = null;
    }
  });

  // Debounced backend search. The generation is advanced FIRST, so clearing the query
  // (or any change) immediately invalidates a request already in flight.
  $effect(() => {
    const q = query.trim();
    clearTimeout(debounceTimer);
    const mySeq = ++seq;
    if (!q) { results = []; total = 0; truncated = false; loading = false; error = null; return; }
    loading = true;
    debounceTimer = setTimeout(() => {
      api.fleetEntities({ text: q, limit: 25 })
        .then((res) => {
          if (mySeq !== seq) return;
          results = res.entities || [];
          total = res.total ?? results.length;
          truncated = total > results.length;
          error = null;
        })
        .catch((e) => {
          if (mySeq !== seq) return;
          error = e; results = []; total = 0; truncated = false;
        })
        .finally(() => { if (mySeq === seq) loading = false; });
    }, 140);
  });

  // Destroy invalidates any in-flight response so it can never write to torn-down state.
  onDestroy(() => { seq++; });

  // Keep the selection in range as results change.
  $effect(() => { if (selectedIdx > results.length - 1) selectedIdx = Math.max(0, results.length - 1); });

  function hrefFor(r) { return r.href ? hashForHref(r.href) : fleetEntityUrl(r.kind, r.key); }
  function openResult(r) { if (!r) return; onClose?.(); location.hash = hrefFor(r); }

  let panelEl = $state(null);

  function onKeydown(e) {
    if (e.key === 'ArrowDown') { e.preventDefault(); selectedIdx = Math.min(selectedIdx + 1, results.length - 1); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); selectedIdx = Math.max(selectedIdx - 1, 0); }
    else if (e.key === 'Enter') { e.preventDefault(); openResult(results[selectedIdx]); }
    else if (e.key === 'Escape') { e.preventDefault(); onClose?.(); }
  }

  // Focus trap for the modal dialog (requirement 8.3): Tab cycles within the panel's
  // focusable elements (the input + result buttons) so focus never escapes an open modal
  // search; Escape closes from any focus position (a result button, not only the input).
  function onPanelKeydown(e) {
    if (e.key === 'Escape') { e.preventDefault(); onClose?.(); return; }
    if (e.key !== 'Tab') return;
    const focusable = Array.from(panelEl?.querySelectorAll('input, a[href], button:not([disabled])') || []);
    if (focusable.length === 0) return;
    const idx = focusable.indexOf(document.activeElement);
    const next = e.shiftKey
      ? (idx <= 0 ? focusable.length - 1 : idx - 1)
      : (idx >= focusable.length - 1 ? 0 : idx + 1);
    e.preventDefault();
    focusable[next]?.focus();
  }
</script>

{#if open}
  <div class="es-backdrop" onclick={onClose} role="presentation">
    <div class="es-panel" role="dialog" aria-modal="true" aria-label="Search the fleet" tabindex="-1" bind:this={panelEl} onkeydown={onPanelKeydown} onclick={(e) => e.stopPropagation()}>
      <div class="es-input-row">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="16" height="16"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
        <input
          bind:this={inputEl}
          bind:value={query}
          type="text"
          placeholder="Search services, revisions, deployments, owners, sources..."
          aria-label="Search the fleet"
          onkeydown={onKeydown}
        />
        <kbd>Esc</kbd>
      </div>
      <div class="es-results" role="listbox">
        {#if loading}
          <div class="es-msg">Searching…</div>
        {:else if error}
          <div class="es-msg es-err">Search unavailable: {error.message}</div>
        {:else if !query.trim()}
          <div class="es-msg">Type to search across the fleet. Search is discovery, not a full listing.</div>
        {:else if results.length === 0}
          <div class="es-msg">No entities match "{query}".</div>
        {:else}
          {#each results as r, i (r.kind + '::' + r.key)}
            <button
              type="button"
              class="es-item"
              class:selected={i === selectedIdx}
              role="option"
              aria-selected={i === selectedIdx}
              data-testid="search-result"
              onmouseenter={() => { selectedIdx = i; }}
              onclick={() => openResult(r)}
            >
              <EntityIdentity ref={r} showStatus={true} />
            </button>
          {/each}
          {#if truncated}
            <div class="es-trunc">Showing {results.length} of {total}. Refine your search to narrow it.</div>
          {/if}
        {/if}
      </div>
    </div>
  </div>
{/if}

<style>
  .es-backdrop {
    position: fixed; inset: 0; z-index: 1000;
    background: color-mix(in srgb, #000 45%, transparent);
    display: flex; align-items: flex-start; justify-content: center;
    padding-top: 12vh; animation: es-fade 120ms ease-out both;
  }
  .es-panel {
    width: min(620px, 94vw); background: var(--c-surface);
    border: 1px solid var(--c-border); border-radius: var(--radius-md, 10px);
    box-shadow: var(--shadow-md); overflow: hidden; animation: es-rise 140ms ease-out both;
  }
  .es-input-row {
    display: flex; align-items: center; gap: var(--sp-2);
    padding: var(--sp-3) var(--sp-4); border-bottom: 1px solid var(--c-border); color: var(--c-text-3);
  }
  .es-input-row input { flex: 1; border: none; background: none; outline: none; font: inherit; font-size: var(--text-md, 1rem); color: var(--c-text); }
  .es-input-row kbd {
    padding: 2px 7px; border-radius: 3px; background: var(--c-surface-hover);
    border: 1px solid var(--c-border); font-family: var(--font-sans); font-size: var(--text-xs); color: var(--c-text-3);
  }
  .es-results { max-height: 56vh; overflow-y: auto; padding: var(--sp-2); }
  .es-msg { padding: var(--sp-4); color: var(--c-text-3); font-size: var(--text-sm); }
  .es-err { color: var(--c-err); }
  .es-item {
    display: flex; align-items: center; width: 100%; gap: var(--sp-2);
    padding: var(--sp-2) var(--sp-3); border: none; border-radius: var(--radius-xs);
    background: none; color: var(--c-text); font: inherit; text-align: left; cursor: pointer;
    min-height: var(--touch-min);
  }
  .es-item.selected { background: var(--c-surface-hover); }
  .es-trunc { padding: var(--sp-2) var(--sp-3); color: var(--c-text-3); font-size: var(--text-xs); }
  @keyframes es-fade { from { opacity: 0; } to { opacity: 1; } }
  @keyframes es-rise { from { opacity: 0; transform: translateY(-6px); } to { opacity: 1; transform: none; } }
  @media (prefers-reduced-motion: reduce) { .es-backdrop, .es-panel { animation: none; } }
</style>
