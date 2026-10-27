<script>
  import { loginUrl } from '../lib/delivery.ts';
  import { fleetOverviewUrl } from '../lib/router.ts';

  // The one screen a dashboard people sign in to shows before they do: the
  // product name and the button. Signing in comes back to the page asked for.
  const href = loginUrl(location.hash || fleetOverviewUrl());
</script>

<div class="signin" data-testid="sign-in">
  <h1 class="signin-title">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" width="32" height="32" aria-hidden="true"><path d="M10 6 5 12 10 18"/><path d="M14 6 19 12 14 18"/><circle cx="12" cy="12" r="1.9" fill="currentColor" stroke="none"/></svg>
    Pacto
  </h1>
  <a class="btn btn-primary" {href}>Sign in</a>
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
</style>
