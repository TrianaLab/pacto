<script>
  import { buildCommands, flattenCommands } from './lib/commands.ts';

  let { open = false, services = [], onClose, onAction } = $props();

  let query = $state('');
  let selectedIdx = $state(0);
  let inputEl = $state(null);
  let prevFocus = null;

  let groups = $derived(buildCommands(query, services));
  let flat = $derived(flattenCommands(groups));

  // Reset + focus each time the palette opens; restore focus on close.
  $effect(() => {
    if (open) {
      query = '';
      selectedIdx = 0;
      prevFocus = document.activeElement;
      queueMicrotask(() => inputEl?.focus());
    } else if (prevFocus) {
      prevFocus.focus?.();
      prevFocus = null;
    }
  });

  // Keep the selection in range as results shrink.
  $effect(() => { if (selectedIdx > flat.length - 1) selectedIdx = Math.max(0, flat.length - 1); });

  function activate(cmd) {
    if (!cmd) return;
    onClose?.();
    if (cmd.action) onAction?.(cmd.action);
    else if (cmd.href) location.hash = cmd.href;
  }

  function onKeydown(e) {
    if (e.key === 'ArrowDown') { e.preventDefault(); selectedIdx = Math.min(selectedIdx + 1, flat.length - 1); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); selectedIdx = Math.max(selectedIdx - 1, 0); }
    else if (e.key === 'Enter') { e.preventDefault(); activate(flat[selectedIdx]); }
    else if (e.key === 'Escape') { e.preventDefault(); onClose?.(); }
    else if (e.key === 'Tab') {
      e.preventDefault();
      // Focus trap: cycle among input + result buttons
      const focusable = Array.from(e.currentTarget.closest('.cp-panel')?.querySelectorAll('input, button') || []);
      if (!focusable.length) return;
      const current = document.activeElement;
      const idx = focusable.indexOf(current);
      const next = e.shiftKey
        ? (idx <= 0 ? focusable.length - 1 : idx - 1)
        : (idx >= focusable.length - 1 ? 0 : idx + 1);
      focusable[next]?.focus();
    }
  }

  // Flat index of the first item in each group, so hover/selection line up.
  function baseIndex(gi) {
    let n = 0;
    for (let i = 0; i < gi; i++) n += groups[i].items.length;
    return n;
  }
</script>

{#if open}
  <div class="cp-backdrop" onclick={onClose} role="presentation">
    <div
      class="cp-panel"
      role="dialog"
      aria-modal="true"
      aria-label="Command palette"
      onclick={(e) => e.stopPropagation()}
    >
      <div class="cp-input-row">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="16" height="16"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
        <input
          bind:this={inputEl}
          bind:value={query}
          type="text"
          placeholder="Jump to a service, owner, view or action..."
          aria-label="Command palette search"
          onkeydown={onKeydown}
        />
        <kbd>Esc</kbd>
      </div>
      <div class="cp-results" role="listbox">
        {#if flat.length === 0}
          <div class="cp-empty">No results for "{query}"</div>
        {:else}
          {#each groups as g, gi}
            <div class="cp-group-label">{g.label}</div>
            {#each g.items as cmd, ii}
              {@const idx = baseIndex(gi) + ii}
              <button
                type="button"
                class="cp-item"
                class:selected={idx === selectedIdx}
                role="option"
                aria-selected={idx === selectedIdx}
                onmouseenter={() => { selectedIdx = idx; }}
                onclick={() => activate(cmd)}
              >
                <span class="cp-item-label">{cmd.label}</span>
                {#if cmd.hint}<span class="cp-item-hint">{cmd.hint}</span>{/if}
                <span class="cp-item-kind">{cmd.kind}</span>
              </button>
            {/each}
          {/each}
        {/if}
      </div>
    </div>
  </div>
{/if}

<style>
  .cp-backdrop {
    position: fixed; inset: 0; z-index: 1000;
    background: color-mix(in srgb, #000 45%, transparent);
    display: flex; align-items: flex-start; justify-content: center;
    padding-top: 12vh;
    animation: cp-fade 120ms ease-out both;
  }
  .cp-panel {
    width: min(560px, 92vw);
    background: var(--c-surface);
    border: 1px solid var(--c-border);
    border-radius: var(--radius-md, 10px);
    box-shadow: var(--shadow-md);
    overflow: hidden;
    animation: cp-rise 140ms ease-out both;
  }
  .cp-input-row {
    display: flex; align-items: center; gap: var(--sp-2);
    padding: var(--sp-3) var(--sp-4);
    border-bottom: 1px solid var(--c-border);
    color: var(--c-text-3);
  }
  .cp-input-row input {
    flex: 1; border: none; background: none; outline: none;
    font: inherit; font-size: var(--text-md, 1rem); color: var(--c-text);
  }
  .cp-input-row kbd {
    padding: 2px 7px; border-radius: 3px;
    background: var(--c-surface-hover); border: 1px solid var(--c-border);
    font-family: var(--font-sans); font-size: var(--text-xs); color: var(--c-text-3);
  }
  .cp-results { max-height: 52vh; overflow-y: auto; padding: var(--sp-2); }
  .cp-empty { padding: var(--sp-4); color: var(--c-text-3); font-size: var(--text-sm); }
  .cp-group-label {
    padding: 8px var(--sp-3) 4px; font-size: 10px; font-weight: 600;
    text-transform: uppercase; letter-spacing: 0.05em; color: var(--c-text-3);
  }
  .cp-item {
    display: flex; align-items: center; gap: var(--sp-2); width: 100%;
    padding: var(--sp-2) var(--sp-3); border: none; border-radius: var(--radius-xs);
    background: none; color: var(--c-text); font: inherit; font-size: var(--text-sm);
    text-align: left; cursor: pointer; min-height: var(--touch-min);
  }
  .cp-item.selected { background: var(--c-surface-hover); }
  .cp-item-label { font-weight: 500; }
  .cp-item-hint { color: var(--c-text-3); font-size: var(--text-xs); }
  .cp-item-kind {
    margin-left: auto; font-size: 10px; text-transform: uppercase;
    letter-spacing: 0.04em; color: var(--c-text-3);
  }
  @keyframes cp-fade { from { opacity: 0; } to { opacity: 1; } }
  @keyframes cp-rise { from { opacity: 0; transform: translateY(-6px); } to { opacity: 1; transform: none; } }
  @media (prefers-reduced-motion: reduce) {
    .cp-backdrop, .cp-panel { animation: none; }
  }
</style>
