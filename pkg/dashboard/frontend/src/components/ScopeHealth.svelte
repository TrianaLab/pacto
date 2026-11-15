<script>
  // The health and knowledge of one owner's or one domain's estate, from its summary
  // (OwnerSummary): the health check of each target, what the contracts leave out with a
  // row per gap kind, and a facts line. Every number with a list filter opens the list
  // scoped to this estate (`filter` is { businessDomain } or { ownerKey }).
  import { fleetServicesUrl, fleetAttentionUrl } from '../lib/router.ts';
  import { healthSegments, knowledgeSegments, KNOWLEDGE_GAPS } from '../lib/distributions.ts';
  import { GAP_WORDS } from '../lib/entityLabels.ts';
  import { healthCaption, knowledgeCaption } from '../lib/answers.ts';
  import { formatCount } from '../lib/format.ts';
  import DistributionBar from './viz/DistributionBar.svelte';

  /** @type {{ summary: any, filter: { businessDomain?: string, ownerKey?: string }, noun: string }} */
  let { summary = {}, filter, noun } = $props();
  const targets = $derived(summary.targets ?? 0);
  const health = $derived(summary.health ?? {});
  const k = $derived(summary.knowledge ?? {});
  const services = $derived(k.services ?? summary.services ?? 0);
  const gapUrl = (gap) => fleetServicesUrl({ ...filter, gap });
  const gapRows = $derived(KNOWLEDGE_GAPS.map((g) => ({ ...g, label: GAP_WORDS[g.value], count: k.gaps?.[g.field] || 0 })).filter((g) => g.count > 0));
  const r = $derived(summary.readiness ?? {});
  const nowhere = $derived(summary.notDeployed ?? 0);
</script>

<!-- A count that opens the list it counts, when there is anything in it. -->
{#snippet cnt(v, href)}
  {#if v > 0}<a {href}>{formatCount(v)}</a>{:else}{formatCount(v || 0)}{/if}
{/snippet}

<div class="sh" data-testid="scope-health">
  <DistributionBar
    title="Health checks"
    level={3}
    description="A target's own health check, answered on its own. Failing is an observed problem; unknown means Pacto cannot tell."
    scopeNote={`All ${formatCount(targets)} operational ${targets === 1 ? 'target' : 'targets'} ${noun}.`}
    segments={healthSegments(health, { unavailable: fleetAttentionUrl({ ...filter, category: 'health' }) })}
    total={targets}
    emptyLabel="Nothing running has been observed here yet, so there is no health picture."
  />
  {#if healthCaption(health)}<p class="sh-caption t-body-2">{healthCaption(health)}</p>{/if}
</div>
<div class="sh" data-testid="scope-knowledge">
  <DistributionBar
    title="How much the contracts say"
    level={3}
    description="What the contracts leave out. Unknown means Pacto could not read enough to tell."
    scopeNote={`All ${formatCount(services)} ${services === 1 ? 'service' : 'services'} ${noun}, golden paths apart.`}
    segments={knowledgeSegments(k, { unknown: gapUrl('unknown') })}
    total={services}
    emptyLabel="No services here yet, so there is no knowledge picture."
  />
  {#if knowledgeCaption(k)}<p class="sh-caption t-body-2">{knowledgeCaption(k)}</p>{/if}
  {#if gapRows.length}
    <ul class="sh-gaps" data-testid="scope-gaps" aria-label="Services per knowledge gap">
      {#each gapRows as g (g.value)}
        <li><a href={gapUrl(g.value)}><span>{g.label}</span><span class="sh-n">{formatCount(g.count)}</span></a></li>
      {/each}
    </ul>
  {/if}
</div>
<!-- Runs nowhere counts only services whose every target Pacto can see; the rest are in
     "cannot tell", never in runs nowhere. -->
<p class="sh-facts t-body-2" data-testid="scope-facts">
  {@render cnt(nowhere, gapUrl('not-deployed'))} {nowhere === 1 ? 'runs' : 'run'} nowhere
  {#if k.unknown} · Pacto cannot tell for {@render cnt(k.unknown, gapUrl('unknown'))}{/if}
  · readiness {formatCount(r.passing ?? 0)} passing, {formatCount(r.belowThreshold ?? 0)} below, {formatCount(r.expired ?? 0)} expired, {formatCount(r.notDeclared ?? 0)} not declared
  · {formatCount(summary.unresolvedDependencies ?? 0)} unresolved and {formatCount(summary.undeclaredDependencies ?? 0)} undeclared dependencies
</p>

<style>
  .sh { display: flex; flex-direction: column; gap: var(--sp-2); min-width: 0; }
  .sh-caption, .sh-facts { margin: 0; color: var(--c-text-2); }
  .sh-facts a { color: var(--c-accent); text-decoration: underline; }
  .sh-gaps { list-style: none; margin: 0; padding: 0; display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 18rem), 1fr)); gap: var(--sp-1) var(--sp-4); }
  .sh-gaps a { display: flex; justify-content: space-between; align-items: baseline; gap: var(--sp-2); min-height: 2rem; padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); color: var(--c-text); text-decoration: none; }
  .sh-gaps a:hover, .sh-gaps a:focus-visible { border-color: var(--c-accent); }
  @media (pointer: coarse) { .sh-gaps a { min-height: 44px; } }
  .sh-n { font-variant-numeric: tabular-nums; font-weight: 600; }
</style>
