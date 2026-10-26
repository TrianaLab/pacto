<script>
  import Person from '../components/Person.svelte';
  import { onDestroy } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { deliveryChangeUrl, deliveryHistoryUrl } from '../lib/router.ts';
  import { typeLabel, shortDigest, versionLabel, timeLabel, decisionLabel, actorBadge, autonomyOf, sentence,
    verificationLabel, outcomeTone, reasonAfter, UNKNOWN_DOCS } from '../lib/delivery.ts';
  import { urlInt, limitParam } from '../lib/paging.ts';
  import { formatCount } from '../lib/format.ts';
  import { deliverySession } from '../lib/deliverySession.svelte.ts';
  import BlamePanel from '../components/BlamePanel.svelte';
  import UndoPanel from '../components/UndoPanel.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import DeliveryNav from '../components/DeliveryNav.svelte';
  import DeliveryProblem from '../components/DeliveryProblem.svelte';
  import ListingNote from '../components/ListingNote.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import DigestText from '../components/DigestText.svelte';
  import Pager from '../components/Pager.svelte';
  import ListFilter from '../components/ListFilter.svelte';
  import CappedList from '../components/CappedList.svelte';

  // What happened on each environment: from its signed records where the
  // recorder runs, otherwise from the changes still in the cluster. Every
  // history says where it came from and whether it is the whole story. The
  // server pages it: one environment's changes newest first, or every
  // environment as one line: its newest change, how it ended, how many
  // changes it has and a link to them.
  let { namespace = '', stage = '', offset = '', limit = '', q = '', refreshTick = 0, miraURL = '' } = $props();

  const one = $derived(!!(namespace && stage));
  const loader = createProductLoader(() => api.deliveryHistory(namespace, stage, {
    offset: urlInt(offset) || undefined, limit: urlInt(limit) || undefined, q: q || undefined,
  }));
  const question = $derived(`${namespace}/${stage}@@${urlInt(offset)}@@${urlInt(limit)}@@${q}`);
  $effect(() => { loader.sync(`${question}@@${refreshTick}`, question); });
  onDestroy(() => loader.destroy());

  const data = $derived(loader.dataTag === question ? loader.data : null);
  const histories = $derived(data?.histories ?? []);
  // One environment pages its changes; every environment pages the environments.
  const page = $derived(one ? histories[0]?.entriesPage : data?.page);
  const href = (off, lim) => deliveryHistoryUrl({ namespace, stage, q, offset: off, limit: limitParam(lim, limit, page?.limit ?? lim) });
  const me = $derived({ user: deliverySession.data?.caller?.user, name: deliverySession.data?.name });
  const version = (d) => versionLabel(d, data?.versions);
  // The full ids a history names, so a short one in a problem line can show and copy its full id.
  const knownIds = (h) => [h.stage.pointer, h.head, ...(h.entries ?? []).flatMap((en) =>
    [en.record, en.unverifiedRecord, en.from, en.to, en.pointer?.before, en.pointer?.after])];

  // One panel open at a time: blame of an environment, or undo of a change.
  // Closing it gives focus back to the button that opened it.
  let open = $state(null);
  let opener = null;
  function show(e, panel) { opener = e.currentTarget; open = panel; }
  function close() { open = null; opener?.focus(); }
  const isOpen = (kind, ns, key) => open?.kind === kind && open.ns === ns && open.key === key;

  /** Where a history came from, in words. */
  function sourceLabel(h) {
    const from = h.source !== 'records' ? 'From status' : h.verified ? 'From signed records, every signature verified' : 'From records, signatures not verified';
    return `${from}, ${h.completeness === 'complete' ? 'complete' : 'partial'}`;
  }

  /** The same, short enough for one cell of the summary. */
  function sourceShort(h) {
    const from = h.source !== 'records' ? 'Status' : h.verified ? 'Signed records' : 'Records, not verified';
    return h.completeness === 'complete' ? from : `${from}, partial`;
  }

  /** What a line is: a change, or a record that stands for one. */
  function changeLabel(en) {
    if (en.kind === 'gap') return 'Gap';
    if (en.kind === 'unrecordable') return 'Unrecordable';
    return typeLabel(en.type);
  }

  // How far delivery ran on its own, over every line shown here; null (and
  // hidden) when a line does not say who approved it.
  const autonomy = $derived(autonomyOf(histories.flatMap((h) => h.entries ?? [])));
  const partial = $derived(histories.some((h) => h.completeness !== 'complete'));
  const more = $derived(histories.some((h) => (h.entriesPage?.total ?? 0) > (h.entries ?? []).length));

  /** The people who decided a change: an approval or a rejection, never only who asked. */
  const deciders = (en) => (en.approvals ?? []).filter((a) => a.by);
</script>

