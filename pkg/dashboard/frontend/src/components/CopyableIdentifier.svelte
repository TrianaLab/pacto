<script>
  // Renders a canonical identifier as secondary, copyable metadata: monospace text
  // plus a copy button. Human labels are primary elsewhere; the raw key lives here.
  // display: what to show when it is not the value itself (a digest cut short);
  // the button and the hover still carry the whole value. iconOnly: only the
  // button shows, for a value a reader needs to copy but not to read.
  let { value = '', label = 'canonical key', display = '', iconOnly = false } = $props();

  // The value wraps rather than truncating: a raw key is copied, compared and pasted,
  // so every character stays on screen, ends included. One run of text, so selecting
  // it copies the whole value, as does the button. (The old CSS head/tail split left a
  // variable blank after its ellipsis and cut the tail mid-word: SPEC C6.)
  const shown = $derived(display || value);

  let copied = $state(false);
  let timer = null;

  async function copy() {
    try {
      await navigator.clipboard?.writeText(value);
      copied = true;
      clearTimeout(timer);
      timer = setTimeout(() => (copied = false), 1200);
    } catch {
      // Clipboard may be unavailable (insecure context, jsdom); fail quietly rather
      // than throw — the value is still visible and selectable.
      copied = false;
    }
  }
</script>

{#if value}
  <span class="copyable" title={value}>
    {#if !iconOnly}<code class="copyable-value"><bdi data-format={display && display !== value ? "short" : undefined}>{shown}</bdi></code>{/if}
    <button
      type="button"
      class="copyable-btn"
      onclick={copy}
      aria-label={copied ? 'Copied' : `Copy ${label}`}
    >{copied ? '✓' : '⧉'}</button>
  </span>
{/if}

<style>
  /* It shrinks inside any flex row, so the value wraps and the copy button stays on
     screen instead of both running off the edge. */
  .copyable {
    display: inline-flex; align-items: center; gap: var(--sp-1);
    max-width: 100%; min-width: 0; flex: 0 1 auto;
  }
  .copyable-value {
    display: inline-block; min-width: 0; flex: 0 1 auto; overflow-wrap: anywhere;
    font-family: var(--font-mono); font-size: var(--text-xs);
    color: var(--c-text-2); background: var(--c-surface-inset);
    padding: 1px 6px; border-radius: var(--radius-xs);
  }
  .copyable-btn {
    background: none; border: none; cursor: pointer; color: var(--c-text-3);
    font-size: var(--text-sm); line-height: 1; padding: 2px 4px; border-radius: var(--radius-xs);
    flex-shrink: 0;
  }
  .copyable-btn:hover { color: var(--c-text); background: var(--c-surface-inset); }
</style>
