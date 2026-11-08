<script>
  // AnswerCard (SPEC 3): the figure's answer as facts. Flat stamps (double border,
  // caps, never rotated) carry the verdicts; the <dl> the caller passes carries the
  // facts, asOf, provenance, completeness and what the reader may not see.
  /** @type {{ stamps?: { label: string, tone: string }[], testid?: string, children: import('svelte').Snippet }} */
  let { stamps = [], testid = undefined, children } = $props();
</script>

<div class="card ac" data-testid={testid}>
  {#if stamps.length}
    <p class="ac-stamps">{#each stamps as s (s.label)}<span class="stamp t-{s.tone}">{s.label}</span>{/each}</p>
  {/if}
  {@render children()}
</div>
