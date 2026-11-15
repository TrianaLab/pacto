<script>
  import { tick } from 'svelte';
  import Pager from './Pager.svelte';
  import { api } from '../lib/api.ts';
  import { timeLabel, andList } from '../lib/delivery.ts';
  import { clientPage, urlInt, limitParam } from '../lib/paging.ts';
  import { formatCount, countOf } from '../lib/format.ts';
  import { scrollToSection } from '../lib/motion.ts';
  import { serviceChangesUrl, fleetChangesUrl, deliveryHistoryUrl, hashForHref } from '../lib/router.ts';

  // A service's changes on its page (#459): every declared change across the revisions
  // Pacto holds, in version order, each tied to the revision that shows it and the one
  // it was compared with. A change has a time only from a delivery record of when its
  // revision reached a Stage, read as you in a second request; the fleet answer shows
  // at once and every line without a record says so ("time not recorded", "time
  // hidden"), never a guessed time.
  /** @type {{ changes?: import('../lib/api.ts').ChangeHistory | null, service?: string, label?: string, deliveryState?: string,
   *   kind?: string, offset?: string | number, limit?: string | number, section?: string, refreshTick?: number }} */
  let { changes = null, service = '', label = '', deliveryState = 'unknown', kind = '', offset = 0, limit = '', section = '', refreshTick = 0 } = $props();

  const PAGE = 25;
  const KINDS = ['owner', 'domain', 'offering', 'dependency', 'lifecycle', 'environment', 'finding'];

  // The reached answer, when asked: the same events with their times.
  let reachedAnswer = $state(null);
  let reachedError = $state('');
  let reading = $state(false);
  const asks = $derived(deliveryState === 'enabled' && !!service);
  $effect(() => {
    void refreshTick;
    reachedAnswer = null;
    reachedError = '';
    if (!asks) { reading = false; return; }
    let live = true;
    reading = true;
    api.fleetEntityDetail('service', service, { reached: true }).then((d) => {
      if (live) reachedAnswer = d.service?.changes ?? null;
    }, (err) => {
      if (live) reachedError = err instanceof Error ? err.message : String(err);
    }).finally(() => { if (live) reading = false; });
    return () => { live = false; };
  });

  const h = $derived(reachedAnswer ?? changes);
  const items = $derived(h?.items ?? []);
  const times = $derived(reachedAnswer?.times ?? changes?.times ?? 'not-read');
  const counts = $derived(Object.fromEntries(KINDS.map((k) => [k, items.filter((e) => e.kind === k).length])));
  const filtered = $derived(kind ? items.filter((e) => e.kind === kind) : items);
  const pageSize = $derived(urlInt(limit) || PAGE);
  const page = $derived(clientPage(filtered, urlInt(offset), pageSize));
  const name = $derived(label || service);

  // The caption is generated from the same answer as the rows.
  const caption = $derived.by(() => {
    const parts = [`${countOf(h?.total ?? 0, 'change')} across the versions Pacto holds, in version order`];
    if (h?.completeness === 'partial') parts.push(`partial: ${h.completenessReason || 'Pacto holds only some revisions of this service'}`);
    parts.push(timesSentence());
    return parts.join('; ');
  });
  function timesSentence() {
    if (reading) return 'reading times from delivery records';
    if (times === 'read') return 'times from delivery records';
    if (times === 'partial') return h?.timesUnreadable?.length ? `times from the Stages read; ${andList(h.timesUnreadable, 4)} could not be read` : `times from the Stages read${h?.timesReason ? `: ${h.timesReason}` : ''}`;
    if (times === 'hidden') return h?.timesReason || 'you may not read delivery records, so times are hidden';
    if (reachedError) return `times not read: ${reachedError}`;
    return h?.timesReason || 'times not recorded: no delivery record was read';
  }

  /** @param {import('../lib/api.ts').ChangeEvent} e */
  function changeText(e) {
    const what = e.subject || (e.kind === 'finding' ? 'a finding' : '');
    if (e.change === 'present') return `${what ? `${what} ` : ''}declared since at least this version`;
    if (e.kind === 'finding') return `${what} ${{ added: 'appeared', removed: 'resolved', changed: 'crossed its minimum' }[e.change] ?? e.change}`;
    if (e.change === 'changed' && e.fields?.length) return `${what ? `${what} ` : ''}${andList(e.fields, 4)} changed`;
    return `${what ? `${what} ` : ''}${e.change}`;
  }
  const version = (r) => r?.version || r?.label || '';
  // A line without a time: hidden when the caller may not list Stages, else not recorded.
  const noTime = $derived(reading ? 'reading times from delivery records' : times === 'hidden' ? 'time hidden' : times === 'partial' ? 'time not recorded in the Stages read' : 'time not recorded');

  // The route that asked for this section scrolls to it once its rows render.
  const wantsSection = $derived(section === 'changes' || !!kind || !!urlInt(offset) || !!urlInt(limit));
  $effect(() => {
    if (!wantsSection || !items.length) return;
    void kind; void offset; void limit;
    tick().then(() => scrollToSection('sec-changes'));
  });

  const chipHref = (k) => serviceChangesUrl(service, { changesKind: k, section: 'changes' });
  const pageHref = (o, l) => serviceChangesUrl(service, { changesKind: kind, changesOffset: o, changesLimit: limitParam(l, limit, PAGE), section: 'changes' });
