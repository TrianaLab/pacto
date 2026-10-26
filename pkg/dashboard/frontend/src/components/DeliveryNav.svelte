<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.ts';
  import { deliverySession, loadSession } from '../lib/deliverySession.svelte.ts';
  import { deliveryInboxUrl, deliveryEnvironmentsUrl, deliveryAutonomyUrl, deliveryHistoryUrl, deliveryPublishUrl } from '../lib/router.ts';
  import { loginUrl, signIn, hostSignsIn } from '../lib/delivery.ts';
  import Person from './Person.svelte';
  import CappedList from './CappedList.svelte';

  // The delivery section's own navigation and who the views act as. Every
  // delivery view renders it first, so a person always sees whose identity an
  // action would run under before they take one.
  let { current = '' } = $props();

  const tabs = [
    { view: 'delivery-inbox', label: 'Inbox', href: deliveryInboxUrl() },
    { view: 'delivery-environments', label: 'Environments', href: deliveryEnvironmentsUrl() },
    { view: 'delivery-autonomy', label: 'Autonomy', href: deliveryAutonomyUrl() },
    { view: 'delivery-history', label: 'History', href: deliveryHistoryUrl() },
    { view: 'delivery-publish', label: 'Published', href: deliveryPublishUrl() },
  ];

  onMount(() => { loadSession(); });

  const s = $derived(deliverySession.data);
  const caller = $derived(s?.caller);
  // The name the identity provider gave is a claim the person may have set
  // themselves, so Person prints the cluster's username next to it.
  const me = $derived({ user: caller?.user, name: s?.name });
  let signOutError = $state('');

  async function signOut() {
    signOutError = '';
    // A host that signs people in itself (the browser demo) switches who you
    // are instead: there is no session to end.
    if (hostSignsIn()) { signIn(location.hash || deliveryInboxUrl()); return; }
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
    <span>Acting as <strong>{#if caller?.user}<Person user={caller.user} {me} />{:else}your kubeconfig user{/if}</strong> from this machine. Decisions on held changes are taken with <code>pacto delivery approve</code>.</span>
  {:else if s?.signedIn}
    <span>
      {#if caller?.user}
        Signed in as <strong><Person user={caller.user} {me} /></strong>{#if caller.groups?.length}{', in '}<CappedList items={caller.groups} max={3} inline label="Your groups" testid="identity-groups">{#snippet item(g)}{g}{/snippet}</CappedList>{/if}.
      {:else}
        <!-- The ID token's name alone is self-asserted, so it is never shown as who you are. -->
        Signed in. <span class="dn-err">The cluster did not say who you are{#if s.callerError}: {s.callerError}{:else}.{/if}</span>
      {/if}
    </span>
    <button type="button" class="btn btn-sm" onclick={signOut}>{hostSignsIn() ? 'Switch person' : 'Sign out'}</button>
  {:else}
    <span>You are not signed in, so delivery shows nothing and offers no actions.</span>
    <a class="btn btn-sm btn-primary" href={loginUrl(location.hash || deliveryInboxUrl())}>Sign in</a>
  {/if}
  {#if signOutError}<span class="dn-err" role="alert">{signOutError}</span>{/if}
</div>

<style>
  /* One row that scrolls on a phone, never a tab alone on a second line. */
  .dn-tabs { display: flex; gap: var(--sp-1); overflow-x: auto; scrollbar-width: thin; box-shadow: inset 0 -1px 0 var(--c-border); }
  .dn-tab {
    padding: var(--sp-2) var(--sp-3); color: var(--c-text-2); text-decoration: none;
    border-bottom: 2px solid transparent; font-size: var(--text-sm); font-weight: 500;
    white-space: nowrap; flex-shrink: 0;
  }
  .dn-tab:hover { color: var(--c-text); }
  .dn-tab:focus-visible { outline: 2px solid var(--c-accent); outline-offset: 2px; }
  .dn-tab[aria-current='page'] { color: var(--c-accent); border-bottom-color: var(--c-accent); }
  /* Who you are, then the button, on one line: the text takes what room there is and the
     button never wraps under a long list of groups. */
  .dn-who { display: flex; gap: var(--sp-3); align-items: center; color: var(--c-text-2); }
  .dn-who > span:first-child { flex: 1 1 auto; min-width: 0; }
  .dn-who > .btn { flex-shrink: 0; }
  .dn-err { color: var(--c-err); }
</style>
