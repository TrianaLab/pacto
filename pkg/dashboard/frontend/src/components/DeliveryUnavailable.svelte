<script>
  // Why this dashboard shows no delivery: it has no signed-in user to sign a
  // delivery action, or the cluster it reads does not serve the delivery API.
  let { reason = 'not-served' } = $props();
</script>

{#if reason === 'no-user'}
  <p class="t-body" data-testid="delivery-unavailable-reason">Every delivery action is signed by a person, and this dashboard has no signed-in user. Delivery views appear in <code>pacto dashboard</code> on your own machine, or in a shared dashboard people sign in to.</p>
{:else}
  <p class="t-body" data-testid="delivery-unavailable-reason">The cluster this dashboard reads does not serve the delivery API. Install pacto-operator with <code>delivery.enabled=true</code>.</p>
{/if}
