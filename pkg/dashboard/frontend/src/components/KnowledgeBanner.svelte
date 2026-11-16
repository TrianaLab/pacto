<script>
  import { knowledgeTone } from '../lib/entityLabels.ts';
  import { degradedSourceSummary, watchedScope, scopeLabels } from '../lib/knowledgeState.ts';
  import { joinAnd } from '../lib/answers.ts';

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
  // sources, when given, names the sources behind the caveat ("edge-cluster and
  // registry-mirror"); tone overrides the colour a page wants for it (the overview
  // keeps an unavailable source a caution: the answer above it already says what is
  // broken). Partial and unknown knowledge are the neutral hatch, never a status tone.
  let { knowledge = {}, noun = 'view', testid = undefined, sources = [], tone = '' } = $props();

  const names = $derived.by(() => {
    const ids = (sources ?? []).filter((s) => s?.status && s.status !== 'available').map((s) => s.id);
    return ids.length > 4 ? [...ids.slice(0, 3), `${ids.length - 3} more`] : ids;
  });

  // A fleet read from a namespace list says so first: "Watching 2 namespaces (ledger
  // and payments)." Everything outside those namespaces is unknown, never empty.
  const scope = $derived(watchedScope(sources));
  const scopeShown = $derived(scopeLabels(scope.names, scope.count));

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

{#if knowledge.incomplete || scope.count}
  <div class="knowledge tone-{tone || knowledgeTone(knowledge.level)}" role="status" data-testid={testid}>
    {#if scope.count}
      <strong data-testid="watched-scope" title={joinAnd(scope.names)}>Watching {scope.count} {scope.count === 1 ? 'namespace' : 'namespaces'} ({#each scopeShown as n, i}{i === 0 ? '' : i === scopeShown.length - 1 ? ' and ' : ', '}{#if n.endsWith(' more')}{n}{:else}<bdi>{n}</bdi>{/if}{/each}).</strong>
    {:else}
      <strong>The fleet snapshot is missing data.</strong>
    {/if}
    {cause}{#if names.length}{' '}({joinAnd(names)}){/if}, so this {noun} may be incomplete.
  </div>
{/if}

<style>
  .knowledge {
    padding: var(--sp-2) var(--sp-3); border-radius: var(--radius-sm); font-size: var(--text-sm);
    background: var(--c-warn-bg); border: 1px solid var(--c-warn-border);
  }
  .knowledge.tone-err { background: var(--c-err-bg); border-color: color-mix(in srgb, var(--c-err) 30%, transparent); }
  /* The unknown family: a neutral box with the hatch as a band on its leading edge, so
     the pattern never sits under the words. */
  .knowledge.tone-neutral {
    background: var(--c-hatch) left / 6px 100% no-repeat, var(--c-neutral-bg);
    border-color: var(--c-neutral); padding-inline-start: calc(var(--sp-3) + 6px);
  }
</style>
