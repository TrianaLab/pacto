<script>
  import { timeLabel, andList, shortDigest } from '../lib/delivery.ts';
  import { countOf } from '../lib/format.ts';
  import { fleetEntityUrl, deliveryHistoryUrl } from '../lib/router.ts';

  // What each Stage ran at one past time (#459): read from its signed records,
  // never guessed. A Stage with no record before the time is unknown, and one whose
  // record leaves doubt (a later move unsigned or untimed, signatures not checked)
  // is partial with why. The caption is generated from the same answer as the rows.
  /** @type {{ answer: NonNullable<import('../lib/api.ts').DeliveryHistory['at']> }} */
  let { answer } = $props();

  const stages = $derived(answer.stages ?? []);
  const when = $derived(timeLabel(answer.at));
  const caption = $derived.by(() => {
    const known = stages.filter((s) => s.state === 'known').length;
    const parts = [`As of ${when}: from signed records, complete for ${known} of ${countOf(stages.length, 'Stage')}`];
    if (answer.unreadable?.length) parts.push(`${andList(answer.unreadable, 4)} could not be read`);
    if (answer.note?.includes('others were not read')) parts.push('other Stages were not read');
    return `${parts.join('; ')}.`;
  });
  const TONE = { known: 'badge-ok', partial: 'badge-warn', unknown: 'badge-neutral' };
</script>

<section class="ha" aria-labelledby="ha-h">
  <h2 id="ha-h" class="t-section-title">What did each Stage run at this time?</h2>
  <p class="t-meta ha-caption" data-testid="history-at-caption">{caption}</p>
  <!-- svelte-ignore a11y_no_noninteractive_tabindex (SPEC T2: the table region is labelled and takes focus) -->
  <div class="ha-frame" role="region" aria-label={`What each Stage ran at ${when}`} tabindex="0" data-testid="history-at">
    <div class="ha-table" role="table" aria-label={`What each Stage ran at ${when}`}>
      <div class="ha-row ha-head" role="row">
        {#each ['Stage', 'Service', 'Version', 'Owner', 'Business domain', 'Since', 'State'] as c (c)}<span role="columnheader">{c}</span>{/each}
      </div>
      {#each stages as s (`${s.namespace}/${s.stage}`)}
        <div class="ha-row" role="row" data-testid="history-at-row">
          <span class="ha-cell ha-title" role="cell" data-label="Stage">
            <a href={deliveryHistoryUrl({ namespace: s.namespace, stage: s.stage, at: answer.at })} title={`${s.namespace}/${s.stage}`}><bdi>{s.namespace}/{s.stage}</bdi></a>
          </span>
          <span class="ha-cell" role="cell" data-label="Service">{#if s.service}<bdi title={s.service}>{s.service}</bdi>{:else}<span class="t-meta">–</span>{/if}</span>
          <span class="ha-cell" role="cell" data-label="Version">
            {#if s.digest}
              {@const label = s.version || shortDigest(s.digest)}
              {#if s.revision}<a href={fleetEntityUrl('revision', s.revision)} title={label} aria-label={`${label}, revision on ${s.namespace}/${s.stage}`}><bdi>{label}</bdi></a>
              {:else}<bdi title={label}>{label}</bdi>{/if}
            {:else}<span class="t-meta">–</span>{/if}
          </span>
          <span class="ha-cell" role="cell" data-label="Owner">
            {#if s.owner && s.ownerKey}<a href={fleetEntityUrl('owner', s.ownerKey)} title={s.owner}><bdi>{s.owner}</bdi></a>
            {:else if s.owner}<bdi title={s.owner}>{s.owner}</bdi>{:else}<span class="t-meta">–</span>{/if}
          </span>
          <span class="ha-cell" role="cell" data-label="Business domain">{#if s.businessDomain}<bdi title={s.businessDomain}>{s.businessDomain}</bdi>{:else}<span class="t-meta">–</span>{/if}</span>
          <span class="ha-cell" role="cell" data-label="Since">{#if s.since}<time datetime={s.since} title={s.since}>{timeLabel(s.since)}</time>{:else}<span class="t-meta">–</span>{/if}</span>
          <span class="ha-cell" role="cell" data-label="State">
            <span class="badge {TONE[s.state] ?? 'badge-neutral'}" data-testid="history-at-state">{s.state}</span>
            {#if s.reason}<bdi class="t-meta" title={s.reason}>{s.reason}</bdi>{/if}
          </span>
        </div>
      {/each}
    </div>
  </div>
</section>

<style>
  .ha { display: flex; flex-direction: column; gap: var(--sp-2); min-inline-size: 0; }
  .ha h2, .ha-caption { margin: 0; }
  .ha-caption { overflow-wrap: anywhere; }
  /* One row per Stage: seven labelled cells side by side on a wide container, a
     stacked card below it (SPEC T2, 24). Long values wrap, never scroll. */
  .ha-frame { container: hatable / inline-size; min-inline-size: 0; }
  .ha-table { display: flex; flex-direction: column; }
  .ha-row { display: grid; grid-template-columns: minmax(0, 1fr); gap: var(--sp-1) var(--sp-3); padding: var(--sp-2) 0; border-block-end: 1px solid var(--c-border); min-inline-size: 0; overflow-wrap: anywhere; }
  .ha-head { position: absolute; inline-size: 1px; block-size: 1px; overflow: hidden; clip-path: inset(50%); }
  .ha-cell { display: flex; flex-wrap: wrap; align-items: baseline; gap: var(--sp-1) var(--sp-2); min-inline-size: 0; }
  .ha-cell bdi { min-inline-size: 0; overflow-wrap: anywhere; }
  .ha-cell[data-label]:not(.ha-title)::before { content: attr(data-label); flex-basis: 100%; color: var(--c-text-3); font: var(--role-label-font); font-variant-caps: var(--role-label-caps); }
  .ha-title { font-weight: 600; }
  @container hatable (min-width: 64rem) {
    .ha-row { grid-template-columns: minmax(16ch, 1.4fr) minmax(12ch, 1fr) minmax(10ch, 0.8fr) minmax(12ch, 1fr) minmax(12ch, 1fr) minmax(12ch, 0.9fr) minmax(14ch, 1.2fr); align-items: start; }
    .ha-head { position: static; inline-size: auto; block-size: auto; overflow: visible; clip-path: none; color: var(--c-text-3); font: var(--role-label-font); font-variant-caps: var(--role-label-caps); }
    .ha-cell[data-label]:not(.ha-title)::before { content: none; }
  }
  @media (pointer: coarse) { .ha a { display: inline-flex; align-items: center; min-block-size: 44px; } }
</style>
