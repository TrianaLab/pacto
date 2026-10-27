<script>
  import { api } from '../lib/api.ts';
  import { deliverySession, loadSession } from '../lib/deliverySession.svelte.ts';
  import { fleetOverviewUrl } from '../lib/router.ts';
  import { loginUrl, signIn, hostSignsIn } from '../lib/delivery.ts';
  import Person from './Person.svelte';
  import CappedList from './CappedList.svelte';
  import HelpTip from './HelpTip.svelte';

  // Who every page runs as, under the header on every page: the signed-in
  // person and their groups (the identity their Kubernetes RBAC is checked
  // for), or the kubeconfig user of a dashboard on your own machine. A
  // dashboard that identifies nobody shows nothing here.
  const s = $derived(deliverySession.data);
  // The apiserver's answer when delivery could ask it, else the ID token as
  // the cluster maps it.
  const user = $derived(s?.caller?.user || s?.user || '');
  const groups = $derived(s?.caller?.groups?.length ? s.caller.groups : (s?.groups ?? []));
  // The name the identity provider gave is a claim the person may have set
  // themselves, so Person prints the cluster's username next to it.
  const me = $derived({ user, name: s?.name });
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

{#if s && (s.mode === 'local' || s.login)}
  <div class="who t-body-2" data-testid="identity">
    {#if s.mode === 'local'}
      <span>Acting as <strong>{#if user}<Person {user} {me} />{:else}your kubeconfig user{/if}</strong> from this machine. <HelpTip label="deciding here" text="A dashboard on your own machine shows held changes. Decide them with pacto delivery approve." /></span>
    {:else if s.signedIn}
      <span>
        {#if user}
          Signed in as <strong><Person {user} {me} /></strong>{#if groups.length}{', in '}<CappedList items={groups} max={3} inline label="Your groups" testid="identity-groups">{#snippet item(g)}{g}{/snippet}</CappedList>{/if}.
        {:else}
          <!-- The ID token's name alone is self-asserted, so it is never shown as who you are. -->
          Signed in. <span class="who-err">The cluster did not say who you are{#if s.callerError}: {s.callerError}{:else}.{/if}</span>
        {/if}
      </span>
      <button type="button" class="btn btn-sm" data-testid="sign-out" onclick={signOut}>{hostSignsIn() ? 'Switch person' : 'Sign out'}</button>
    {:else}
      <span>You are not signed in.</span>
      <a class="btn btn-sm btn-primary" href={loginUrl(here())}>Sign in</a>
    {/if}
    {#if signOutError}<span class="who-err" role="alert">{signOutError}</span>{/if}
  </div>
{/if}

<style>
  /* Who you are, then the button, on one line: the text takes what room there is and the
     button never wraps under a long list of groups. */
  .who {
    display: flex; gap: var(--sp-3); align-items: center; color: var(--c-text-2);
    padding: var(--sp-1) var(--sp-8); border-bottom: 1px solid var(--c-border); background: var(--c-surface);
  }
  .who > span:first-child { flex: 1 1 auto; min-width: 0; }
  .who > .btn { flex-shrink: 0; }
  .who-err { color: var(--c-err); }
  @media (max-width: 48rem) { .who { padding: var(--sp-1) var(--sp-4); } }
</style>
