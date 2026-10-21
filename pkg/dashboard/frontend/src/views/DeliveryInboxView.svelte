<script>
  import { onDestroy } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { deliveryChangeUrl, deliveryHistoryUrl, serviceUrl } from '../lib/router.ts';
  import { typeLabel, decisionLabel, shortDigest } from '../lib/delivery.ts';
  import PageHeader from '../components/PageHeader.svelte';
  import DeliveryNav from '../components/DeliveryNav.svelte';
  import DeliveryProblem from '../components/DeliveryProblem.svelte';
  import ListingNote from '../components/ListingNote.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import MoveFirstList from '../components/MoveFirstList.svelte';

  // What waits on me or my team: held changes whose question names one of my
  // teams, then paused environments and changes that could not finish.
  let { refreshTick = 0 } = $props();

  const loader = createProductLoader(() => api.deliveryInbox());
  $effect(() => { loader.sync(String(refreshTick)); });
  onDestroy(() => loader.destroy());

  const inbox = $derived(loader.data);
  const held = $derived(inbox?.held ?? []);
  const frozen = $derived(inbox?.frozen ?? []);
  const errored = $derived(inbox?.errored ?? []);
  const nothing = $derived(!!inbox && held.length + frozen.length + errored.length === 0);
</script>

<div class="product-page">
  <PageHeader
    title="Inbox"
    subtitle="What waits on you or your team: the question asked and the services it affects."
    count={inbox ? `${held.length} waiting for a decision` : ''}
    asOf={inbox?.asOf}
    revalidating={!!inbox && loader.loading}
  />
  <DeliveryNav current="delivery-inbox" />

  {#if !inbox && loader.loading}
    <EmptyState loading={true} rows={3} />
  {:else if !inbox && loader.error}
    <DeliveryProblem error={loader.error} onRetry={() => loader.refresh()} />
  {:else if inbox}
    <ListingNote listing={inbox} error={loader.error} onRetry={() => loader.refresh()} />

    {#if nothing}
      <div class="state-box" data-testid="inbox-empty">
        <h2>{inbox.completeness === 'partial' ? 'Nothing waits on you that you can see' : 'Nothing waits on you'}</h2>
        <p>No change is held for your teams, and none of your environments is paused or stuck.</p>
      </div>
    {:else}
      <section aria-labelledby="inbox-held">
        <h2 id="inbox-held" class="t-section-title">Waiting for your decision</h2>
        {#if held.length === 0}
          <p class="t-body-2 dl-none">No change is held for your teams.</p>
        {:else}
          <ul class="pl-list" data-testid="inbox-held">
            {#each held as it (it.namespace + '/' + it.name)}
              <li class="pl-row dl-row">
                <a class="dl-title" href={deliveryChangeUrl(it.namespace, it.name)}>
                  {typeLabel(it.type)} on {it.namespace}/{it.stage}{#if it.to}: {shortDigest(it.to)}{/if}
                </a>
                {#if it.question}<p class="dl-question t-body">{it.question}</p>{/if}
                <MoveFirstList order={it.order} testid="inbox-move-first" />
                <dl class="dl-facts t-body-2">
                  <dt>Decision asked</dt><dd>{decisionLabel(it.decision) || 'A decision'}</dd>
                  <dt>Who may answer</dt><dd>{it.owners?.length ? it.owners.join(', ') : 'Any person'}</dd>
                  {#if it.consumers?.length}
                    <dt>Affected services</dt>
                    <dd>{#each it.consumers as c, i}{#if i > 0}, {/if}<a href={serviceUrl(c)}>{c}</a>{/each}</dd>
                  {/if}
                </dl>
              </li>
            {/each}
          </ul>
        {/if}
      </section>

      {#if frozen.length}
        <section aria-labelledby="inbox-frozen">
          <h2 id="inbox-frozen" class="t-section-title">Paused environments</h2>
          <ul class="pl-list" data-testid="inbox-frozen">
            {#each frozen as row (row.namespace + '/' + row.stage)}
              <li class="pl-row dl-row">
                <a class="dl-title" href={deliveryHistoryUrl({ namespace: row.namespace, stage: row.stage })}>{row.service} in {row.environment}</a>
                <p class="t-body-2">Paused{#if row.frozenReason}: {row.frozenReason}{/if}. Nothing moves here until someone unpauses it.</p>
              </li>
            {/each}
          </ul>
        </section>
      {/if}

      {#if errored.length}
        <section aria-labelledby="inbox-errored">
          <h2 id="inbox-errored" class="t-section-title">Changes that could not finish</h2>
          <ul class="pl-list" data-testid="inbox-errored">
            {#each errored as it (it.namespace + '/' + it.name)}
              <li class="pl-row dl-row">
                <a class="dl-title" href={deliveryChangeUrl(it.namespace, it.name)}>{typeLabel(it.type)} on {it.namespace}/{it.stage}</a>
                <p class="t-body-2">{it.reason || 'Pacto could not tell how it ended'}. The environment is still paused.</p>
              </li>
            {/each}
          </ul>
        </section>
      {/if}
    {/if}
  {/if}
</div>

<style>
  .dl-row { display: flex; flex-direction: column; gap: var(--sp-1); padding: var(--sp-3); }
  .dl-title { font-weight: 600; color: var(--c-accent); }
  .dl-question { margin: 0; }
  .dl-facts { display: grid; grid-template-columns: max-content 1fr; gap: 2px var(--sp-3); margin: 0; }
  .dl-facts dt { color: var(--c-text-3); }
  .dl-facts dd { margin: 0; }
  .dl-facts a { text-decoration: underline; }
  .dl-none { color: var(--c-text-3); }
  .state-box { display: flex; flex-direction: column; align-items: center; gap: var(--sp-2); padding: var(--sp-8) var(--sp-4); text-align: center; color: var(--c-text-3); }
  .state-box h2 { color: var(--c-text-2); }
</style>
