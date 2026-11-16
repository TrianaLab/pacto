<script>
  import { api } from '../lib/api.ts';
  import { session, TOKEN_DOCS } from '../lib/session.svelte.ts';
  import SignInLink from './SignInLink.svelte';
  import { fleetOverviewUrl } from '../lib/router.ts';
  import { consumeSignInLink } from '../lib/signInLink.ts';

  // The one screen a dashboard people sign in to shows before they do: the
  // product name and the button, which names the identity provider (OIDC
  // unless the dashboard is told its name). Signing in comes back to the page asked for.
  // Where token sign-in is on, a field takes a Kubernetes bearer token too, and a
  // sign-in link from the chart's NOTES (#token=) signs in with the token it carried.
  let hash = $state(location.hash);
  const s = $derived(session.data);
  let token = $state('');
  let busy = $state(false);
  let error = $state('');
  // Read once: the link's token is handed over a single time.
  const link = consumeSignInLink();
  let linkExpired = $state(!!link?.expired);
  if (link && !link.expired) {
    busy = true;
    api.signInWithToken(link.token, { link: true })
      .then(() => { history.replaceState(null, '', location.pathname + fleetOverviewUrl()); location.reload(); })
      .catch((err) => { error = err?.message || String(err); busy = false; });
  }

  async function signInWithToken(e) {
    e.preventDefault();
    busy = true;
    error = '';
    linkExpired = false;
    try {
      await api.signInWithToken(token);
      location.reload();
    } catch (err) {
      error = err?.message || String(err);
      busy = false;
    }
  }
</script>

<svelte:window onhashchange={() => { hash = location.hash; }} />

<div class="signin" data-testid="sign-in">
  <h1 class="signin-title">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" width="32" height="32" aria-hidden="true"><path d="M10 6 5 12 10 18"/><path d="M14 6 19 12 14 18"/><circle cx="12" cy="12" r="1.9" fill="currentColor" stroke="none"/></svg>
    Pacto
  </h1>
  {#if s?.login !== false}<SignInLink hash={hash || fleetOverviewUrl()}>Sign in with {s?.loginName || 'OIDC'}</SignInLink>{/if}
  {#if s?.tokenLogin}
    <form class="signin-token" onsubmit={signInWithToken} data-testid="token-sign-in">
      <label for="signin-token">Kubernetes token</label>
      <input id="signin-token" class="input" type="password" autocomplete="off" spellcheck="false" bind:value={token} required aria-describedby="signin-token-hint" />
      <p id="signin-token-hint" class="t-body-2 signin-hint">For a ServiceAccount, run <code>kubectl create token &lt;service-account&gt; -n &lt;namespace&gt; --audience pacto-dashboard</code>. A ServiceAccount reads the fleet only. For yourself, use the token your cluster's provider gives kubectl. <a href={TOKEN_DOCS} target="_blank" rel="noopener noreferrer">How to get a token</a></p>
      <button type="submit" class="btn" disabled={busy || !token.trim()}>Sign in with a token</button>
      {#if linkExpired}<p class="signin-err" role="alert" data-testid="sign-in-link-expired">link expired, run the command again</p>{/if}
      {#if error}<p class="signin-err" role="alert">{error}</p>{/if}
    </form>
  {/if}
</div>

<style>
  .signin {
    min-height: 70vh; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: var(--sp-6);
  }
  .signin-title {
    display: flex; align-items: center; gap: var(--sp-3); margin: 0;
    font-family: var(--font-display); font-size: 2rem; font-weight: 700;
  }
  .signin-title svg { color: var(--c-accent); }
  .signin-token { display: flex; flex-direction: column; gap: var(--sp-2); width: min(24rem, 100%); }
  .signin-hint { margin: 0; color: var(--c-text-2); }
  .signin-hint code { overflow-wrap: anywhere; }
  .signin-err { color: var(--c-err); margin: 0; }
</style>
