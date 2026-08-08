<script>
  // Renders a canonical identifier as secondary, copyable metadata: monospace text
  // plus a copy button. Human labels are primary elsewhere; the raw key lives here.
  let { value = '', label = 'canonical key' } = $props();

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
    <code class="copyable-value">{value}</code>
    <button
      type="button"
      class="copyable-btn"
      onclick={copy}
      aria-label={copied ? 'Copied' : `Copy ${label}`}
    >{copied ? '✓' : '⧉'}</button>
  </span>
{/if}

<style>
  .copyable {
    display: inline-flex; align-items: center; gap: var(--sp-1);
    max-width: 100%; min-width: 0;
  }
  .copyable-value {
    font-family: var(--font-mono); font-size: var(--text-xs);
    color: var(--c-text-2); background: var(--c-surface-inset);
    padding: 1px 6px; border-radius: var(--radius-xs);
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
    word-break: break-all;
  }
  .copyable-btn {
    background: none; border: none; cursor: pointer; color: var(--c-text-3);
    font-size: var(--text-sm); line-height: 1; padding: 2px 4px; border-radius: var(--radius-xs);
  }
  .copyable-btn:hover { color: var(--c-text); background: var(--c-surface-inset); }
</style>
