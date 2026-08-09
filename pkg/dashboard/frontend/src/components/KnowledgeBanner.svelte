<script>
  import { knowledgeLabel, knowledgeTone } from '../lib/entityLabels.ts';

  // "What we could not see" is the same caveat on every product surface, and it was
  // written out five times: four class names, four copies of the same CSS and three
  // different punctuation marks between the two clauses (an em dash, a hyphen and a
  // colon). One sentence, one style, one place to change the wording. `noun` is the
  // only part that legitimately differs — a list, a neighborhood, a page.
  let { knowledge = {}, noun = 'view', testid = undefined } = $props();
</script>

{#if knowledge.incomplete}
  <div class="knowledge tone-{knowledgeTone(knowledge.level)}" role="status" data-testid={testid}>
    {knowledgeLabel(knowledge.level)} — this {noun} may be incomplete.
  </div>
{/if}

<style>
  .knowledge {
    padding: var(--sp-2) var(--sp-3); border-radius: var(--radius-sm); font-size: var(--text-sm);
    background: var(--c-warn-bg); border: 1px solid var(--c-warn-border);
  }
  .knowledge.tone-err { background: var(--c-err-bg); border-color: color-mix(in srgb, var(--c-err) 30%, transparent); }
</style>
