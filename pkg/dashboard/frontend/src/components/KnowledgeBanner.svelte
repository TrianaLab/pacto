<script>
  import { knowledgeTone } from '../lib/entityLabels.ts';
  import { degradedSourceSummary } from '../lib/knowledgeState.ts';

  // "What we could not see" is the same caveat on every product surface, and it was
  // written out five times: four class names, four copies of the same CSS and three
  // different punctuation marks between the two clauses (an em dash, a hyphen and a
  // colon). One sentence, one style, one place to change the wording. `noun` is the
  // only part that legitimately differs — a list, a neighborhood, a page.
  //
  // The caveat names its own SCOPE, and that is not cosmetic. It used to open with the
  // bare level label, which on the worst level reads "Source unavailable" — a sentence
  // fragment about a source, printed at the top of whatever page you are on. On a data
  // source's own page, badged Available a hundred pixels higher, it flatly contradicted
  // the page. The snapshot's knowledge and the health of the thing being looked at are
  // two different facts, so the banner says which one it is talking about and then says
  // how many OTHER sources are behind it.
  let { knowledge = {}, noun = 'view', testid = undefined } = $props();

  // Why the snapshot is short. A per-source count where there is one; otherwise the
  // reason is not in the source list at all -- either the snapshot declared itself
  // partial, or no meta arrived to declare anything.
  const cause = $derived(
    degradedSourceSummary(knowledge)
      || (knowledge.level === 'unknown'
        ? 'It did not report how complete it is'
        : 'It reports itself partial'),
  );
</script>

{#if knowledge.incomplete}
  <div class="knowledge tone-{knowledgeTone(knowledge.level)}" role="status" data-testid={testid}>
    <strong>The fleet snapshot is missing data.</strong>
    {cause}, so this {noun} may be incomplete.
  </div>
{/if}

<style>
  .knowledge {
    padding: var(--sp-2) var(--sp-3); border-radius: var(--radius-sm); font-size: var(--text-sm);
    background: var(--c-warn-bg); border: 1px solid var(--c-warn-border);
  }
  .knowledge.tone-err { background: var(--c-err-bg); border-color: color-mix(in srgb, var(--c-err) 30%, transparent); }
</style>
