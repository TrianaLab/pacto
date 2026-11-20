<script>
  import { api } from '../lib/api.ts';
  import CopyableIdentifier from './CopyableIdentifier.svelte';
  import ProvenanceMark from './ProvenanceMark.svelte';

  // One action row inside the Inferred block: "Accept all proposed ranges for <service>".
  // It asks the server for the plan pacto accept would sign and shows it with the exact
  // command a person runs to sign it. The dashboard never signs and never writes: the
  // command is the action. Acceptances already attached that do not count (waiting for
  // the owner, or signed by CI or an agent) are one muted line each, with a mark.
  let { serviceKey = '', service = '' } = $props();

  let answer = $state(null);
  let error = $state('');
  let loading = $state(false);
  let open = $state(false);

  async function plan() {
    open = true;
    if (loading) return;
    loading = true;
    error = '';
    try {
      answer = await api.fleetAccept(serviceKey);
    } catch (e) {
      answer = null;
      error = e?.status === 404
        ? 'This dashboard does not plan acceptances: run pacto accept <bundle repository> --proposed --dry-run.'
        : (e?.message || 'Pacto could not plan the acceptance.');
    } finally {
      loading = false;
    }
  }

  const waiting = $derived((answer?.plan?.existing ?? []).filter((a) => a.state !== 'counted'));
  const stateWords = { routed: 'waiting for the owner', proposal: 'a proposal, signed by a non-person', expired: 'expired', ignored: 'ignored' };
  const markText = (a) => `${stateWords[a.state] ?? a.state}. Signed by ${a.by || 'nobody named'}${a.issuer ? ` (${a.issuer})` : ''}.`
    + (a.record ? ` Record: ${a.record}.` : '') + (a.reason ? ` ${a.reason}` : '');
</script>

<div class="apr t-meta" data-testid="accept-proposed">
  <button type="button" class="apr-action" onclick={plan} aria-expanded={open}>
    Accept all proposed ranges for <bdi>{service}</bdi>
  </button>
  {#if open}
    <details class="apr-plan disclosure" open data-testid="accept-plan">
      <summary><span class="disclosure-caret" data-motion aria-hidden="true"></span>{answer?.refusal ? 'Nothing to accept' : 'Plan'}</summary>
      {#if loading}
        <p class="apr-note">Reading the newest published version…</p>
      {:else if error}
        <p class="apr-note" role="alert">{error}</p>
      {:else if answer?.refusal}
        <p class="apr-text" data-testid="accept-refusal">{answer.refusal.message}</p>
        {#if answer.refusal.command}<CopyableIdentifier value={answer.refusal.command} label="command" />{/if}
      {:else if answer?.plan}
        <p class="apr-text">Signs one record for <bdi title={answer.plan.subject}>{answer.plan.subject}</bdi>. {answer.plan.whoMayAccept}</p>
        <ul class="apr-list">
          {#each answer.plan.ranges ?? [] as r (r.field)}
            <li><code><bdi>{r.field}</bdi></code> <bdi>{r.value}</bdi>{#if r.source}{" "}<span class="apr-src">from <bdi title={r.source}>{r.source}</bdi></span>{/if}</li>
          {/each}
        </ul>
        {#each waiting as a (a.record + a.state)}
          <p class="apr-waiting" data-testid="accept-waiting">
            <ProvenanceMark start label={`About ${a.state} acceptance`} text={markText(a)} />
            <span>{a.state === 'routed' ? 'Waiting for the owner' : 'Not counted'}: <bdi title={a.by}>{a.by}</bdi></span>
          </p>
        {/each}
        <p class="apr-text">Run this to sign it with your Sigstore login:</p>
        <CopyableIdentifier value={answer.command} label="command" />
      {/if}
    </details>
  {/if}
</div>

<style>
  .apr { display: flex; flex-direction: column; gap: var(--sp-2); margin-top: var(--sp-2); min-inline-size: 0; container-type: inline-size; }
  .apr-action { align-self: flex-start; max-inline-size: 100%; padding: 0; border: none; background: none; color: var(--c-accent); font: inherit; cursor: pointer; text-decoration: underline; overflow-wrap: anywhere; text-align: start; min-block-size: var(--touch-min); }
  @media (pointer: coarse) { .apr-action { min-block-size: 44px; } }
  .apr-plan > :not(summary) { margin-top: var(--sp-1); }
  .apr-text, .apr-note, .apr-waiting { margin: 0; overflow-wrap: anywhere; }
  .apr-note, .apr-waiting, .apr-src { color: var(--c-text-3); }
  .apr-waiting { display: flex; align-items: center; gap: var(--sp-2); min-block-size: var(--touch-min); }
  .apr-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-1); }
  .apr-list li { overflow-wrap: anywhere; min-inline-size: 0; }
  .apr-list code { font-size: var(--text-sm); }
</style>
