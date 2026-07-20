<script>
  import { readinessGateClass, readinessGateTip } from '../lib/format.ts';

  // `readiness` is the dashboard ReadinessInfo (has score, minScore, passing, expired).
  let { readiness = null } = $props();

  // Color by the GATE (passing), not the absolute score, so a service that
  // clears its minScore reads green and one below the gate reads amber/red —
  // even when their raw scores are close.
  let cssClass = $derived(readinessGateClass(readiness));
  let tip = $derived(readinessGateTip(readiness));
  let passing = $derived(!!readiness?.passing && !readiness?.expired);
  let showScore = $derived(readiness != null && readiness.score >= 0);
</script>

{#if showScore}
  <span class="readiness-score {cssClass}" data-tip={tip} aria-label={tip}>
    {readiness.score}<span class="score-unit">%</span>{#if passing}<span class="gate-check" aria-label="passes minScore">&#10003;</span>{/if}
  </span>
{:else}
  <span class="text-dim">—</span>
{/if}

<style>
  .readiness-score {
    font-weight: 600;
    display: inline-flex;
    align-items: baseline;
    gap: 1px;
  }
  .score-unit {
    font-size: 0.8em;
    font-weight: 500;
    color: var(--c-text-3);
    margin-left: 1px;
  }
  .gate-check {
    font-size: 0.85em;
    font-weight: 700;
    margin-left: 3px;
    color: var(--c-ok);
  }
  .readiness-score.score-ok { color: var(--c-ok); }
  .readiness-score.score-warn { color: var(--c-warn); }
  .readiness-score.score-err { color: var(--c-err); }
  .text-dim { color: var(--c-text-3); }
</style>