</script>

<section id="sec-changes" class="sc" data-toc="Changes" data-testid="service-changes" aria-labelledby="sc-h">
  <h2 id="sc-h" class="t-section-title">What changed in this service?</h2>
  <p class="t-meta sc-caption" data-testid="service-changes-caption">{caption}</p>

  {#if items.length}
    <nav class="sc-kinds" aria-label="Filter changes by kind">
      <a class="chip" class:active={!kind} aria-current={!kind ? 'true' : undefined} href={chipHref('')}>All {formatCount(items.length)}</a>
      {#each KINDS.filter((k) => counts[k]) as k (k)}
        <a class="chip" class:active={kind === k} aria-current={kind === k ? 'true' : undefined} href={chipHref(k)} data-testid="service-changes-kind" data-kind={k}>{k} {formatCount(counts[k])}</a>
      {/each}
    </nav>
    {#if kind}<p class="t-meta" data-testid="service-changes-filtered">{countOf(filtered.length, 'change')} of kind {kind}</p>{/if}

    <!-- svelte-ignore a11y_no_noninteractive_tabindex (SPEC T2: the table region is labelled and takes focus) -->
    <div class="sc-frame" role="region" aria-label={`Changes of ${name}`} tabindex="0">
      <div class="sc-table" role="table" aria-label={`Changes of ${name}`}>
        <div class="sc-row sc-head" role="row">
          <span role="columnheader">Version</span>
          <span role="columnheader">Change</span>
          <span role="columnheader">From → To</span>
          <span role="columnheader">Reached</span>
          <span role="columnheader">Source</span>
        </div>
        {#each page.items as e, i (`${page.page.offset + i}`)}
          <div class="sc-row" role="row" data-testid="service-change-row" data-kind={e.kind}>
            <span class="sc-cell sc-title" role="cell" data-label="Version">
              <a href={hashForHref(e.revision.href)} title={e.revision.label}><bdi>{version(e.revision)}</bdi></a>
              {#if e.previous}
                <span class="t-meta">compared with <a href={hashForHref(e.previous.href)} title={e.previous.label}><bdi>{version(e.previous)}</bdi></a></span>
                <a class="sc-analyse" href={fleetChangesUrl(service, { old: e.previous.key, new: e.revision.key })}
                  aria-label={`Analyse ${version(e.previous)} to ${version(e.revision)} against today's graph`}>Analyse against today's graph</a>
              {/if}
            </span>
            <span class="sc-cell" role="cell" data-label="Change">
              <span class="badge badge-neutral">{e.kind}</span> <bdi title={changeText(e)}>{changeText(e)}</bdi>
            </span>
            <span class="sc-cell" role="cell" data-label="From → To">
              {#if e.from || e.to}
                <bdi title={e.from || 'none'}>{e.from || 'none'}</bdi> → <bdi title={e.to || 'none'}>{e.to || 'none'}</bdi>
              {:else}<span class="t-meta">–</span>{/if}
            </span>
            <span class="sc-cell" role="cell" data-label="Reached" data-testid="service-change-reached">
              {#if e.reached?.length}
                {#each e.reached as r (`${r.namespace}/${r.stage}`)}
                  <span class="sc-reach">
                    {#if r.record && r.verified}
                      <a href={deliveryHistoryUrl({ namespace: r.namespace, stage: r.stage })} title={`Stage ${r.namespace}/${r.stage}, record ${r.record}`}><bdi>{r.environment || r.stage}</bdi> {timeLabel(r.at)}</a>
                    {:else}
                      <bdi title={`Stage ${r.namespace}/${r.stage}`}>{r.environment || r.stage}</bdi> {timeLabel(r.at)}
                      <span class="t-meta">{r.fromStatus ? 'from status, not signed' : 'recorded, signature not checked'}</span>
                    {/if}
                  </span>
                {/each}
              {:else}<span class="t-meta">{noTime}</span>{/if}
            </span>
            <span class="sc-cell" role="cell" data-label="Source">
              <span class="badge" class:badge-ok={e.source !== 'validation'} class:badge-info={e.source === 'validation'}>{e.source === 'validation' ? 'validation' : 'declared'}</span>
            </span>
          </div>
        {/each}
      </div>
    </div>
    <Pager page={page.page} href={pageHref} label="Pages of changes" noun="changes" testid="changes-pager" count={page.items.length} />
    {#if h?.truncated}<p class="t-meta">And {formatCount(h.total - h.count)} more changes.</p>{/if}
  {:else}
    <p class="t-meta" data-testid="service-changes-empty">No declared change between the versions Pacto holds.</p>
  {/if}
  {#if (h?.gaps ?? 0) > 0}<p class="t-meta sc-warn">{countOf(h.gaps, 'revision')} could not be read; changes across them are unknown</p>{/if}
</section>

<style>
  .sc { display: flex; flex-direction: column; gap: var(--sp-2); min-inline-size: 0; }
  .sc h2 { margin: 0; }
  .sc-caption, .sc-warn { margin: 0; overflow-wrap: anywhere; }
  .sc-warn { color: var(--c-warn); }
  .sc-kinds { display: flex; flex-wrap: wrap; gap: var(--sp-1) var(--sp-2); }
  .sc-kinds .chip { text-decoration: none; }
  /* One row per change: five labelled cells side by side on a wide container, a
     stacked card below it (SPEC T2, 24). Long values wrap, never scroll. */
  .sc-frame { container: sctable / inline-size; min-inline-size: 0; }
  .sc-table { display: flex; flex-direction: column; }
  .sc-row { display: grid; grid-template-columns: minmax(0, 1fr); gap: var(--sp-1) var(--sp-3); padding: var(--sp-2) 0; border-block-end: 1px solid var(--c-border); min-inline-size: 0; overflow-wrap: anywhere; }
  .sc-head { position: absolute; inline-size: 1px; block-size: 1px; overflow: hidden; clip-path: inset(50%); }
  .sc-cell { display: flex; flex-wrap: wrap; align-items: baseline; gap: var(--sp-1) var(--sp-2); min-inline-size: 0; }
  .sc-cell bdi { min-inline-size: 0; overflow-wrap: anywhere; }
  .sc-cell[data-label]:not(.sc-title)::before { content: attr(data-label); flex-basis: 100%; color: var(--c-text-3); font: var(--role-label-font); font-variant-caps: var(--role-label-caps); }
  .sc-title { font-weight: 600; }
  .sc-title .t-meta, .sc-analyse { font-weight: 400; }
  .sc-reach { display: flex; flex-wrap: wrap; gap: 0 var(--sp-1); flex-basis: 100%; }
  @container sctable (min-width: 56rem) {
    .sc-row { grid-template-columns: minmax(16ch, 1.2fr) minmax(20ch, 1.6fr) minmax(16ch, 1.4fr) minmax(18ch, 1.4fr) minmax(10ch, 0.6fr); align-items: start; }
    .sc-head { position: static; inline-size: auto; block-size: auto; overflow: visible; clip-path: none; color: var(--c-text-3); font: var(--role-label-font); font-variant-caps: var(--role-label-caps); }
    .sc-cell[data-label]:not(.sc-title)::before { content: none; }
  }
  @media (pointer: coarse) { .sc a { display: inline-flex; align-items: center; min-block-size: 44px; } }
</style>
