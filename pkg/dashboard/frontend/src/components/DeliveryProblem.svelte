<script>
  import EmptyState from './EmptyState.svelte';
  import { loginUrl } from '../lib/delivery.ts';

  // Why a delivery read gave nothing, said as what it is: not signed in, not
  // installed or a failure. Never an empty list.
  let { error, onRetry = null } = $props();

  const status = $derived(error?.status ?? 0);
  const code = $derived(error?.code ?? '');
  const message = $derived(error?.message || String(error));
</script>

{#if status === 401}
  <div class="state-box" data-testid="delivery-signin">
    <h2>Sign in to see delivery</h2>
    <p>Everything here runs as you, so the dashboard needs to know who you are first.</p>
    <a class="btn btn-primary" href={loginUrl(location.hash)}>Sign in</a>
  </div>
{:else if code === 'DeliveryNotInstalled'}
  <EmptyState title="Delivery is not installed" message={message} />
{:else if status === 403}
  <EmptyState error={true} title="The cluster refused" message={message} {onRetry} />
{:else}
  <EmptyState error={true} title="Can’t read delivery" message={message} {onRetry} />
{/if}

<style>
  .state-box {
    display: flex; flex-direction: column; align-items: center; gap: var(--sp-2);
    padding: var(--sp-8) var(--sp-4); text-align: center; color: var(--c-text-3);
  }
  .state-box h2 { color: var(--c-text-2); }
</style>
