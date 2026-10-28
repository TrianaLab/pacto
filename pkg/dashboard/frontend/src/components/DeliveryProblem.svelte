<script>
  import EmptyState from './EmptyState.svelte';

  // Why a delivery read gave nothing, said as what it is: not installed, refused
  // or a failure. Never an empty list. A 401 is the sign-in gate's: it shows the
  // sign-in screen over every view.
  let { error, onRetry = null } = $props();

  const status = $derived(error?.status ?? 0);
  const code = $derived(error?.code ?? '');
  const message = $derived(error?.message || String(error));
</script>

{#if code === 'DeliveryNotInstalled'}
  <EmptyState title="Delivery is not installed" message={message} />
{:else if status === 403}
  <EmptyState error={true} title="The cluster refused" message={message} {onRetry} />
{:else}
  <EmptyState error={true} title="Can’t read delivery" message={message} {onRetry} />
{/if}
