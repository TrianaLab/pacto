<script>
  // Renders a canonical identifier as secondary, copyable metadata: monospace text
  // plus a copy button. Human labels are primary elsewhere; the raw key lives here.
  // display: what to show when it is not the value itself (a digest cut short);
  // the button and the hover still carry the whole value. iconOnly: only the
  // button shows, for a value a reader needs to copy but not to read.
  let { value = '', label = 'canonical key', display = '', iconOnly = false } = $props();

  // Middle ellipsis: a squeezed identifier keeps its end (the version, the digest
  // tail) on screen, which is what tells two long keys apart. The two halves are one
  // run of text, so selecting it copies the whole value, as does the button.
  const shown = $derived(display || value);
  const tail = $derived(shown.length > 20 ? shown.slice(-10) : '');
  const head = $derived(tail ? shown.slice(0, -10) : shown);

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
    {#if !iconOnly}<code class="copyable-value"><span class="cv-head">{head}</span>{#if tail}<span class="cv-tail">{tail}</span>{/if}</code>{/if}
    <button
      type="button"
      class="copyable-btn"
      onclick={copy}
      aria-label={copied ? 'Copied' : `Copy ${label}`}
    >{copied ? '✓' : '⧉'}</button>
  </span>
{/if}

<style>
  /* It shrinks inside any flex row, so the value ellipsizes and the copy button stays
     on screen instead of both running off the edge. */
  .copyable {
    display: inline-flex; align-items: center; gap: var(--sp-1);
    max-width: 100%; min-width: 0; flex: 0 1 auto;
  }
  .copyable-value {
    display: inline-flex; min-width: 0; flex: 0 1 auto;
    font-family: var(--font-mono); font-size: var(--text-xs);
    color: var(--c-text-2); background: var(--c-surface-inset);
    padding: 1px 6px; border-radius: var(--radius-xs);
    white-space: nowrap;
  }
  .cv-head { overflow: hidden; text-overflow: ellipsis; min-width: 0; }
  .cv-tail { flex-shrink: 0; }
  .copyable-btn {
    background: none; border: none; cursor: pointer; color: var(--c-text-3);
    font-size: var(--text-sm); line-height: 1; padding: 2px 4px; border-radius: var(--radius-xs);
    flex-shrink: 0;
  }
  .copyable-btn:hover { color: var(--c-text); background: var(--c-surface-inset); }
</style>
