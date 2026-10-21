<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.ts';
  import { deliverySession, loadSession } from '../lib/deliverySession.svelte.ts';
  import { deliveryInboxUrl, deliveryEnvironmentsUrl, deliveryHistoryUrl, deliveryPublishUrl } from '../lib/router.ts';
  import { loginUrl } from '../lib/delivery.ts';

  // The delivery section's own navigation and who the views act as. Every
  // delivery view renders it first, so a person always sees whose identity an
  // action would run under before they take one.
  let { current = '' } = $props();

  const tabs = [
    { view: 'delivery-inbox', label: 'Inbox', href: deliveryInboxUrl() },
    { view: 'delivery-environments', label: 'Environments', href: deliveryEnvironmentsUrl() },
    { view: 'delivery-history', label: 'History', href: deliveryHistoryUrl() },
    { view: 'delivery-publish', label: 'Published', href: deliveryPublishUrl() },
  ];

  onMount(() => { loadSession(); });

  const s = $derived(deliverySession.data);
  const caller = $derived(s?.caller);
  let signOutError = $state('');

  async function signOut() {
    signOutError = '';
    try {
      const next = await api.signOut();
      location.assign(/^https?:\/\//i.test(next) ? next : `/${deliveryInboxUrl()}`);
    } catch (e) {
      signOutError = `Signing out failed: ${e?.message || e}`;
    }
  }
</script>

<nav class="dn-tabs" aria-label="Delivery">
  {#each tabs as t (t.view)}
    <a href={t.href} class="dn-tab" aria-current={current === t.view ? 'page' : undefined}>{t.label}</a>
  {/each}
</nav>

<div class="dn-who t-body-2" data-testid="delivery-identity">
  {#if !deliverySession.loaded}
    <span>Checking who you are…</span>
  {:else if deliverySession.error}
    <span class="dn-err" role="alert">Could not read your session: {deliverySession.error?.message || deliverySession.error}</span>
  {:else if s?.mode === 'local'}
    <span>Acting as <strong>{caller?.user || 'your kubeconfig user'}</strong> from this machine. Decisions on held changes are taken with <code>pacto delivery approve</code>.</span>
  {:else if s?.signedIn}
    <span>
      Signed in as <strong>{caller?.user || 'unknown'}</strong>{#if caller?.groups?.length}, in {caller.groups.join(', ')}{/if}.
      {#if s.callerError}<span class="dn-err">The cluster did not say who you are: {s.callerError}</span>{/if}
    </span>
    <button type="button" class="btn btn-sm" onclick={signOut}>Sign out</button>
  {:else}
    <span>You are not signed in, so delivery shows nothing and offers no actions.</span>
    <a class="btn btn-sm btn-primary" href={loginUrl(location.hash || deliveryInboxUrl())}>Sign in</a>
  {/if}
  {#if signOutError}<span class="dn-err" role="alert">{signOutError}</span>{/if}
</div>

<style>
  .dn-tabs { display: flex; gap: var(--sp-1); flex-wrap: wrap; border-bottom: 1px solid var(--c-border); }
  .dn-tab {
    padding: var(--sp-2) var(--sp-3); color: var(--c-text-2); text-decoration: none;
    border-bottom: 2px solid transparent; margin-bottom: -1px; font-size: var(--text-sm); font-weight: 500;
  }
  .dn-tab:hover { color: var(--c-text); }
  .dn-tab:focus-visible { outline: 2px solid var(--c-accent); outline-offset: 2px; }
  .dn-tab[aria-current='page'] { color: var(--c-accent); border-bottom-color: var(--c-accent); }
  .dn-who { display: flex; gap: var(--sp-3); align-items: center; flex-wrap: wrap; color: var(--c-text-2); }
  .dn-err { color: var(--c-err); }
</style>
