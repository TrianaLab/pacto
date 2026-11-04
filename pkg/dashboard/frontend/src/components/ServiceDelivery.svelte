<script>
  import { onDestroy } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { deliveryHistoryUrl, deliveryEnvironmentsUrl } from '../lib/router.ts';
  import { shortDigest, sentence } from '../lib/delivery.ts';
  import PreviewSection from './PreviewSection.svelte';
  import DeliveryBadges from './DeliveryBadges.svelte';
  import DeliveryProblem from './DeliveryProblem.svelte';
  import DeliveryUnavailable from './DeliveryUnavailable.svelte';

  // A service's Stages on its page, with the same badges as the Delivery board.
  // `state` is whether this dashboard serves delivery: only 'enabled' asks.
  // `domain` is the service's registry domain: same-named services in two
  // domains are two services, and a Stage naming one is not the other's.
  let { service = '', domain = '', state = 'unknown', refreshTick = 0 } = $props();

  // Paging counts services, so one service is one page. The exact `service`
  // and `domain` filter already returns only its Stages; the row guard keeps it
  // that way. A Stage that names no domain matches any; when this service has
  // a domain, its row says the match is by name only.
  const NO_DOMAIN = 'matched by service name only, so it may be a same-named service in another domain';
  const tag = $derived(`${domain}/${service}`);
  const loader = createProductLoader(() => api.deliveryEnvironments({ service, domain, limit: 1 }));
  $effect(() => { if (state === 'enabled') loader.sync(`${tag}@@${refreshTick}`, tag); });
  onDestroy(() => loader.destroy());

  // Rows on hand stay while a refresh of the same service is in flight.
  const data = $derived(loader.dataTag === tag ? loader.data : null);
  const rows = $derived((data?.environments ?? []).filter((r) => r.service === service && (!r.domain || r.domain === domain)));
</script>

{#snippet links()}
  {#if state === 'enabled'}
    <p class="t-body-2 sd-links">
      <a href={deliveryHistoryUrl({ service, domain })}>Delivery history</a>
      <a href={deliveryEnvironmentsUrl({ service, domain })}>Open in Delivery</a>
    </p>
  {/if}
{/snippet}

{#snippet emptyBody()}
  {#if state === 'unknown'}
    <p class="t-body-2">Checking whether this dashboard serves delivery.</p>
  {:else if state !== 'enabled'}
    <DeliveryUnavailable reason={state} />
  {:else if loader.error}
    <DeliveryProblem error={loader.error} onRetry={() => loader.refresh()} />
  {:else if !data}
    <p class="t-body-2">Loading delivery</p>
  {:else}
    <p class="t-body-2">No Stages for this service. Delivery is opt-in per service.</p>
  {/if}
  {@render links()}
{/snippet}

<PreviewSection title="Delivery" count={rows.length} total={rows.length} {emptyBody}
  help="The Stages of this service: the version each environment runs and whether it is verified, failed, frozen or waiting.">
  {#each rows as r (r.namespace + '/' + r.stage)}
    <div class="sd-row" data-testid="service-delivery-row">
      <span class="sd-env">{r.environment}</span>
      <a href={deliveryHistoryUrl({ namespace: r.namespace, stage: r.stage })} title={r.pointer || 'nothing deployed yet'}>{r.version || shortDigest(r.pointer) || 'Nothing yet'}</a>
      <DeliveryBadges row={r} />
      {#if r.frozen && r.frozenReason}<span class="t-meta">Frozen: {sentence(r.frozenReason)}</span>{/if}
      {#if domain && !r.domain}<span class="t-meta sd-nodomain" data-testid="service-delivery-nodomain" title={NO_DOMAIN}>Stage names no domain<span class="visually-hidden">: {NO_DOMAIN}</span></span>{/if}
      {#if r.candidate || r.candidateVersion}<span class="t-meta">Candidate {r.candidateVersion || shortDigest(r.candidate)}</span>{/if}
    </div>
  {/each}
  {#if loader.error}<DeliveryProblem error={loader.error} onRetry={() => loader.refresh()} />{/if}
  {@render links()}
</PreviewSection>

<style>
  .sd-row { display: flex; align-items: baseline; gap: var(--sp-2); flex-wrap: wrap; padding: var(--sp-1) 0; }
  .sd-env { min-width: 8rem; font-weight: 600; }
  .sd-nodomain { border: 1px solid var(--c-border); border-radius: var(--radius-sm); padding: 0 var(--sp-1); }
  .sd-links { display: flex; gap: var(--sp-3); margin: var(--sp-2) 0 0; }
</style>
