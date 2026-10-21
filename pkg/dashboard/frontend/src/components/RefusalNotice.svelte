<script>
  import { loginUrl } from '../lib/delivery.ts';

  // A refused action, said plainly: the dashboard's own words for the refusals
  // it knows, then the reason exactly as the server or the apiserver gave it.
  let { error } = $props();

  const HEADLINES = {
    NotYourTeam: 'Only the team the question names may answer it.',
    OwnChange: 'Someone else must decide on a change you asked for.',
    AgentsMayNotApprove: 'Agents may not decide on held changes.',
    AuthTimeUnavailable: 'Nobody can approve on this cluster yet.',
    ApproveFromCLI: 'A dashboard on your own machine does not decide.',
    LoginNotConfigured: 'This dashboard has no sign-in, so it takes no decisions.',
    LoginRequired: 'Sign in first.',
    FreshLoginRequired: 'Deciding needs a fresh sign-in.',
    OverrideFromCLI: 'An override is break-glass and stays on the command line.',
    DeliveryNotInstalled: 'Delivery is not installed on this cluster.',
    ClusterUnreachable: 'The dashboard cannot reach the cluster right now.',
    PlanStale: 'Something changed since the plan.',
  };

  const code = $derived(error?.code || '');
  const headline = $derived(HEADLINES[code] || (error?.status === 403 ? 'The cluster refused.' : 'That did not work.'));
</script>

<div class="rn" role="alert" data-testid="refusal" data-code={code}>
  <p class="rn-head"><strong>{headline}</strong></p>
  <p class="rn-detail t-body-2">{error?.message || String(error)}</p>
  {#if code === 'LoginRequired'}<a class="btn btn-sm" href={loginUrl(location.hash)}>Sign in</a>{/if}
</div>

<style>
  .rn { border: 1px solid var(--c-err-border); background: var(--c-err-bg); color: var(--c-err); border-radius: var(--radius-sm); padding: var(--sp-2) var(--sp-3); display: flex; flex-direction: column; gap: var(--sp-1); }
  .rn p { margin: 0; }
  .rn-detail { overflow-wrap: anywhere; }
</style>
