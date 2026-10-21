<script>
  import { onDestroy } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { serviceUrl } from '../lib/router.ts';
  import { shortDigest } from '../lib/delivery.ts';
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
</script>

<div class="product-page">
  <PageHeader
    title="Published versions"
    subtitle="The versions your environments know about, whether their signature was checked and where each one runs."
    count={data ? `${services.length} service${services.length === 1 ? '' : 's'}` : ''}
    asOf={data?.asOf}
    revalidating={!!data && loader.loading}
  />
  <DeliveryNav current="delivery-publish" />

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
  .pv-contract, .pv-stages { margin: 0; }
  .pv-stages { display: flex; gap: var(--sp-3); flex-wrap: wrap; }
  .state-box { display: flex; flex-direction: column; align-items: center; gap: var(--sp-2); padding: var(--sp-8) var(--sp-4); text-align: center; color: var(--c-text-3); }
  .state-box h2 { color: var(--c-text-2); }
</style>
