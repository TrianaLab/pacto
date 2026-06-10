<script>
  import MarkdownView from './MarkdownView.svelte';

  // `doc` is a DocInfo-like object: { title, path, content, truncated }.
  // When null, the modal is closed.
  let { doc = null, onClose = () => {} } = $props();

  function onKeydown(e) {
    if (doc && e.key === 'Escape') onClose();
  }
</script>

<svelte:window onkeydown={onKeydown} />

{#if doc}
  <!-- Backdrop is presentational; Escape + the close button drive keyboard users. -->
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <div class="doc-modal-backdrop" role="presentation" onclick={onClose}>
    <div class="doc-modal" role="dialog" aria-modal="true" aria-label={doc.title || doc.path} tabindex="-1"
      onclick={(e) => e.stopPropagation()}>
      <div class="doc-modal-header">
        <div class="doc-modal-titles">
          <span class="doc-modal-title">{doc.title || doc.path}</span>
          {#if doc.path}<code class="doc-modal-path">{doc.path}</code>{/if}
        </div>
        <button type="button" class="doc-modal-close" onclick={onClose} aria-label="Close full screen">✕</button>
      </div>
      <div class="doc-modal-body">
        <MarkdownView content={doc.content} truncated={doc.truncated} />
      </div>
    </div>
  </div>
{/if}

<style>
  .doc-modal-backdrop {
    position: fixed;
    inset: 0;
    z-index: 1000;
    background: rgba(0, 0, 0, 0.55);
    display: flex;
    align-items: stretch;
    justify-content: center;
    padding: clamp(12px, 4vh, 48px) clamp(12px, 4vw, 64px);
    animation: fadeIn 120ms ease-out;
  }
  .doc-modal {
    display: flex;
    flex-direction: column;
    width: 100%;
    max-width: 980px;
    background: var(--c-surface);
    border: 1px solid var(--c-border);
    border-radius: var(--radius-sm);
    box-shadow: 0 12px 48px rgba(0, 0, 0, 0.35);
    overflow: hidden;
  }
  .doc-modal-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--sp-3);
    padding: var(--sp-3) var(--sp-4);
    border-bottom: 1px solid var(--c-border);
    background: var(--c-surface-inset);
    flex-shrink: 0;
  }
  .doc-modal-titles { display: flex; align-items: baseline; gap: var(--sp-2); min-width: 0; }
  .doc-modal-title { font-weight: 600; font-size: var(--text-md, 1rem); }
  .doc-modal-path { font-size: var(--text-xs); color: var(--c-text-3); }
  .doc-modal-close {
    flex-shrink: 0;
    background: none;
    border: 1px solid var(--c-border);
    border-radius: var(--radius-xs);
    color: var(--c-text-2);
    font: inherit;
    line-height: 1;
    padding: 6px 10px;
    cursor: pointer;
  }
  .doc-modal-close:hover { background: var(--c-surface-hover, var(--c-surface-inset)); color: var(--c-text); }
  .doc-modal-body {
    padding: var(--sp-4);
    overflow-y: auto;
    flex: 1 1 auto;
  }

  @keyframes fadeIn {
    from { opacity: 0; }
    to { opacity: 1; }
  }
</style>
