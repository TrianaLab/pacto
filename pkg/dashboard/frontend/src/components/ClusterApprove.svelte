<script>
  import CopyableIdentifier from './CopyableIdentifier.svelte';
  import { approveCommand, isDNSLabel } from '../lib/clusters.ts';

  // [Approve] on a pending member (514-S3): the dashboard never signs. It hands the
  // person the exact command, with the key they are to check and an optional display
  // name, and they sign it as themselves with pacto cluster approve.
  let { uid = '', keyID = '', name = '' } = $props();
  const panelId = $props.id();
  let open = $state(false);
  let override = $state('');
  const bad = $derived(!!override && !isDNSLabel(override));
  const command = $derived(approveCommand(uid, override, keyID));
</script>

<div class="ca" data-testid="cluster-approve">
  <button type="button" class="btn ca-btn" aria-expanded={open} aria-controls={panelId} onclick={() => (open = !open)}>
    Approve<span class="visually-hidden"> {name || uid}</span>
  </button>
  {#if open}
    <div class="ca-panel" id={panelId} data-testid="cluster-approve-panel">
      <p class="ca-text">Key <code class="ca-key"><bdi title={`sha256:${keyID}`}>sha256:{keyID}</bdi></code></p>
      <label class="ca-field">
        <span>Display name (optional)</span>
        <input class="input" type="text" bind:value={override} placeholder={name} spellcheck="false" autocomplete="off" aria-invalid={bad} aria-describedby={bad ? `${panelId}-bad` : undefined} />
      </label>
      {#if bad}<p class="ca-bad" id={`${panelId}-bad`}>A display name is a DNS label: lowercase letters, digits and hyphens, at most 63 characters.</p>{/if}
      <CopyableIdentifier value={command} label="command" />
      <p class="ca-text">Approving signs a record as you. Before you run it, check that the key matches the member's own snapshotKeyID (see Multi-cluster).</p>
    </div>
  {/if}
</div>

<style>
  .ca { display: flex; flex-direction: column; gap: var(--sp-2); min-inline-size: 0; inline-size: 100%; }
  .ca-btn { align-self: flex-start; min-block-size: var(--touch-min); }
  @media (pointer: coarse) { .ca-btn { min-block-size: 44px; } }
  .ca-panel { display: flex; flex-direction: column; gap: var(--sp-2); min-inline-size: 0; padding: var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); }
  .ca-text, .ca-bad { margin: 0; overflow-wrap: anywhere; font-size: var(--text-sm); }
  .ca-bad { color: var(--c-warn); }
  .ca-key { font-family: var(--font-mono); font-size: var(--text-xs); overflow-wrap: anywhere; }
  .ca-field { display: flex; flex-direction: column; gap: 2px; font-size: var(--text-xs); color: var(--c-text-3); min-inline-size: 0; }
  .ca-field .input { min-inline-size: 0; max-inline-size: 40ch; }
  @media (pointer: coarse) { .ca-field .input { min-block-size: 44px; } }
</style>
