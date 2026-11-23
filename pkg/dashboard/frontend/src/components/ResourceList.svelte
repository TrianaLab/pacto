<script>
  import { fleetEntityUrl } from '../lib/router.ts';
  import { formatCount } from '../lib/format.ts';
  import { formatDateTime } from '../lib/dateFormat.ts';
  import { phaseLabel } from '../lib/provisioning.ts';
  import { acceptsLabel } from '../lib/entityLabels.ts';
  import EntityLink from './EntityLink.svelte';
  import IdentityBadge from './IdentityBadge.svelte';
  import OwnershipFact from './OwnershipFact.svelte';
  import LifecycleBadge from './LifecycleBadge.svelte';
  import ProvisioningBadges from './ProvisioningBadges.svelte';
  import { STATE_TONE } from './OfferingAvailability.svelte';
  import Glyph from './figure/Glyph.svelte';
  import OfferingUseFigure from './figure/OfferingUseFigure.svelte';

  // The resources one service consumes (SPEC 20 to 24): one card per use of a provider,
  // with the provider and its owner, what the contract requests (declared), what
  // providers report per environment (observed) and what Pacto knows about usage.
  // Every fact is visible text; a title only repeats it. Cards are the narrow form too.
  /** @type {{ uses?: any[], service?: string, miraHref?: string }} */
  let { uses = [], service = '', miraHref = '' } = $props();
  const VIA = { dependency: 'dependency', configuration_reference: 'configuration', policy_reference: 'policy' };
  const RANGE_TONE = { satisfied: 'ok', unsatisfied: 'warn', unknown: 'neutral' };
  // The request the reader opened: which card and which of its statuses.
  let picked = $state(/** @type {{ row: number, i: number } | null} */ (null));
  const where = (st) => [st.environment, st.entry].filter(Boolean).join(' ') || 'no environment label';
  const detailOf = (st) => [
    st.phase === 'Unknown' && st.source ? (st.verified ? 'signed' : 'unsigned') : '',
    st.provider ? `adapter ${st.provider}` : '',
    st.externalRef ?? '',
    // The step a person may take, visible as well: a title alone never reaches a touch pointer.
    st.suggestion ?? '',
  ].filter(Boolean);
  const reasons = (u) => [...new Set((u.consumption ?? []).map((c) => c.reason).filter(Boolean))];
  // Usage: a number reads as current only in figures (a signed, fresh, uncontested report);
  // the raw figures of an untrusted report stay apart, labelled, in the neutral tone.
  const notReported = (u) => (u.consumption ?? []).every((c) => c.state === 'not-reported');
  const usageLabel = (c) => {
    if (c.state === 'reported') return 'Reported (observed)';
    if (c.state === 'not-reported') return 'Not reported';
    return `Unknown: ${c.reason || 'no trusted report'}${c.conflict?.length ? ` (${c.conflict.join(', ')})` : ''}`;
  };
  const figureRows = (f) => [
    ...(f.metrics ?? []).map((m) => [m.name, `${m.value} ${m.unit ?? ''}`.trim()]),
    ...(f.cost ? [['cost', `${f.cost.amount} ${f.cost.currency} per ${f.cost.period}`]] : []),
  ];
  const usageBy = (c) => [
    c.producer ? `reported by ${c.producer}` : '',
    c.source ? (c.verified ? 'signed' : 'unsigned') : '',
    c.asOf ? `as of ${formatDateTime(c.asOf)}` : '',
    c.revision?.label ? `about ${c.revision.label}` : '',
  ].filter(Boolean).join(', ');
</script>

