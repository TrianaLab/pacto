<script>
  import Person from '../components/Person.svelte';
  import { onDestroy } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { serviceUrl, deliveryPublishUrl } from '../lib/router.ts';
  import { urlInt, limitParam } from '../lib/paging.ts';
  import { formatCount } from '../lib/format.ts';
  import Pager from '../components/Pager.svelte';
  import ListFilter from '../components/ListFilter.svelte';
  import { shortDigest, sentence, timeLabel } from '../lib/delivery.ts';
  import PageHeader from '../components/PageHeader.svelte';
  import DeliveryNav from '../components/DeliveryNav.svelte';
  import DeliveryProblem from '../components/DeliveryProblem.svelte';
  import ListingNote from '../components/ListingNote.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import CopyableIdentifier from '../components/CopyableIdentifier.svelte';
  import CappedList from '../components/CappedList.svelte';

  // Published versions per service, whether their signature was checked and
  // where each one runs or was verified, paged by service on the server.
  let { offset = '', limit = '', q = '', refreshTick = 0 } = $props();

  const loader = createProductLoader(() => api.deliveryPublished({
    offset: urlInt(offset) || undefined, limit: urlInt(limit) || undefined, q: q || undefined,
  }));
  const question = $derived(`${urlInt(offset)}@@${urlInt(limit)}@@${q}`);
  // The same question without its page: turning a page keeps the rows on screen.
  const listQuestion = $derived(q);
  $effect(() => { loader.sync(`${question}@@${refreshTick}`, question, listQuestion); });
  onDestroy(() => loader.destroy());

  const answer = $derived(loader.answer(question, listQuestion));
  const data = $derived(answer.data);
  const services = $derived(data?.services ?? []);
  const serviceTotal = $derived(data?.page?.total ?? services.length);
  const href = (off, lim) => deliveryPublishUrl({ q, offset: off, limit: limitParam(lim, limit, data?.page?.limit ?? lim) });

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
  const BUMPS = { major: 'a major version', minor: 'at least a minor version', patch: 'a patch version' };
</script>

