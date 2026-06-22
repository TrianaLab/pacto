<script>
  import { complianceClass, complianceStatusClass } from '../lib/format.ts';

  let { score = undefined, status = undefined } = $props();

  let cssClass = $derived(
    score != null ? complianceClass(score) : status ? complianceStatusClass(status) : ''
  );
  let showScore = $derived(score != null && score >= 0);
</script>

{#if showScore}
  <span class="score {cssClass}">{score}<span class="score-unit">%</span></span>
{:else}
  <span class="text-dim">—</span>
{/if}

<style>
  .score {
    font-weight: 600;
  }
  .score-unit {
    font-size: 0.8em;
    font-weight: 500;
    color: var(--c-text-3);
    margin-left: 1px;
  }
  .score.score-ok {
    color: var(--c-ok);
  }
  .score.score-warn {
    color: var(--c-warn);
  }
  .score.score-err {
    color: var(--c-err);
  }
  .text-dim {
    color: var(--c-text-3);
  }
</style>
