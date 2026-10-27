<script>
  import { api } from '../lib/api.ts';
  import { deliverySession, loadSession } from '../lib/deliverySession.svelte.ts';
  import { fleetOverviewUrl } from '../lib/router.ts';
  import { loginUrl, signIn, hostSignsIn, personLabel } from '../lib/delivery.ts';
  import Person from './Person.svelte';
  import CappedList from './CappedList.svelte';
  import HelpTip from './HelpTip.svelte';

  // Who every page runs as, in the header on every page: the signed-in person
  // and their groups (the identity their Kubernetes RBAC is checked for), or
  // the kubeconfig user of a dashboard on your own machine. A dashboard that
  // identifies nobody shows nothing here. In the header it is a button with
  // the person's name that opens the details and Sign out; in the phone menu
  // (drawer) the details show in place.
  let { drawer = false } = $props();

  const s = $derived(deliverySession.data);
  // The apiserver's answer when delivery could ask it, else what the session
  // says the cluster calls the person.
  const user = $derived(s?.caller?.user || s?.user || '');
  const groups = $derived(s?.caller?.groups?.length ? s.caller.groups : (s?.groups ?? []));
  // The name the identity provider gave is a claim the person may have set
  // themselves, so Person prints the cluster's username next to it.
  const me = $derived({ user, name: s?.name });
  const label = $derived(s?.mode === 'local' ? (personLabel(user, null, me) || 'This machine')
    : s?.signedIn ? (personLabel(user, null, me) || 'Signed in') : 'Signed out');
  const here = () => location.hash || fleetOverviewUrl();
  let signOutError = $state('');

  async function signOut() {
    signOutError = '';
    // A host that signs people in itself (the browser demo) switches who you
    // are instead: there is no session to end.
    if (hostSignsIn()) { signIn(here()); return; }
    try {
      const next = await api.signOut();
      if (/^https?:\/\//i.test(next)) location.assign(next);
      else await loadSession();
    } catch (e) {
      signOutError = `Signing out failed: ${e?.message || e}`;
    }
  }
</script>

{#snippet details()}
  {#if s.mode === 'local'}
    <p>Acting as <strong>{#if user}<Person {user} {me} />{:else}your kubeconfig user{/if}</strong> from this machine. <HelpTip label="deciding here" text="A dashboard on your own machine shows held changes. Decide them with pacto delivery approve." /></p>
  {:else if s.signedIn}
    <p>
      {#if user}
        Signed in{#if s.method === 'token'}{' with a token'}{/if} as <strong><Person {user} {me} /></strong>{#if groups.length}{', in '}<CappedList items={groups} max={3} inline label="Your groups" testid="identity-groups">{#snippet item(g)}{g}{/snippet}</CappedList>{/if}.
      {:else}
        <!-- The ID token's name alone is self-asserted, so it is never shown as who you are. -->
        Signed in. <span class="who-err">The cluster did not say who you are{#if s.callerError}: {s.callerError}{:else}.{/if}</span>
      {/if}
    </p>
    <button type="button" class="btn btn-sm" data-testid="sign-out" onclick={signOut}>{hostSignsIn() ? 'Switch person' : 'Sign out'}</button>
  {:else}
    <p>You are not signed in.</p>
    {#if s.login}<a class="btn btn-sm btn-primary" href={loginUrl(here())}>Sign in</a>{/if}
  {/if}
  {#if signOutError}<p class="who-err" role="alert">{signOutError}</p>{/if}
{/snippet}

{#if s && (s.mode === 'local' || s.login || s.tokenLogin)}
  {#if drawer}
    <div class="who-panel t-body-2" data-testid="identity-drawer">{@render details()}</div>
  {:else}
    <div class="who" data-testid="identity">
      <button type="button" class="who-trigger" popovertarget="navbar-identity" data-testid="identity-trigger" aria-label="Who you are: {label}">
        <span class="who-avatar" aria-hidden="true">{label.slice(0, 1).toUpperCase()}</span><span class="who-name">{label}</span>
      </button>
      <div id="navbar-identity" class="who-pop t-body-2" popover>{@render details()}</div>
    </div>
  {/if}
{/if}

<style>
  .who-trigger {
    display: inline-flex; align-items: center; gap: var(--sp-2); min-height: 36px; max-width: 14rem;
    padding: 0 var(--sp-2); border: 1px solid var(--c-border); border-radius: var(--radius-sm);
    background: none; color: var(--c-text-2); font: inherit; font-size: var(--text-sm); font-weight: 500; cursor: pointer;
  }
  .who-trigger:hover { color: var(--c-text); background: var(--c-surface-hover); }
  .who-avatar {
    display: inline-grid; place-items: center; flex-shrink: 0; width: 22px; height: 22px; border-radius: 50%;
    background: var(--c-accent-bg); color: var(--c-accent); font-size: var(--text-xs); font-weight: 700;
  }
  .who-name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  /* Parked under the bar's right edge, like the data sources panel. */
  .who-pop {
    inset: auto; position: fixed; top: calc(var(--navbar-h) + var(--sp-1)); right: var(--sp-8);
    width: min(360px, calc(100vw - 2 * var(--sp-4))); margin: 0; padding: var(--sp-3) var(--sp-4);
    border: 1px solid var(--c-border); border-radius: var(--radius-md);
    background: var(--c-surface); color: var(--c-text); box-shadow: var(--shadow-md);
  }
  .who-pop p, .who-panel p { margin: 0 0 var(--sp-2); overflow-wrap: anywhere; }
  .who-panel { color: var(--c-text-2); margin-bottom: var(--sp-3); }
  .who-err { color: var(--c-err); }
  /* The tight bar keeps the avatar and leaves the name to the button's label. */
  @media (max-width: 1340px) { .who-name { display: none; } }
</style>
