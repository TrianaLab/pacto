<script>
  import Person from '../components/Person.svelte';
  import { onDestroy } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { deliveryPublishUrl, fleetEntityUrl } from '../lib/router.ts';
  import { deliveryServiceUrl } from '../lib/deliveryLinks.svelte.ts';
  import { urlInt, limitParam } from '../lib/paging.ts';
  import { formatCount, countOf } from '../lib/format.ts';
  import Pager from '../components/Pager.svelte';
  import ListFilter from '../components/ListFilter.svelte';
  import { shortDigest, sentence, timeLabel, lifecycleWords } from '../lib/delivery.ts';
  import PageHeader from '../components/PageHeader.svelte';
  import DeliveryNav from '../components/DeliveryNav.svelte';
  import DeliveryProblem from '../components/DeliveryProblem.svelte';
  import ListingNote from '../components/ListingNote.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import CopyableIdentifier from '../components/CopyableIdentifier.svelte';
  import CappedList from '../components/CappedList.svelte';
  import DeliveryLead from '../components/DeliveryLead.svelte';
  import PublishFigure from '../components/figure/PublishFigure.svelte';
  import DeliveryServiceFilter from '../components/DeliveryServiceFilter.svelte';
  import DeliveryLifecycle from '../components/DeliveryLifecycle.svelte';

  // Published versions per service, whether their signature was checked and
  // where each one runs or was verified, paged by service on the server.
  // signed narrows the list to the services whose every version is signed (yes),
  // that have an unsigned version (no) or a signature not known here (unknown):
  // the figure's three lanes. `service` and `domain` narrow it to one exact service,
  // from a service page's link; every link the page builds keeps them.
  let { offset = '', limit = '', q = '', signed = '', service = '', domain = '', refreshTick = 0 } = $props();
  const keep = $derived({ service: service || undefined, domain: (service && domain) || undefined });

  const loader = createProductLoader(() => api.deliveryPublished({
    offset: urlInt(offset) || undefined, limit: urlInt(limit) || undefined, q: q || undefined,
    // The router passes any string; the server refuses one it does not know.
    signed: /** @type {any} */ (signed || undefined),
    service: service || undefined, domain: keep.domain,
  }));
  const question = $derived(`${urlInt(offset)}@@${urlInt(limit)}@@${q}@@${signed}@@${keep.domain}/${service}`);
  // The same question without its page: turning a page keeps the rows on screen.
  const listQuestion = $derived(`${q}@@${signed}@@${keep.domain}/${service}`);
  $effect(() => { loader.sync(`${question}@@${refreshTick}`, question, listQuestion); });
  onDestroy(() => loader.destroy());

  const answer = $derived(loader.answer(question, listQuestion));
  const data = $derived(answer.data);
  const services = $derived(data?.services ?? []);
  const serviceTotal = $derived(data?.page?.total ?? services.length);
  const href = (off, lim) => deliveryPublishUrl({ q, signed, ...keep, offset: off, limit: limitParam(lim, limit, data?.page?.limit ?? lim) });
  // A version's lifecycle opens under its row (its version links its fleet revision
  // when one is known): the panel asks /api/delivery/lifecycle by artifact digest, in the domain the service's
  // Stages name (the server lists a service once per domain).
  let lifecycle = $state({});

  // Publishes Pacto refused, for whoever supervises: the check that refused
  // each and, for the version rule, the bump the change needs. Its own read,
  // so a cluster that keeps no record of them never hides the versions.
  const refusalsLoader = createProductLoader(() => api.deliveryRefusals());
  $effect(() => { refusalsLoader.sync(String(refreshTick)); });
  onDestroy(() => refusalsLoader.destroy());
  const refused = $derived(refusalsLoader.data);
  // A service shows its newest few versions and each version its first few
  // Stages, running and failed first, so one page stays one page. open holds
  // what the reader asked to see whole, keyed by service (and digest).
  const VERSIONS = 5;
  let open = $state({});
  // A contract reference by its digest alone; the repository is on hover and in the copy.
  const contractShort = (ref) => (ref.includes('@') ? shortDigest(ref.slice(ref.lastIndexOf('@') + 1)) : ref);
  // Running and failed first, so the list a count opens leads with what matters.
  const stagesFirst = (stages) => [...(stages ?? [])].sort((a, b) => (b.running || b.failed ? 1 : 0) - (a.running || a.failed ? 1 : 0));
  // The page's answer: how many publishes Pacto refused lately, when the
  // cluster keeps a record of them.
  const refusedTotal = $derived(refused?.total ?? refused?.refusals?.length ?? 0);
  const lead = $derived(!refused ? null
    : !refused.recorded ? { head: 'Refused publishes are not recorded here.', rest: '' }
    : refusedTotal ? { head: `${countOf(refusedTotal, 'publish', 'publishes')} refused lately.`, rest: refused.completeness === 'partial' ? 'There may be more.' : '' }
    : { head: 'Nothing refused lately.', rest: refused.completeness === 'partial' ? 'The list may be incomplete.' : '' });
  // Each version's signature, said once for the page: a version is marked only
  // where its state differs from the most common one (a tie leaves the
  // unsigned ones marked). Unknown is its own state,
  // never drawn as unsigned.
  const SIG = {
    signed: { tone: 'badge-ok', mark: lifecycleWords.signature.signed, line: 'Signature checked on every version shown' },
    unknown: { tone: 'badge-neutral', mark: lifecycleWords.signature.unknown, line: 'Signature not known here for the versions shown' },
    unsigned: { tone: 'badge-warn', mark: lifecycleWords.signature.unsigned, line: 'No signature verified on the versions shown' },
  };
  const shown = $derived(services.flatMap((svc) => svc.versions ?? []));
  const sigOf = (v) => (SIG[v.signature] ? v.signature : 'unknown');
  const sigCommon = $derived(Object.keys(SIG).reduce((best, k) => (shown.filter((v) => sigOf(v) === k).length > shown.filter((v) => sigOf(v) === best).length ? k : best), 'signed'));
  const sigMixed = $derived(new Set(shown.map(sigOf)).size > 1);
  const sigLine = $derived(!shown.length ? '' : `${SIG[sigCommon].line}${sigMixed ? ', except where marked' : ''}.`);
  const BUMPS = { major: 'a major version', minor: 'at least a minor version', patch: 'a patch version' };