<div class="product-page">
  <PageHeader
    title="Published versions"
    subtitle="The versions your environments know about, whether their signature was checked and where each one runs, and the publishes Pacto refused."
    count={data ? `${formatCount(serviceTotal)} service${serviceTotal === 1 ? '' : 's'}` : ''}
    asOf={data?.asOf}
    revalidating={!!data && loader.loading}
    paging={answer.paging}
  />
  <DeliveryNav current="delivery-publish" />

  <section class="pv-refused" aria-labelledby="pv-refused" data-testid="refused-publishes">
    <h2 id="pv-refused" class="t-section-title">Recently refused{#if refused?.total > (refused?.refusals ?? []).length}{' '}<span class="t-meta" data-testid="refused-count">(the latest {formatCount(refused.refusals.length)} of {formatCount(refused.total)})</span>{/if}</h2>
    {#if !refused && refusalsLoader.loading}
      <p class="t-body-2" role="status">Reading refused publishes…</p>
    {:else if !refused && refusalsLoader.error}
      <p class="t-body-2 pv-err" role="alert">Could not read refused publishes: {refusalsLoader.error?.message || refusalsLoader.error}. <button type="button" class="btn btn-sm" onclick={() => refusalsLoader.refresh()}>Retry</button></p>
    {:else if refused && !refused.recorded}
      <p class="t-body-2 pv-none" data-testid="refused-unrecorded">This cluster keeps no record of refused publishes, so none can be shown here. <code>pacto publish</code> shows a refusal and the version bump it needs to whoever ran it.</p>
    {:else if refused}
      {#if refused.completeness === 'partial'}
        <p class="t-body-2 pv-partial" role="status" data-testid="refused-partial">This list may be incomplete{#if refused.note}: {refused.note}{/if}.{#if refused.unreadable?.length}{' '}Could not read: <CappedList items={refused.unreadable} max={3} inline={true} label="Repositories Pacto could not read">{#snippet item(u)}{u}{/snippet}</CappedList>.{/if}</p>
      {:else if refused.note}
        <p class="t-meta" data-testid="refused-note">{sentence(refused.note)}</p>
      {/if}
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
  </section>

  <h2 class="t-section-title">Versions by service</h2>
  <ListFilter value={q} href={(v) => deliveryPublishUrl({ q: v })} label="Filter services by name" placeholder="Service" testid="published-filter" />
  {#if !data && loader.loading}
    <EmptyState loading={true} rows={4} />
  {:else if !data && loader.error}
    <DeliveryProblem error={loader.error} onRetry={() => loader.refresh()} />
  {:else if data}
    <ListingNote listing={data} error={loader.error} onRetry={() => loader.refresh()} />
    {#if services.length === 0 && q}
      <div class="state-box"><h2>Nothing matches the filter</h2><p>No service you can see matches “{q}”.</p></div>
    {:else if services.length === 0}
      <div class="state-box"><h2>Nothing published yet</h2><p>No environment you can see knows a version. Publish one with <code>pacto publish</code>.</p></div>
    {:else}
      {#each services as svc (svc.service)}
        <section class="pv-svc" aria-labelledby={`pv-${svc.service}`}>
          <h2 id={`pv-${svc.service}`} class="t-section-title"><a href={serviceUrl(svc.service)}>{svc.service}</a></h2>
          <ul class="pl-list">
            {#each open[svc.service] ? (svc.versions ?? []) : (svc.versions ?? []).slice(0, VERSIONS) as v (v.digest)}
              {@const sk = `${svc.service}@${v.digest}`}
              {@const stages = stagesFirst(v.stages)}
              <li class="pl-row pl-row--stack pv-row">
                <!-- One line per version: digests short with a copy button, and where it runs as a
                     count that opens the per-environment list. -->
                <div class="pv-head">
                  {#if v.version}<strong>{v.version}</strong>{/if}
                  {#if v.signed}<span class="badge badge-ok">Signature checked</span>{:else}<span class="badge badge-neutral">Signature not checked here</span>{/if}
                  <CopyableIdentifier value={v.digest} display={shortDigest(v.digest)} label="digest" />
                  {#if v.contract}<span class="t-meta pv-contract">Contract <CopyableIdentifier value={v.contract} display={contractShort(v.contract)} label="contract reference" /></span>{/if}
                  {#if stages.length}
                    {@const running = stages.filter((on) => on.running).length}
                    {@const failed = stages.filter((on) => on.failed).length}
                    <button type="button" class="btn btn-sm pv-envs" aria-expanded={!!open[sk]} data-testid="published-envs" onclick={() => { open[sk] = !open[sk]; }}>{formatCount(stages.length)} environment{stages.length === 1 ? '' : 's'}{#if running}, {formatCount(running)} running{/if}{#if failed}, {formatCount(failed)} failed{/if}</button>
                  {/if}
                </div>
                {#if open[sk]}
                  <p class="t-body-2 pv-stages" data-testid="published-stages">
                    {#each stages as on (on.namespace + '/' + on.stage)}
                      <span class="pv-stage">{on.environment}: {on.running ? 'running' : 'known'}{#if on.verified}, verified{/if}{#if on.failed}, failed{/if}</span>{' '}
                    {/each}
                  </p>
                {/if}
              </li>
            {/each}
          </ul>
          {#if (svc.versions?.length ?? 0) > VERSIONS}<button type="button" class="btn btn-sm pv-more" aria-expanded={!!open[svc.service]} onclick={() => { open[svc.service] = !open[svc.service]; }}>{open[svc.service] ? 'Show fewer' : `Show all ${formatCount(svc.versions.length)} versions`}</button>{/if}
        </section>
      {/each}
      <Pager page={data.page} {href} label="Pages of services" noun="services" testid="published-pager" />
    {/if}
  {/if}
</div>

<style>
  .pv-svc { display: flex; flex-direction: column; gap: var(--sp-2); }
  .pv-svc h2 a { color: var(--c-accent); }
  .pv-row { gap: var(--sp-1); padding: var(--sp-2) var(--sp-3); min-width: 0; overflow-wrap: break-word; }
  .pv-head { display: flex; gap: var(--sp-2); align-items: center; flex-wrap: wrap; }
  .pv-contract, .pv-stages, .pv-row p, .pv-refused > p { margin: 0; }
  .pv-contract { display: inline-flex; align-items: center; gap: var(--sp-1); min-width: 0; max-width: 100%; }
  .pv-envs { margin-left: auto; }
  .pv-refused { display: flex; flex-direction: column; gap: var(--sp-2); }
  .pv-none { color: var(--c-text-3); }
  .pv-err { color: var(--c-err); }
  .pv-partial { padding: var(--sp-2) var(--sp-3); border-radius: var(--radius-sm); background: var(--c-warn-bg); color: var(--c-warn); border: 1px solid var(--c-warn-border); }
  .pv-stages { display: flex; gap: var(--sp-3); flex-wrap: wrap; align-items: center; }
  .pv-more { align-self: flex-start; }
  .state-box { display: flex; flex-direction: column; align-items: center; gap: var(--sp-2); padding: var(--sp-8) var(--sp-4); text-align: center; color: var(--c-text-3); }
  .state-box h2 { color: var(--c-text-2); }
</style>
