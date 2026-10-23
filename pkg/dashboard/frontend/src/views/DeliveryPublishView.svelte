<script>
  import Person from '../components/Person.svelte';
  import { onDestroy } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { serviceUrl } from '../lib/router.ts';
  import { shortDigest, sentence, timeLabel } from '../lib/delivery.ts';
  import PageHeader from '../components/PageHeader.svelte';
  import DeliveryNav from '../components/DeliveryNav.svelte';
  import DeliveryProblem from '../components/DeliveryProblem.svelte';
  import ListingNote from '../components/ListingNote.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import CopyableIdentifier from '../components/CopyableIdentifier.svelte';

  // Published versions per service, whether their signature was checked and
  // where each one runs or was verified.
  let { refreshTick = 0 } = $props();

  const loader = createProductLoader(() => api.deliveryPublished());
  $effect(() => { loader.sync(String(refreshTick)); });
  onDestroy(() => loader.destroy());

  const data = $derived(loader.data);
  const services = $derived(data?.services ?? []);

  // Publishes Pacto refused, for whoever supervises: the check that refused
  // each and, for the version rule, the bump the change needs. Its own read,
  // so a cluster that keeps no record of them never hides the versions.
  const refusalsLoader = createProductLoader(() => api.deliveryRefusals());
  $effect(() => { refusalsLoader.sync(String(refreshTick)); });
  onDestroy(() => refusalsLoader.destroy());
  const refused = $derived(refusalsLoader.data);
  const BUMPS = { major: 'a major version', minor: 'at least a minor version', patch: 'a patch version' };
</script>

<div class="product-page">
  <PageHeader
    title="Published versions"
    subtitle="The versions your environments know about, whether their signature was checked and where each one runs, and the publishes Pacto refused."
    count={data ? `${services.length} service${services.length === 1 ? '' : 's'}` : ''}
    asOf={data?.asOf}
    revalidating={!!data && loader.loading}
  />
  <DeliveryNav current="delivery-publish" />

  <section class="pv-refused" aria-labelledby="pv-refused" data-testid="refused-publishes">
    <h2 id="pv-refused" class="t-section-title">Recently refused</h2>
    {#if !refused && refusalsLoader.loading}
      <p class="t-body-2" role="status">Reading refused publishes…</p>
    {:else if !refused && refusalsLoader.error}
      <p class="t-body-2 pv-err" role="alert">Could not read refused publishes: {refusalsLoader.error?.message || refusalsLoader.error}. <button type="button" class="btn btn-sm" onclick={() => refusalsLoader.refresh()}>Retry</button></p>
    {:else if refused && !refused.recorded}
      <p class="t-body-2 pv-none" data-testid="refused-unrecorded">This cluster keeps no record of refused publishes, so none can be shown here. <code>pacto publish</code> shows a refusal and the version bump it needs to whoever ran it.</p>
    {:else if refused}
      {#if refused.completeness === 'partial'}
        <p class="t-body-2 pv-partial" role="status" data-testid="refused-partial">This list may be incomplete{#if refused.note}: {refused.note}{/if}.{#if refused.unreadable?.length}{' '}Could not read: {refused.unreadable.join('; ')}.{/if}</p>
      {:else if refused.note}
        <p class="t-meta" data-testid="refused-note">{sentence(refused.note)}</p>
      {/if}
      {#if (refused.refusals ?? []).length === 0}
        <p class="t-body-2 pv-none">No publish was refused{refused.completeness === 'partial' ? ' that Pacto could read' : ''}.</p>
      {:else}
        <ul class="pl-list">
          {#each refused.refusals as r, i (r.service + '@' + r.version + '#' + i)}
            <li class="pl-row pv-row">
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

  {#if !data && loader.loading}
    <EmptyState loading={true} rows={4} />
  {:else if !data && loader.error}
    <DeliveryProblem error={loader.error} onRetry={() => loader.refresh()} />
  {:else if data}
    <ListingNote listing={data} error={loader.error} onRetry={() => loader.refresh()} />
    {#if services.length === 0}
      <div class="state-box"><h2>Nothing published yet</h2><p>No environment you can see knows a version. Publish one with <code>pacto publish</code>.</p></div>
    {:else}
      {#each services as svc (svc.service)}
        <section class="pv-svc" aria-labelledby={`pv-${svc.service}`}>
          <h2 id={`pv-${svc.service}`} class="t-section-title"><a href={serviceUrl(svc.service)}>{svc.service}</a></h2>
          <ul class="pl-list">
            {#each svc.versions ?? [] as v (v.digest)}
              <li class="pl-row pv-row">
                <div class="pv-head">
                  <strong>{v.version || shortDigest(v.digest)}</strong>
                  {#if v.signed}<span class="badge badge-ok">Signature checked</span>{:else}<span class="badge badge-neutral">Signature not checked here</span>{/if}
                  <code class="t-meta" title={v.digest}>{shortDigest(v.digest)}</code>
                </div>
                {#if v.contract}<p class="t-body-2 pv-contract">Contract: <CopyableIdentifier value={v.contract} label="contract reference" /></p>{/if}
                {#if v.stages?.length}
                  <p class="t-body-2 pv-stages">
                    {#each v.stages as on (on.namespace + '/' + on.stage)}
                      <span class="pv-stage">{on.environment}: {on.running ? 'running' : 'known'}{#if on.verified}, verified{/if}{#if on.failed}, failed{/if}</span>
                    {/each}
                  </p>
                {/if}
              </li>
            {/each}
          </ul>
        </section>
      {/each}
    {/if}
  {/if}
</div>

<style>
  .pv-svc { display: flex; flex-direction: column; gap: var(--sp-2); }
  .pv-svc h2 a { color: var(--c-accent); }
  .pv-row { display: flex; flex-direction: column; gap: var(--sp-1); padding: var(--sp-3); }
  .pv-head { display: flex; gap: var(--sp-2); align-items: center; flex-wrap: wrap; }
  .pv-contract, .pv-stages, .pv-row p, .pv-refused > p { margin: 0; }
  .pv-refused { display: flex; flex-direction: column; gap: var(--sp-2); }
  .pv-none { color: var(--c-text-3); }
  .pv-err { color: var(--c-err); }
  .pv-partial { padding: var(--sp-2) var(--sp-3); border-radius: var(--radius-sm); background: var(--c-warn-bg); color: var(--c-warn); border: 1px solid var(--c-warn-border); }
  .pv-stages { display: flex; gap: var(--sp-3); flex-wrap: wrap; }
  .state-box { display: flex; flex-direction: column; align-items: center; gap: var(--sp-2); padding: var(--sp-8) var(--sp-4); text-align: center; color: var(--c-text-3); }
  .state-box h2 { color: var(--c-text-2); }
</style>