<div class="product-page">
  <PageHeader
    title={stage ? `History of ${namespace}/${stage}` : 'History'}
    subtitle="What happened on each environment, newest first. Where the recorder runs it is read from signed records; elsewhere from the changes still in the cluster, which is never the whole story."
    asOf={data?.asOf}
    revalidating={!!data && loader.loading}
  />
  <DeliveryNav current="delivery-history" />
  {#if stage}<p class="t-body-2"><a class="hv-link" href={deliveryHistoryUrl()}>Show every environment</a></p>{/if}
  <ListFilter value={q} href={(v) => deliveryHistoryUrl({ namespace, stage, q: v })} testid="history-filter"
    label={one ? 'Filter changes by person, change, outcome or reason' : 'Filter environments by name or service'}
    placeholder={one ? 'Person, change, outcome or reason' : 'Environment or service'} />

  {#if !data && loader.loading}
    <EmptyState loading={true} rows={4} />
  {:else if !data && loader.error}
    <DeliveryProblem error={loader.error} onRetry={() => loader.refresh()} />
  {:else if data}
    <ListingNote listing={data} error={loader.error} onRetry={() => loader.refresh()} />
    {#if one && autonomy?.total}
      <p class="t-body-2 hv-autonomy" data-testid="history-autonomy">
        <strong>{autonomy.noPerson} of {autonomy.total} promotion{autonomy.total === 1 ? '' : 's'} needed no person</strong>{#if autonomy.onlyUnknown}; {autonomy.onlyUnknown} waited for one only because a check was Unknown, which more visibility removes (<a class="hv-link" href={UNKNOWN_DOCS} target="_blank" rel="noopener noreferrer">how</a>){/if}.{#if partial || more}{' '}Counted from the changes shown here{#if partial}, and some histories are partial{/if}.{/if}
      </p>
    {/if}
    {#if histories.length === 0}
      <div class="state-box"><h2>{q ? 'Nothing matches the filter' : 'No history yet'}</h2><p>{q ? `No ${one ? 'change' : 'environment'} you can see matches “${q}”.` : 'No environment you can see has a change on record.'}</p></div>
    {:else if !one}
      <div class="data-scroll hv-summary-scroll">
        <table class="data-table hv-table" data-testid="history-summary">
          <caption class="visually-hidden">Every environment's newest change, newest first in each</caption>
          <colgroup><col style="width: 24%" /><col style="width: 12%" /><col style="width: 22%" /><col style="width: 13%" /><col style="width: 13%" /><col style="width: 16%" /></colgroup>
          <thead><tr><th scope="col">Stage</th><th scope="col">Runs</th><th scope="col">Newest change</th><th scope="col">How it ended</th><th scope="col">When</th><th scope="col">Changes</th></tr></thead>
          <tbody>
            {#each histories as h (h.namespace + '/' + h.stage.name)}
              {@const en = h.entries?.[0]}
              {@const url = deliveryHistoryUrl({ namespace: h.namespace, stage: h.stage.name })}
              {@const issues = (h.problems?.length ?? 0) + (h.unreadable?.length ?? 0)}
              {@const total = h.entriesPage?.total ?? h.entries?.length ?? 0}
              <tr data-testid="history-stage-row" data-stage={`${h.namespace}/${h.stage.name}`}>
                <th scope="row">
                  <div class="cell-stack">
                    <a class="cell-clip" href={url} title={`${h.namespace}/${h.stage.name}`}>{h.stage.name}</a>
                    <span class="t-meta cell-clip" title={h.namespace}>in {h.namespace}</span>
                    <span class="t-meta hv-source" class:hv-partial={h.completeness !== 'complete'} data-testid="history-source" title={sourceLabel(h)}>{sourceShort(h)}</span>
                  </div>
                </th>
                <td data-label="Runs"><span class="cell-clip" title={h.stage.pointer || 'nothing yet'}>{h.stage.pointer ? version(h.stage.pointer) : 'Nothing yet'}</span>{#if h.stage.frozen}<span class="badge badge-warn">Frozen</span>{/if}</td>
                <td data-label="Newest change">
                  {#if en}
                    <div class="cell-stack">
                      <span>{#if en.operation}<a href={deliveryChangeUrl(h.namespace, en.operation)}>{changeLabel(en)}</a>{:else}{changeLabel(en)}{/if}</span>
                      {#if en.from || en.to}<span class="t-meta cell-clip" title={[en.from, en.to].filter(Boolean).join(' → ')}>{version(en.from) || '–'} → {version(en.to) || '–'}</span>{/if}
                    </div>
                  {:else}<span class="hv-none">None kept</span>{/if}
                </td>
                <td data-label="How it ended">{#if en}<span class="badge {outcomeTone(en.outcome)}">{en.outcome || en.phase || 'Not known'}</span>{/if}</td>
                <td data-label="When">{#if en?.finished}<time datetime={en.finished} title={en.finished}>{timeLabel(en.finished)}</time>{/if}</td>
                <td data-label="Changes">
                  <div class="cell-stack">
                    <a href={url} data-testid="history-all-changes" aria-label={`All ${formatCount(total)} changes on ${h.namespace}/${h.stage.name}`}>{formatCount(total)} change{total === 1 ? '' : 's'}</a>
                    {#if issues}<a class="badge badge-warn" href={url} data-testid="history-issues">{formatCount(issues)} problem{issues === 1 ? '' : 's'}</a>{/if}
                  </div>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {:else}
      {#each histories as h (h.namespace + '/' + h.stage.name)}
        {@const known = knownIds(h)}
        <section class="hv-stage" aria-label={`History of ${h.namespace}/${h.stage.name}`}>
          <h2 class="t-section-title">{h.namespace}/{h.stage.name}</h2>
          <p class="t-body-2">
            <span class="hv-source" class:hv-partial={h.completeness !== 'complete'} data-testid="history-source">{sourceLabel(h)}.</span>
            Runs {#if h.stage.pointer}<strong>{version(h.stage.pointer)}</strong>{#if data.versions?.[h.stage.pointer]}{' '}<code class="hv-digest" title={h.stage.pointer}>{shortDigest(h.stage.pointer)}</code>{/if}{:else}nothing yet{/if}{#if h.stage.frozen}, frozen{/if}. {sentence(h.note)}
          </p>
          {#if h.stage.pointer}
            <div><button type="button" class="btn btn-sm" onclick={(e) => show(e, { kind: 'blame', ns: h.namespace, key: h.stage.name })}>Who put {version(h.stage.pointer)} here?</button></div>
          {/if}
          {#if isOpen('blame', h.namespace, h.stage.name)}
            <BlamePanel namespace={h.namespace} stage={h.stage.name} {miraURL} onClose={close} />
          {/if}
          {#if (h.problems ?? []).length}
            <div class="hv-notes" data-testid="history-problems">
              <h3 class="t-body-2">What the records show is wrong ({formatCount(h.problems.length)})</h3>
              <CappedList items={h.problems} max={5}>{#snippet item(p)}<DigestText text={p} {known} />{/snippet}</CappedList>
            </div>
          {/if}
          {#if (h.unreadable ?? []).length}
            <div class="hv-notes" data-testid="history-unreadable">
              <h3 class="t-body-2">What could not be read or verified ({formatCount(h.unreadable.length)})</h3>
              <CappedList items={h.unreadable} max={5}>{#snippet item(u)}<DigestText text={u} {known} />{/snippet}</CappedList>
            </div>
          {/if}
          {#if (h.entries ?? []).length === 0}
            <p class="t-body-2 hv-none">{q ? 'No change matches the filter.' : 'No change kept for this environment.'}</p>
          {:else}
            <div class="data-scroll">
              <table class="data-table hv-table">
                <caption class="visually-hidden">Changes on {h.namespace}/{h.stage.name}, newest first</caption>
                <colgroup><col style="width: 15%" /><col style="width: 14%" /><col style="width: 16%" /><col style="width: 17%" /><col style="width: 18%" /><col style="width: 10%" /><col style="width: 10%" /></colgroup>
                <thead><tr><th scope="col">Change</th><th scope="col">Version</th><th scope="col">Asked by</th><th scope="col">Decided by</th><th scope="col">How it ended</th><th scope="col">When</th><th scope="col">Record</th></tr></thead>
                <tbody>
                  {#each h.entries as en, i (`${en.seq ?? ''}/${en.record ?? ''}/${en.operation}/${i}`)}
                    {@const reason = reasonAfter(en.reason, en.outcome, en.phase, en.verification)}
                    <tr>
                      <td data-label="Change">
                        <div class="cell-stack">
                          <span>{#if en.seq}<span class="hv-seq">#{en.seq}</span>{' '}{/if}{#if en.operation}<a href={deliveryChangeUrl(h.namespace, en.operation)}>{changeLabel(en)}</a>{:else}{changeLabel(en)}{/if}</span>
                          {#if en.operation && en.pointer?.after}<button type="button" class="btn btn-sm" aria-label={`Undo ${en.operation}`} aria-expanded={isOpen('undo', h.namespace, en.operation)} onclick={(e) => show(e, { kind: 'undo', ns: h.namespace, key: en.operation })}>Undo</button>{/if}
                        </div>
                      </td>
                      <td data-label="Version"><span class="cell-clip" title={[en.from, en.to].filter(Boolean).join(' → ')}>{version(en.from) || '–'} → {version(en.to) || '–'}</span></td>
                      <td data-label="Asked by"><div class="cell-stack">{#if en.actor}<Person user={en.actor} people={data.people} {me} />{:else}–{/if}{#if actorBadge(en.actorClass)}{@const ab = actorBadge(en.actorClass)}{' '}<span class="badge {ab.tone}" data-testid="actor-badge">{ab.label}</span>{/if}</div></td>
                      <td data-label="Decided by" data-testid="history-decided">{#if en.kind === 'gap' || en.kind === 'unrecordable'}–{:else if deciders(en).length}<ul class="hv-deciders">{#each deciders(en) as a, j (j)}<li><Person user={a.by} people={data.people} {me} /> <span class="badge badge-neutral">{decisionLabel(a.decision)}</span></li>{/each}</ul>{:else if en.phase === 'Held'}<span class="hv-class">Waiting for a person</span>{:else if en.actorClass === 'human'}<span class="hv-class">The person who asked</span>{:else if en.approvals === undefined}<span class="hv-class">Not recorded</span>{:else}<span class="hv-class">No person</span>{/if}</td>
                      <td data-label="How it ended" data-testid="history-end">
                        <div class="cell-stack">
                          <span class="badge {outcomeTone(en.outcome)}">{en.outcome || en.phase || 'Not known'}</span>
                          {#if en.verification}{' '}<span>{verificationLabel(en.verification)}</span>{/if}
                          {#if reason}{' '}<span class="hv-reason"><DigestText text={reason} {known} /></span>{/if}
                        </div>
                      </td>
                      <td data-label="When">{#if en.finished}<time datetime={en.finished} title={en.finished}>{timeLabel(en.finished)}</time>{/if}</td>
                      <td data-label="Record">{#if en.record}<span class="cell-clip" title={en.record}><DigestText text={en.record} known={[en.record]} /></span>{:else if en.unverifiedRecord}<span title={en.unverifiedRecord}>Recorded, not verified</span>{:else if h.source === 'records'}Not recorded yet{:else}–{/if}</td>
                    </tr>
                    {#if en.operation && isOpen('undo', h.namespace, en.operation)}
                      <tr class="hv-undo-row"><td colspan="7">
                        <UndoPanel namespace={h.namespace} stage={h.stage.name} name={en.operation} {miraURL} onClose={close} />
                      </td></tr>
                    {/if}
                  {/each}
                </tbody>
              </table>
            </div>
          {/if}
        </section>
      {/each}
    {/if}
    <Pager {page} {href} label={one ? 'Pages of changes' : 'Pages of environments'} noun={one ? 'changes' : 'environments'} testid="history-pager" />
  {/if}
</div>

<style>
  .hv-link { color: var(--c-accent); text-decoration: underline; }
  .hv-stage { display: flex; flex-direction: column; gap: var(--sp-2); }
  .hv-stage p { margin: 0; }
  .hv-source { font-weight: 600; color: var(--c-text-2); margin-right: var(--sp-2); }
  .hv-source.hv-partial { color: var(--c-text-3); }
  .hv-notes h3 { margin: 0; font-weight: 600; }
  .hv-notes { overflow-wrap: break-word; }
  .hv-table a { color: var(--c-accent); }
  .hv-table a.badge-warn { color: var(--c-warn); text-decoration: none; }
  /* The summary is a page of twenty-five short rows: the page scrolls, not a box. */
  .hv-summary-scroll { max-height: none; }
  .hv-table tbody th .hv-source { margin: 0; font-weight: 400; }
  .hv-none { color: var(--c-text-3); }
  .hv-seq, .hv-class, .hv-digest { color: var(--c-text-2); }
  .hv-reason { color: var(--c-text-2); }
  .hv-deciders { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-1); }
  .hv-undo-row td { background: var(--c-surface-inset); }
  .hv-undo-row:hover { background: none; }
  /* The tints are translucent, so on a hovered row a chip loses contrast; lay each on the surface. */
  .hv-table .badge-ok { background: linear-gradient(var(--c-ok-bg), var(--c-ok-bg)), var(--c-surface); }
  .hv-table .badge-err { background: linear-gradient(var(--c-err-bg), var(--c-err-bg)), var(--c-surface); }
  .hv-table .badge-info { background: linear-gradient(var(--c-info-bg), var(--c-info-bg)), var(--c-surface); }
  .hv-table .badge-neutral { background: linear-gradient(var(--c-neutral-bg), var(--c-neutral-bg)), var(--c-surface); }
  .state-box { display: flex; flex-direction: column; align-items: center; gap: var(--sp-2); padding: var(--sp-8) var(--sp-4); text-align: center; color: var(--c-text-3); }
  .state-box h2 { color: var(--c-text-2); }
</style>