</script>

<div class="product-page">
  <PageHeader
    title="Published versions"
    count={data ? countOf(serviceTotal, 'service') : ''}
    asOf={data?.asOf}
    revalidating={!!data && loader.loading}
    paging={answer.paging}
  >
    {#if lead}<DeliveryLead head={lead.head} rest={lead.rest} tipLabel="Published versions"
      tip="The versions your environments know about, whether their signature was checked and where each one runs, and the publishes Pacto refused." />{/if}
  </PageHeader>
  <DeliveryNav current="delivery-publish" />
  <DeliveryServiceFilter {service} {domain} clear={deliveryPublishUrl({ q, signed })} />
  {#if data?.counts?.services}<PublishFigure counts={data.counts} href={(s) => deliveryPublishUrl({ q, signed: s, ...keep })} serviceHref={deliveryServiceUrl} />{/if}

  <!-- The refused publishes are a count that opens: the versions are what the page lists. -->
  <section class="pv-refused" aria-labelledby="pv-refused" data-testid="refused-publishes">
    {#if !refused && refusalsLoader.loading}
      <h2 id="pv-refused" class="t-section-title">Refused publishes</h2>
      <p class="t-body-2" role="status">Reading refused publishes…</p>
    {:else if !refused && refusalsLoader.error}
      <h2 id="pv-refused" class="t-section-title">Refused publishes</h2>
      <p class="t-body-2 pv-err" role="alert">Could not read refused publishes: {refusalsLoader.error?.message || refusalsLoader.error}. <button type="button" class="btn btn-sm" onclick={() => refusalsLoader.refresh()}>Retry</button></p>
    {:else if refused}
      {#if refused.recorded && refused.completeness === 'partial'}
        <p class="t-body-2 pv-partial" role="status" data-testid="refused-partial">This list may be incomplete{#if refused.note}: {refused.note}{/if}.{#if refused.unreadable?.length}{' '}Could not read: <CappedList items={refused.unreadable} max={3} inline={true} label="Repositories Pacto could not read">{#snippet item(u)}{u}{/snippet}</CappedList>.{/if}</p>
      {/if}
      <!-- Open when a refusal is the page's answer, so the answer is on screen. -->
      <details class="disclosure pv-fold" open={!!(refused.recorded && refusedTotal)}>
        <summary>
          <span class="disclosure-caret" data-motion aria-hidden="true"></span>
          <h2 id="pv-refused" class="t-section-title">Refused publishes</h2>
          <span class="badge {refused.recorded && refusedTotal ? 'badge-err' : 'badge-neutral'}" data-testid="refused-chip">{refused.recorded ? formatCount(refusedTotal) : 'Not recorded'}</span>
        </summary>
        <div class="pv-fold-body">
          {#if !refused.recorded}
            <p class="t-body-2 pv-none" data-testid="refused-unrecorded">This cluster keeps no record of refused publishes, so none can be shown here. <code>pacto publish</code> shows a refusal and the version bump it needs to whoever ran it.</p>
          {:else}
            {#if refused.total > (refused.refusals ?? []).length}<p class="t-meta" data-testid="refused-count">(the latest {formatCount(refused.refusals.length)} of {formatCount(refused.total)})</p>{/if}
            {#if refused.completeness !== 'partial' && refused.note}<p class="t-meta" data-testid="refused-note">{sentence(refused.note)}</p>{/if}
            {#if (refused.refusals ?? []).length === 0}
              <p class="t-body-2 pv-none">No publish was refused{refused.completeness === 'partial' ? ' that Pacto could read' : ''}.</p>
            {:else}
              <ul class="pl-list">
                {#each refused.refusals as r, i (r.service + '@' + r.version + '#' + i)}
                  <li class="pl-row pl-row--stack pv-row">
                    <div class="pv-head">
                      <strong>{r.service} {r.version}</strong>
                      {#if r.requiredBump}<span class="badge badge-err">Needs {BUMPS[r.requiredBump] ?? r.requiredBump}</span>{:else}<span class="badge badge-err">Refused: {r.check}</span>{/if}
                      {#if r.verified}<span class="badge badge-ok">Signed record</span>{/if}
                    </div>
                    <p class="t-body-2">{sentence(r.reason)}{#if r.baseline}{' '}Compared with {r.baseline}.{/if}</p>
                    <p class="t-meta">{#if r.actor}By <Person user={r.actor} />,{' '}{/if}<time datetime={r.at}>{timeLabel(r.at)}</time></p>
                  </li>
                {/each}
              </ul>
            {/if}
          {/if}
        </div>
      </details>
    {/if}
  </section>

  <h2 class="t-section-title" data-view-total={data ? serviceTotal : undefined}>Versions by service</h2>
  {#if signed}<p class="t-meta">{signed === 'no' ? 'Services with a version that is not signed, and only those versions.' : signed === 'unknown' ? 'Services with a version whose signature is not known here, and only those versions.' : 'Services whose every version is signed.'} <a href={deliveryPublishUrl({ q, ...keep })}>Show every service</a></p>{/if}
  <ListFilter value={q} href={(v) => deliveryPublishUrl({ q: v, signed, ...keep })} label="Filter services by name" placeholder="Service" testid="published-filter" />
  {#if !data && loader.loading}
    <EmptyState loading={true} rows={4} />
  {:else if !data && loader.error}
    <DeliveryProblem error={loader.error} onRetry={() => loader.refresh()} />
  {:else if data}
    <ListingNote listing={data} error={loader.error} onRetry={() => loader.refresh()} />
    {#if sigLine}<p class="t-meta pv-sigline" data-testid="published-signatures">{sigLine}</p>{/if}
    {#if services.length === 0 && q}
      <div class="state-box"><h2>Nothing matches the filter</h2><p>No service you can see matches “{q}”.</p></div>
    {:else if services.length === 0}
      <div class="state-box"><h2>Nothing published yet</h2><p>No environment you can see knows a version. Publish one with <code>pacto publish</code>.</p></div>
    {:else}
      {#each services as svc (svc.service + '@' + (svc.domain ?? ''))}
        {@const id = svc.domain ? `${svc.service}@${svc.domain}` : svc.service}
        <section class="pv-svc" aria-labelledby={`pv-${id}`}>
          <h2 id={`pv-${id}`} class="t-section-title"><a href={deliveryServiceUrl(svc.service)}>{svc.service}</a>{#if svc.domain}{' '}<span class="t-meta"><bdi>{svc.domain}</bdi></span>{/if}</h2>
          <ul class="pl-list">
            {#each open[id] ? (svc.versions ?? []) : (svc.versions ?? []).slice(0, VERSIONS) as v (v.digest)}
              {@const sk = `${id}@${v.digest}`}
              {@const stages = stagesFirst(v.stages)}
              <li class="pl-row pl-row--stack pv-row">
                <!-- One line per version: digests short with a copy button, and where it runs as a
                     count that opens the per-environment list. -->
                <div class="pv-head" class:pv-sigcol={sigMixed}>
                  <strong class="pv-ver" title={v.version || undefined}>{#if v.revision && v.version}<a href={fleetEntityUrl('revision', v.revision)}>{v.version}</a>{:else}{v.version ?? ''}{/if}</strong>
                  {#if sigMixed}<span class="pv-sig">{#if sigOf(v) !== sigCommon}<span class="badge {SIG[sigOf(v)].tone}">{SIG[sigOf(v)].mark}</span>{/if}</span>{/if}
                  <CopyableIdentifier value={v.digest} display={shortDigest(v.digest)} label="digest" />
                  {#if v.contract}<span class="t-meta pv-contract">Contract <CopyableIdentifier value={v.contract} display={contractShort(v.contract)} label="contract reference" /></span>{:else}<span></span>{/if}
                  <span class="pv-acts">
                    {#if stages.length}
                      {@const running = stages.filter((on) => on.running).length}
                      {@const failed = stages.filter((on) => on.failed).length}
                      <button type="button" class="btn btn-sm pv-envs" aria-expanded={!!open[sk]} data-testid="published-envs" onclick={() => { open[sk] = !open[sk]; }}>{formatCount(stages.length)} environment{stages.length === 1 ? '' : 's'}{#if running}, {formatCount(running)} running{/if}{#if failed}, {formatCount(failed)} failed{/if}</button>
                    {/if}
                    <button type="button" class="btn btn-sm pv-envs" aria-expanded={!!lifecycle[sk]} aria-label={`Lifecycle of ${svc.service} ${v.version || shortDigest(v.digest)}`}
                      onclick={() => { lifecycle[sk] = !lifecycle[sk]; }}>Lifecycle</button>
                  </span>
                </div>
                {#if v.sourceRevision}<p class="t-meta pv-source" title={v.sourceRevision}>Source revision <bdi class="pv-mono">{v.sourceRevision}</bdi>, an annotation, not signed</p>{/if}
                {#if open[sk]}
                  <p class="t-body-2 pv-stages" data-testid="published-stages">
                    {#each stages as on (on.namespace + '/' + on.stage)}
                      <span class="pv-stage">{on.environment}: {on.running ? 'running' : 'known'}{#if on.verified}, verified{/if}{#if on.failed}, failed{/if}</span>{' '}
                    {/each}
                  </p>
                {/if}
                {#if lifecycle[sk]}<DeliveryLifecycle query={{ service: svc.service, domain: svc.domain ?? '', digest: v.digest }} />{/if}
              </li>
            {/each}
          </ul>
          {#if (svc.versions?.length ?? 0) > VERSIONS}<button type="button" class="btn btn-sm pv-more" aria-expanded={!!open[id]} onclick={() => { open[id] = !open[id]; }}>{open[id] ? 'Show fewer' : `Show all ${formatCount(svc.versions.length)} versions`}</button>{/if}
        </section>
      {/each}
      <Pager page={data.page} {href} label="Pages of services" noun="services" testid="published-pager" />
    {/if}
  {/if}
</div>

<style>
  .pv-svc { display: flex; flex-direction: column; gap: var(--sp-2); }
  .pv-svc h2 a { color: var(--c-accent); }
  .pv-row { gap: var(--sp-1); padding: var(--sp-1) var(--sp-3); min-width: 0; overflow-wrap: break-word; }
  .pv-head { display: flex; gap: var(--sp-2); align-items: center; flex-wrap: wrap; }
  .pv-sigline { margin: 0; }
  /* On a wide screen every version lines up in columns, so a long pre-release
     version is cut short (whole on hover) instead of pushing the rest along. */
  @media (min-width: 769px) {
    .pv-row { min-height: 0; }
    /* The digest and contract columns keep room for a 12-character short form and its
       copy button: as auto columns they shrank to one character per line once the
       environments button grew to a 44px touch target. */
    .pv-head { display: grid; grid-template-columns: minmax(0, 13rem) minmax(calc(12ch + 3rem), auto) minmax(calc(12ch + 8rem), 1fr) auto; column-gap: var(--sp-3); }
    .pv-head.pv-sigcol { grid-template-columns: minmax(0, 13rem) 12rem minmax(calc(12ch + 3rem), auto) minmax(calc(12ch + 8rem), 1fr) auto; }
    .pv-ver { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    .pv-envs { justify-self: end; min-height: 26px; padding: 0 var(--sp-2); }
  }
  .pv-contract, .pv-stages, .pv-row p, .pv-refused > p { margin: 0; }
  .pv-contract { display: inline-flex; align-items: center; gap: var(--sp-1); min-width: 0; max-width: 100%; }
  .pv-envs { margin-left: auto; }
  .pv-acts { display: inline-flex; flex-wrap: wrap; gap: var(--sp-1); justify-self: end; margin-left: auto; }
  .pv-acts .pv-envs { margin-left: 0; }
  .pv-source { overflow-wrap: anywhere; }
  .pv-mono { font-family: var(--font-mono); }
  .pv-refused { display: flex; flex-direction: column; gap: var(--sp-2); }
  .pv-fold > summary h2 { margin: 0; }
  .pv-fold-body { display: flex; flex-direction: column; gap: var(--sp-2); margin-top: var(--sp-2); }
  .pv-fold-body > p { margin: 0; }
  .pv-none { color: var(--c-text-3); }
  .pv-err { color: var(--c-err); }
  /* A possibly incomplete list is the unknown family: neutral, with the hatch as a band
     on its leading edge so the pattern never sits under the words. */
  .pv-partial { padding: var(--sp-2) var(--sp-3) var(--sp-2) calc(var(--sp-3) + 6px); border-radius: var(--radius-sm); background: var(--c-hatch) left / 6px 100% no-repeat, var(--c-neutral-bg); color: var(--c-text-2); border: 1px solid var(--c-neutral); }
  .pv-stages { display: flex; gap: var(--sp-3); flex-wrap: wrap; align-items: center; }
  .pv-more { align-self: flex-start; }
  .state-box { display: flex; flex-direction: column; align-items: center; gap: var(--sp-2); padding: var(--sp-8) var(--sp-4); text-align: center; color: var(--c-text-3); }
  .state-box h2 { color: var(--c-text-2); }
</style>
