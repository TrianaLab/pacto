<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.ts';
  import { typeLabel, versionLabel, sentence } from '../lib/delivery.ts';
  import ProposePanel from './ProposePanel.svelte';
  import RefusalNotice from './RefusalNotice.svelte';

  // How to undo one change, as a plan nobody has proposed: a rollback bound to
  // it while the environment still runs it, else a promote back to the version
  // it replaced, through every check. Pacto never proposes it on its own.
  let { namespace, stage, name, miraURL = '', onClose } = $props();

  let undo = $state(null);
  let error = $state(null);
  let planning = $state(false);
  let headingEl = $state(null);
  const request = $derived(undo?.kind === 'rollback'
    ? { namespace, stage, type: 'pacto.dev/rollback', undoes: name }
    : { namespace, stage, type: 'pacto.dev/promote', to: undo?.plan?.to });

  onMount(async () => {
    headingEl?.focus();
    try { undo = await api.deliveryUndo(namespace, name, stage); } catch (e) { error = e; }
  });
</script>

<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<section class="hp" aria-labelledby="up-{namespace}-{name}" data-testid="undo-panel"
  onkeydown={(e) => { if (e.key === 'Escape' && !e.defaultPrevented) { e.preventDefault(); onClose?.(); } }}>
  <h3 id="up-{namespace}-{name}" class="t-subsection-title" tabindex="-1" bind:this={headingEl}>Undo {name}</h3>
  {#if error}
    <RefusalNotice {error} />
  {:else if !undo}
    <p class="t-body-2" role="status">Working out how to undo it…</p>
  {:else}
    <p class="t-body" data-testid="undo-note">
      {#if undo.kind === 'none'}Nothing to undo.{:else}<strong>{typeLabel(request.type)}{#if request.to}{' '}to {versionLabel(request.to, undo.versions)}{/if}.</strong>{/if}
      {sentence(undo.note)}
    </p>
    {#if undo.kind !== 'none'}
      <p class="t-body-2">Nothing has been proposed. Plan it here, or run:</p>
      <pre class="hp-cmd"><code>{undo.command}</code></pre>
      {#if planning}
        <ProposePanel {request} toVersion={undo.versions?.[request.to] ?? ''} {miraURL} onClose={() => { planning = false; }} />
      {:else}
        <div><button type="button" class="btn btn-primary btn-sm" onclick={() => { planning = true; }}>Plan this {typeLabel(request.type).toLowerCase()}</button></div>
      {/if}
    {/if}
  {/if}
  <div><button type="button" class="btn btn-sm" onclick={() => onClose?.()}>Close</button></div>
</section>

<style>
  .hp { border: 1px solid var(--c-border); border-radius: var(--radius-md); padding: var(--sp-4); display: flex; flex-direction: column; gap: var(--sp-2); background: var(--c-surface); overflow-wrap: anywhere; }
  .hp h3:focus { outline: none; }
  .hp p { margin: 0; }
  .hp-cmd { margin: 0; white-space: pre-wrap; overflow-wrap: anywhere; font-size: var(--text-sm); }
</style>
