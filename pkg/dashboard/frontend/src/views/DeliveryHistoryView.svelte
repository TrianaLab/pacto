<script>
  import { onDestroy } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { deliveryChangeUrl, deliveryHistoryUrl } from '../lib/router.ts';
  import { typeLabel, shortDigest } from '../lib/delivery.ts';
  import PageHeader from '../components/PageHeader.svelte';
  import DeliveryNav from '../components/DeliveryNav.svelte';
  import DeliveryProblem from '../components/DeliveryProblem.svelte';
  import ListingNote from '../components/ListingNote.svelte';
  import EmptyState from '../components/EmptyState.svelte';

  // What happened on each environment, from the changes still in the cluster.
  // It is partial until signed records exist, and says so.
  let { namespace = '', stage = '', refreshTick = 0 } = $props();

  const loader = createProductLoader(() => api.deliveryHistory(namespace, stage));
  const question = $derived(`${namespace}/${stage}`);
  $effect(() => { loader.sync(`${question}@@${refreshTick}`, question); });
  onDestroy(() => loader.destroy());

  const data = $derived(loader.dataTag === question ? loader.data : null);
  const histories = $derived(data?.histories ?? []);
</script>

<div class="product-page">
  <PageHeader
    title={stage ? `History of ${namespace}/${stage}` : 'History'}
    subtitle="Past changes still kept in the cluster. Older ones are gone until signed records arrive, so this is never the whole story."
    asOf={data?.asOf}
    revalidating={!!data && loader.loading}
  />
  <DeliveryNav current="delivery-history" />
  {#if stage}<p class="t-body-2"><a class="hv-link" href={deliveryHistoryUrl()}>Show every environment</a></p>{/if}

  {#if !data && loader.loading}
    <EmptyState loading={true} rows={4} />
  {:else if !data && loader.error}
    <DeliveryProblem error={loader.error} onRetry={() => loader.refresh()} />
  {:else if data}
    <ListingNote listing={data} />
    {#if histories.length === 0}
      <div class="state-box"><h2>No history yet</h2><p>No environment you can see has a change on record.</p></div>
    {:else}
      {#each histories as h (h.namespace + '/' + h.stage.name)}
        <section class="hv-stage" aria-label={`History of ${h.namespace}/${h.stage.name}`}>
          <h2 class="t-section-title">{h.namespace}/{h.stage.name}</h2>
          <p class="t-body-2">Runs <code title={h.stage.pointer}>{shortDigest(h.stage.pointer) || 'nothing yet'}</code>{#if h.stage.frozen}, paused{/if}. {h.note}</p>
          {#if (h.entries ?? []).length === 0}
            <p class="t-body-2 hv-none">No change kept for this environment.</p>
          {:else}
            <div class="hv-scroll">
              <table class="hv-table">
                <thead><tr><th scope="col">#</th><th scope="col">Change</th><th scope="col">Version</th><th scope="col">By</th><th scope="col">How it ended</th><th scope="col">When</th></tr></thead>
                <tbody>
                  {#each h.entries as en (en.operation)}
                    <tr>
                      <td>{en.seq ?? ''}</td>
                      <td><a href={deliveryChangeUrl(h.namespace, en.operation)}>{typeLabel(en.type)}</a></td>
                      <td><code>{shortDigest(en.from) || '–'} → {shortDigest(en.to) || '–'}</code></td>
                      <td>{en.actor}{#if en.actorClass} ({en.actorClass}){/if}</td>
                      <td>{en.outcome || en.phase}{#if en.verification}, {en.verification}{/if}{#if en.reason}: {en.reason}{/if}</td>
                      <td>{#if en.finished}<time datetime={en.finished}>{en.finished}</time>{/if}</td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>
          {/if}
        </section>
      {/each}
    {/if}
  {/if}
</div>

<style>
  .hv-link { color: var(--c-accent); text-decoration: underline; }
  .hv-stage { display: flex; flex-direction: column; gap: var(--sp-2); }
  .hv-stage p { margin: 0; }
  .hv-scroll { overflow-x: auto; }
  .hv-table { border-collapse: collapse; width: 100%; font-size: var(--text-sm); }
  .hv-table th, .hv-table td { border-bottom: 1px solid var(--c-border); padding: var(--sp-2) var(--sp-3); text-align: left; vertical-align: top; }
  .hv-table th { color: var(--c-text-3); font-weight: 600; }
  .hv-table a { color: var(--c-accent); }
  .hv-none { color: var(--c-text-3); }
  .state-box { display: flex; flex-direction: column; align-items: center; gap: var(--sp-2); padding: var(--sp-8) var(--sp-4); text-align: center; color: var(--c-text-3); }
  .state-box h2 { color: var(--c-text-2); }
</style>