<ul class="rl" data-testid="service-resources">
  {#each uses as u, ri (`${u.provider?.key}:${u.via}:${ri}`)}
    {@const statuses = u.provisioning ?? []}
    {@const pickedSt = picked?.row === ri ? statuses[picked.i] : null}
    <li class="inset-card rl-card" data-testid="service-resource">
      <div class="rl-head">
        <Glyph kind={u.offering ? 'offering' : 'service'} />
        <EntityLink ref={u.provider} showStatus={false} showKind={false} />
        {#if u.offering}<a class="rl-chip" href={fleetEntityUrl('offering', u.offering)}>{u.offering}</a>{:else}<span class="rl-note">no offering</span>{/if}
        <IdentityBadge label={VIA[u.via] ?? u.via} tone="neutral" />
        {#if u.rangeStatus}<IdentityBadge label={u.rangeStatus} tone={RANGE_TONE[u.rangeStatus] ?? 'neutral'} title={acceptsLabel(u.compatibility)} describe />{/if}
        <LifecycleBadge lifecycle={u.providerLifecycle} detail={false} />
      </div>
      <div class="rl-row">
        <span class="rl-k">Provider owner</span>
        {#if u.providerOwner}<OwnershipFact ownership={u.providerOwner} />{:else}<span class="rl-note">Owner unknown</span>{/if}
        {#if u.providerAvailability}<IdentityBadge label={`provider ${u.providerAvailability.state}`} tone={STATE_TONE[u.providerAvailability.state] ?? 'neutral'} title="Whether the provider answers where it runs. Separate from this resource's phase." describe />{/if}
      </div>
      {#each u.requested ?? [] as r, qi (`${r.entry}:${qi}`)}
        <div class="rl-block" data-testid="resource-requested">
          <div class="rl-row">
            <span class="rl-k">Requested (declared)</span>
            {#if r.revision}<EntityLink ref={r.revision} showStatus={false} showKind={false} />{/if}
            <span class="rl-note"><bdi>{r.entry}</bdi>{r.configuration ? `, configuration ${r.configuration}` : ''}</span>
            {#if r.consumerRevision}<span class="rl-note">declared by</span><EntityLink ref={r.consumerRevision} showStatus={false} showKind={false} />{/if}
          </div>
          {#if (r.values?.items ?? []).length}
            <dl class="rl-values">
              {#each r.values.items as v, vi (`${v.key}:${vi}`)}
                <dt><bdi title={v.key}>{v.key}</bdi></dt><dd><bdi title={v.value}>{v.value}</bdi></dd>
              {/each}
            </dl>
            {#if r.values.truncated}<p class="rl-note">Showing {formatCount(r.values.count)}{typeof r.values.total === 'number' ? ` of ${formatCount(r.values.total)}` : ''} values.</p>{/if}
          {:else}
            <p class="rl-note">It requests no values.</p>
          {/if}
        </div>
      {/each}
      {#if u.requestedTruncated}<p class="rl-note">More configurations are requested than the preview shows.</p>{/if}
      {#if statuses.length}
        <div class="rl-block">
          <span class="rl-k">Provisioning (observed)</span>
          <ul class="rl-status">
            {#each statuses as st, i (`${st.environment ?? ''}:${st.entry ?? ''}:${i}`)}
              <li data-testid="resource-status">
                <bdi class="rl-env" title={where(st)}>{where(st)}</bdi>
                <span class="rl-phase">
                  <ProvisioningBadges statuses={[st]} selected={picked?.row === ri && picked.i === i ? 0 : -1}
                    onselect={(j) => { picked = j < 0 ? null : { row: ri, i }; }} />
                  <span class="rl-text">{phaseLabel(st)}</span>
                  {#each detailOf(st) as t (t)}<bdi class="rl-note" title={t}>{t}</bdi>{/each}
                </span>
              </li>
            {/each}
          </ul>
        </div>
      {/if}
      {#if pickedSt}
        <OfferingUseFigure consumer={u.consumer?.label ?? service} request={pickedSt} useKey={`${u.consumer?.key}:${u.provider?.key}:${u.via}:${picked?.i}`} />
      {/if}
      {#if (u.consumption ?? []).length}
        <div class="rl-block" data-testid="resource-usage">
          <div class="rl-row">
            <span class="rl-k">Usage</span>
            {#if notReported(u)}<span class="rl-text">Not reported{#each reasons(u) as reason (reason)}: {reason}{/each}</span>{/if}
            {#if miraHref}<a href={miraHref} target="_blank" rel="noopener noreferrer" data-testid="resource-mira-link">Open {service} in Mira</a>{/if}
          </div>
          {#if !notReported(u)}
            <ul class="rl-status rl-usage">
              {#each u.consumption as c, ci (`${c.environment ?? ''}:${c.entry ?? ''}:${ci}`)}
                {@const f = c.figures ?? c.reported}
                <li data-testid="resource-usage-entry" data-state={c.state}>
                  <bdi class="rl-env" title={where(c)}>{where(c)}</bdi>
                  <div class="rl-block">
                    <span class="rl-text">{usageLabel(c)}</span>
                    {#if f}
                      {#if !c.figures}<span class="rl-note">reported, not trusted</span>{/if}
                      <dl class="rl-figures" class:rl-untrusted={!c.figures}>
                        {#each figureRows(f) as [k, v], fi (`${k}:${fi}`)}
                          <dt><bdi title={k}>{k}</bdi></dt><dd><bdi title={v}>{v}</bdi></dd>
                        {/each}
                      </dl>
                    {/if}
                    {#if usageBy(c)}<span class="rl-note">{usageBy(c)}</span>{/if}
                  </div>
                </li>
              {/each}
            </ul>
          {/if}
        </div>
      {/if}
    </li>
  {/each}
</ul>

<style>
  .rl { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-3); min-inline-size: 0; }
  .rl-card { container-type: inline-size; display: flex; flex-direction: column; gap: var(--sp-2); min-inline-size: 0; }
  .rl-head, .rl-row, .rl-phase { display: flex; align-items: center; gap: var(--sp-1) var(--sp-2); flex-wrap: wrap; min-inline-size: 0; }
  .rl-block { display: flex; flex-direction: column; gap: var(--sp-1); min-inline-size: 0; }
  .rl-k { color: var(--c-text-3); font: var(--role-label-font); font-variant-caps: var(--role-label-caps); letter-spacing: var(--role-label-tracking); }
  .rl-note { margin: 0; color: var(--c-text-3); font-size: var(--text-sm); overflow-wrap: anywhere; min-inline-size: 0; }
  .rl-text { overflow-wrap: anywhere; min-inline-size: 0; }
  .rl-chip { display: inline-block; font-size: var(--text-xs); padding: 2px 8px; border-radius: var(--radius-xs); color: var(--c-text); background: var(--c-surface-inset); border: 1px solid var(--c-border); text-decoration: none; overflow-wrap: anywhere; }
  .rl-chip:hover { color: var(--c-accent); border-color: var(--c-accent); }
  /* Key and value tracks with floors; a long key or value breaks inside its track. */
  .rl-values { display: grid; grid-template-columns: minmax(12ch, max-content) minmax(8ch, 1fr); gap: 2px var(--sp-3); margin: 0; font-family: var(--font-mono); font-size: var(--text-sm); min-inline-size: 0; }
  .rl-values dt, .rl-values dd { margin: 0; overflow-wrap: anywhere; min-inline-size: 0; }
  .rl-values dt { color: var(--c-text-2); }
  /* Usage figures: metric and value tracks with floors; untrusted figures read quieter, never ok. */
  .rl-figures { display: grid; grid-template-columns: minmax(14ch, max-content) minmax(10ch, 1fr); gap: 2px var(--sp-3); margin: 0; font-family: var(--font-mono); font-size: var(--text-sm); min-inline-size: 0; }
  .rl-figures dt, .rl-figures dd { margin: 0; overflow-wrap: anywhere; min-inline-size: 0; }
  .rl-figures dt { color: var(--c-text-2); }
  .rl-untrusted, .rl-untrusted dt { color: var(--c-text-3); }
  .rl-status { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-1); min-inline-size: 0; }
  /* Stacked by default; environment and status side by side once the card has room. */
  .rl-status li { display: grid; grid-template-columns: minmax(0, 1fr); gap: 2px var(--sp-3); align-items: center; min-inline-size: 0; }
  @container (min-width: 40ch) { .rl-status li { grid-template-columns: minmax(10ch, max-content) minmax(14ch, 1fr); } }
  /* A usage entry stacks: its figures need both grid tracks, wider than a status column. */
  .rl-status.rl-usage li { display: flex; flex-direction: column; align-items: stretch; }
  .rl-env { font-weight: 600; overflow-wrap: anywhere; min-inline-size: 0; }
  @media (pointer: coarse) { .rl-chip, .rl-status :global(.pb-btn) { min-block-size: 44px; display: inline-flex; align-items: center; } }
</style>
