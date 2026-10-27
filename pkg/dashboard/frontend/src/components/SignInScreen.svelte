<script>
  import { api } from '../lib/api.ts';
  import { deliverySession } from '../lib/deliverySession.svelte.ts';
  import { loginUrl } from '../lib/delivery.ts';
  import { fleetOverviewUrl } from '../lib/router.ts';

  // The one screen a dashboard people sign in to shows before they do: the
  // product name and the button. Signing in comes back to the page asked for.
  // Where token sign-in is on, a field takes a Kubernetes bearer token too.
  let hash = $state(location.hash);
  const href = $derived(loginUrl(hash || fleetOverviewUrl()));
  const s = $derived(deliverySession.data);
  let token = $state('');
  let busy = $state(false);
  let error = $state('');

  async function signInWithToken(e) {
    e.preventDefault();
    busy = true;
    error = '';
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
  {#if s?.login !== false}<a class="btn btn-primary" {href}>Sign in</a>{/if}
  {#if s?.tokenLogin}
    <form class="signin-token" onsubmit={signInWithToken} data-testid="token-sign-in">
      <label for="signin-token">Kubernetes token</label>
      <input id="signin-token" class="input" type="password" autocomplete="off" spellcheck="false" bind:value={token} required />
      <button type="submit" class="btn" disabled={busy || !token.trim()}>Sign in with a token</button>
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
  .signin-err { color: var(--c-err); margin: 0; }
</style>
