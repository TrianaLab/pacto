<script>
  import { formatCount, countOf } from '../lib/format.ts';
  import { formatRelative } from '../lib/dateFormat.ts';
  import EntityLink from './EntityLink.svelte';
  import IdentityBadge from './IdentityBadge.svelte';
  import PreviewSection from './PreviewSection.svelte';

  // Whether an offering (or one provider, `compact`) answers in each environment, and how
  // Pacto knows: observed from the provider's own targets, inferred from what its
  // consumers report, unknown when nothing fresh is seen. It reads the availability
  // answer only: provider compliance is a contract verdict and is never availability.
  // #463 places its adapter block after this component.
  let { availability = null, compact = false, hiddenProviders = 0 } = $props();
  const STATE_TONE = { available: 'ok', degraded: 'warn', unavailable: 'err', unknown: 'neutral' };
  const PROV_TONE = { observed: 'info', inferred: 'info', unknown: 'neutral' };
  const PARTIAL = 'consumers you may not see are not counted';
  const HELP = 'Whether the offering answers where it runs, from its targets and from what its consumers report. Separate from the resources consumers asked for, below.';
  const rows = $derived(availability?.byEnvironment ?? []);
  const total = $derived(availability?.total ?? rows.length);
</script>

{#snippet list()}
  <ul class="oa-list" data-testid="availability">
    {#each rows as e, i (`${e.environment}:${i}`)}
      <li class="oa-row" data-testid="availability-env" data-state={e.state}>
        <bdi class="oa-env" title={e.environment || 'no environment label'}>{e.environment || 'no environment label'}</bdi>
        <span class="oa-badges">
          <IdentityBadge label={e.state} tone={STATE_TONE[e.state] ?? 'neutral'} />
          <IdentityBadge label={e.provenance} tone={PROV_TONE[e.provenance] ?? 'neutral'} />
          {#if e.partial}<span class="oa-partial-tag" title={PARTIAL}><i class="oa-hatch" aria-hidden="true"></i>partial</span>{/if}
        </span>
        <span class="oa-detail">
          <span class="oa-reason">{e.reason}{#if e.partial && !e.reason?.includes(PARTIAL)}; {PARTIAL}{/if}</span>
          <span class="oa-meta">{formatCount(e.reporting ?? 0)} of {countOf(e.targets ?? 0, 'target')} reporting{#if e.evidenceAt}, evidence {formatRelative(e.evidenceAt)}{/if}</span>
          {#if (e.unreachable ?? []).length}
            <span class="oa-links"><span class="oa-meta">Cannot reach it:</span>{#each e.unreachable as u (u.key)}<EntityLink ref={u} showStatus={false} showKind={false} />{/each}</span>
          {/if}
          {#if !compact && (e.providers ?? []).length}
            <span class="oa-links"><span class="oa-meta">From:</span>{#each e.providers as p (p.key)}<EntityLink ref={p} showStatus={false} showKind={false} />{/each}</span>
          {/if}
        </span>
      </li>
    {/each}
  </ul>
  {#if total > rows.length}<p class="oa-note">+{formatCount(total - rows.length)} more environments</p>{/if}
{/snippet}

{#if compact}
  {#if availability}
    <div class="oa oa-compact">
      <span class="oa-k">Availability</span>
      {#if rows.length}{@render list()}{:else}<span class="oa-note"><IdentityBadge label={availability.state} tone={STATE_TONE[availability.state] ?? 'neutral'} /> no environment reports it</span>{/if}
    </div>
  {/if}
{:else}
  <PreviewSection title="Availability" total={total} count={rows.length} help={HELP}
    empty="No target or consumer reports this offering in any environment, so its availability is unknown.">
    <div class="oa">{@render list()}</div>
  </PreviewSection>
  {#if hiddenProviders > 0}<p class="oa-note" data-testid="hidden-providers">{countOf(hiddenProviders, 'provider')} you may not see</p>{/if}
{/if}

<style>
  .oa { container-type: inline-size; display: flex; flex-direction: column; gap: var(--sp-1); min-inline-size: 0; }
  .oa-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  /* Stacked by default; three tracks once the container has room (SPEC T1, T2). */
  .oa-row { display: grid; grid-template-columns: minmax(0, 1fr); gap: var(--sp-1) var(--sp-3); align-items: start; min-inline-size: 0; padding: var(--sp-1) 0; }
  @container (min-width: 40rem) { .oa-row { grid-template-columns: minmax(12ch, 22%) minmax(18ch, auto) minmax(24ch, 1fr); } }
  .oa-env { font-weight: 600; overflow-wrap: anywhere; min-inline-size: 0; }
  .oa-badges, .oa-links { display: flex; flex-wrap: wrap; align-items: center; gap: var(--sp-1) var(--sp-2); min-inline-size: 0; }
  .oa-detail { display: flex; flex-direction: column; gap: var(--sp-1); min-inline-size: 0; }
  /* The grey hatch marks partial knowledge; it sits beside the word, never behind text. */
  .oa-partial-tag { display: inline-flex; align-items: center; gap: var(--sp-1); font-size: var(--text-xs); font-weight: 600; color: var(--c-text-2); }
  .oa-hatch { display: inline-block; inline-size: 0.9em; block-size: 0.9em; border-radius: 2px; background: var(--c-hatch), var(--c-surface); box-shadow: inset 0 0 0 1px var(--c-neutral); }
  .oa-reason { overflow-wrap: anywhere; }
  .oa-meta, .oa-note { color: var(--c-text-3); font-size: var(--text-sm); overflow-wrap: anywhere; }
  .oa-note { margin: 0; }
  .oa-k { color: var(--c-text-3); font: var(--role-label-font); font-variant-caps: var(--role-label-caps); letter-spacing: var(--role-label-tracking); }
</style>
