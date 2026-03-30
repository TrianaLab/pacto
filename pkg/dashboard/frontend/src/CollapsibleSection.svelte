<script>
  let { title, count, open = $bindable(false), id = '', children } = $props();
</script>

<section class="section" {id}>
  <button type="button" class="section-toggle" onclick={() => open = !open} aria-expanded={open}>
    <span class="section-title">
      {title}
      {#if count != null}<span class="tab-count">{count}</span>{/if}
    </span>
    <span class="toggle-icon" class:open>
      <svg viewBox="0 0 12 12" fill="none">
        <path d="M3 4.5L6 7.5L9 4.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
      </svg>
    </span>
  </button>
  {#if open}
    <div class="section-body">
      {@render children()}
    </div>
  {/if}
</section>

<style>
  .section-toggle {
    display: flex; align-items: center; justify-content: space-between;
    width: 100%; background: none; border: none;
    padding: var(--sp-3) 0; cursor: pointer;
    font: inherit; color: var(--c-text); text-align: left;
    border-radius: var(--radius-xs);
    transition: color var(--transition);
    min-height: var(--touch-min);
  }
  .section-toggle:hover .section-title { color: var(--c-accent); }
  .toggle-icon {
    color: var(--c-text-3);
    display: inline-flex;
    transition: transform 200ms ease;
    transform: rotate(-90deg);
    padding: var(--sp-2);
  }
  .toggle-icon.open { transform: rotate(0deg); }
  .toggle-icon svg { width: 14px; height: 14px; }
  .section-body {
    margin-top: var(--sp-3);
    animation: slideReveal 200ms ease-out both;
  }

  @keyframes slideReveal {
    from { opacity: 0; transform: translateY(-6px); }
    to { opacity: 1; transform: translateY(0); }
  }
</style>
