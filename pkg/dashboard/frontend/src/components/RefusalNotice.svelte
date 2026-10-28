<script>
  import { loginUrl } from '../lib/session.svelte.ts';

  // A refused action, said plainly: the dashboard's own words for the refusals
  // it knows, then the reason exactly as the server or the apiserver gave it.
  // A refusal the dashboard can name keeps the server's words one click away;
  // one it cannot name shows them, since they are all the reader has.
  let { error } = $props();

  const HEADLINES = {
    NotYourTeam: 'Only the team the question names may answer it.',
    NoOwnerTeam: 'No team is declared, so only break-glass can override.',
    OwnChange: 'Someone else must decide on a change you asked for.',
    AgentsMayNotApprove: 'Agents may not decide on held changes.',
    AuthTimeUnavailable: 'Nobody can approve on this cluster yet.',
    ApproveFromCLI: 'Decide from the command line on your own machine.',
    LoginNotConfigured: 'This dashboard has no sign-in, so it takes no decisions.',
    LoginRequired: 'Sign in first.',
    FreshLoginRequired: 'Deciding needs a fresh sign-in.',
    OverrideFromCLI: 'An override is break-glass and stays on the command line.',
    DeliveryNotInstalled: 'Delivery is not installed on this cluster.',
    ClusterUnreachable: 'The dashboard cannot reach the cluster right now.',
    PlanStale: 'Something changed since the plan.',
  };

  const code = $derived(error?.code || '');
  const known = $derived(!!HEADLINES[code]);
  const headline = $derived(HEADLINES[code] || (error?.status === 403 ? 'The cluster refused.' : 'That did not work.'));
  const detail = $derived(error?.message || String(error));
</script>

<div class="rn" role="alert" data-testid="refusal" data-code={code}>
  <p class="rn-head"><strong>{headline}</strong></p>
  {#if known}
    <details class="disclosure rn-more">
      <summary><span class="disclosure-caret" data-motion aria-hidden="true">&#9656;</span>Details</summary>
      <p class="rn-detail t-body-2">{detail}</p>
    </details>
  {:else}
    <p class="rn-detail t-body-2">{detail}</p>
  {/if}
  {#if code === 'LoginRequired'}<a class="btn btn-sm" href={loginUrl(location.hash)}>Sign in</a>{/if}
</div>

<style>
  .rn { border: 1px solid var(--c-err-border); background: var(--c-err-bg); color: var(--c-err); border-radius: var(--radius-sm); padding: var(--sp-2) var(--sp-3); display: flex; flex-direction: column; gap: var(--sp-1); }
  .rn p { margin: 0; }
  .rn-detail { overflow-wrap: anywhere; }
  .rn-more > summary { min-height: 0; color: inherit; }
</style>
