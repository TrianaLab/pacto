<script>
  import HelpTip from './HelpTip.svelte';

  // The one line under a delivery page's title that answers the page's
  // question: the answer in bold, a few words after it, and what the page is
  // behind a (?) a reader opens on purpose.
  let { head = '', rest = '', tip = '', tipLabel = 'this page', testid = 'page-answer' } = $props();
</script>

<p class="dl-lead" data-testid={testid}>
  <strong>{head}</strong>{#if rest}{' '}{rest}{/if}
  {#if tip}<HelpTip label={tipLabel} text={tip} />{/if}
</p>

<style>
  .dl-lead { margin: 0; max-width: 72ch; font-size: var(--text-base, 1rem); color: var(--c-text-2); }
  .dl-lead :global(.help) { vertical-align: middle; margin-left: var(--sp-1); }
  .dl-lead strong { color: var(--c-text); }
</style>
