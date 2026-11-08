<script>
  // AnswerCard (SPEC 3): the figure's answer as facts. Flat stamps (double border, caps,
  // never rotated) carry the verdicts; one <dl> carries the caller's facts, then what
  // every answer states the same way: what the reader may not see (counted, never
  // named), the evidence and its completeness, asOf and provenance.
  import { completenessClass, completenessLabel, formatCount } from '../../lib/format.ts';

  /** @type {{ stamps?: { label: string, tone: string }[], testid?: string, lead?: import('svelte').Snippet | null, facts: import('svelte').Snippet, hidden?: number, completeness?: string, evidence?: import('svelte').Snippet | null, asOf?: string, provenance: string }} */
  let { stamps = [], testid = undefined, lead = null, facts, hidden = 0, completeness = undefined, evidence = null, asOf = '', provenance } = $props();
</script>

<div class="card ac" data-testid={testid}>
  {#if stamps.length}
    <p class="ac-stamps">{#each stamps as s (s.label)}<span class="stamp t-{s.tone}">{s.label}</span>{/each}</p>
  {/if}
  {#if lead}{@render lead()}{/if}
  <dl class="facts">
    {@render facts()}
    {#if hidden}
      <dt>You may not see</dt>
      <dd>{formatCount(hidden)}, not counted and never named</dd>
    {/if}
    <dt>Evidence</dt>
    <dd>
      <span class="badge wrap {completenessClass(completeness)}">{completenessLabel(completeness)}</span>
      {#if evidence}{@render evidence()}{/if}
    </dd>
    {#if asOf}<dt>As of</dt><dd>{asOf}</dd>{/if}
    <dt>Provenance</dt>
    <dd>{provenance}</dd>
  </dl>
</div>
