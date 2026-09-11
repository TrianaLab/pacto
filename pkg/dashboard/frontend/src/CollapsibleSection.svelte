<script>
  import { sourceTooltip } from './lib/format.ts';
  let { title, count, source = '', open = $bindable(false), id = '', children } = $props();
</script>

<section class="section" {id}>
  <!-- The toggle is wrapped in an h2, the WAI-ARIA accordion pattern. Without it a
       section title is a button and nothing else, so the page jumps straight from its
       h1 to the h3s inside a section body -- a skipped level (WCAG 1.3.1) and, worse,
       a document outline with no sections in it. h2 is the only level that fits: every
       host of this component renders it directly under the page h1. -->
  <h2 class="section-heading">
    <button type="button" class="section-toggle" onclick={() => open = !open} aria-expanded={open}>
      <span class="section-title">
        {title}
        {#if count != null}<span class="tab-count">{count}</span>{/if}
        {#if source}<span class="source-dot source-dot-{source}" data-tip={`Provided by ${sourceTooltip(source)}`}></span>{/if}
      </span>
      <span class="toggle-icon" data-motion class:open>
        <svg viewBox="0 0 12 12" fill="none">
          <path d="M3 4.5L6 7.5L9 4.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
        </svg>
      </span>
    </button>
  </h2>
  {#if open}
    <div class="section-body">
      {@render children()}
    </div>
  {/if}
</section>

<style>
  /* The h2 is structure only -- the button inside it carries every visual. Reset the
     UA heading styles so wrapping changed the outline and nothing else; without this
     `font: inherit` on the button would inherit the UA h2 font instead of the page's. */
  .section-heading {
    margin: 0;
    font: inherit;
    /* letter-spacing is inherited and NOT part of the `font` shorthand, so the h2
       tag rule's tightening would reach the button through it. */
    letter-spacing: normal;
  }
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
    transform: rotate(-90deg);
    padding: var(--sp-2);
  }
  .toggle-icon.open { transform: rotate(0deg); }
  .toggle-icon svg { width: 14px; height: 14px; }
  .section-body {
    margin-top: var(--sp-3);
  }
</style>
