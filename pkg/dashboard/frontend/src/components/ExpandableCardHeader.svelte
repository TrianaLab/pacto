<script>
  let { title, expanded, onToggle, disabled = false } = $props();
</script>

<button
  type="button"
  class="detail-card-header"
  class:expandable={!disabled}
  onclick={() => !disabled && onToggle()}
>
  <div class="detail-card-header-left">
    {#if !disabled}
      <span class="expand-icon" class:open={expanded}>
        <svg viewBox="0 0 12 12" fill="none">
          <path d="M3 4.5L6 7.5L9 4.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
      </span>
    {/if}
    <span class="detail-card-title">{title}</span>
  </div>
</button>

<style>
  .detail-card-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    width: 100%;
    padding: var(--sp-3);
    background: none;
    border: none;
    font: inherit;
    color: var(--c-text);
    text-align: left;
    gap: var(--sp-3);
  }
  .detail-card-header.expandable {
    cursor: pointer;
  }
  .detail-card-header.expandable:hover {
    background: var(--c-surface-hover, var(--c-surface-inset));
    border-radius: var(--radius-sm);
  }
  .detail-card-header-left {
    display: flex;
    align-items: center;
    gap: var(--sp-2);
    min-width: 0;
  }
  .expand-icon {
    display: inline-flex;
    color: var(--c-text-3);
    transition: transform 200ms ease;
    transform: rotate(-90deg);
    flex-shrink: 0;
  }
  .expand-icon.open {
    transform: rotate(0deg);
  }
  .expand-icon svg {
    width: 12px;
    height: 12px;
  }
  .detail-card-title {
    font-weight: 600;
  }
</style>
