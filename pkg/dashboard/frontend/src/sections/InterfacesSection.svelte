<script>
  import ShowAll from '../components/ShowAll.svelte';
  import { cap } from '../lib/cap.svelte.ts';
  import CollapsibleSection from '../CollapsibleSection.svelte';
  import SectionState from './SectionState.svelte';
  import { methodClass } from '../lib/format.ts';
  import IdentityBadge from '../components/IdentityBadge.svelte';
  import LifecycleBadge from '../components/LifecycleBadge.svelte';
  import GuaranteeTerms from '../components/GuaranteeTerms.svelte';
  import { lifecycleText } from '../lib/behavior.ts';

  let { interfaces = [], open = $bindable(true), id = '' } = $props();

  let expanded = $state({});

  function toggle(i) {
    expanded = { ...expanded, [i]: !expanded[i] };
  }

  function hasDetails(iface) {
    return iface.endpoints?.length > 0 || iface.contractContent;
  }
  // Eight hundred endpoints show the first fifty; one cap for every interface on the page.
  const eps = cap(50);
</script>

{#if interfaces?.length > 0}
  <CollapsibleSection title="Interfaces" count={interfaces.length} bind:open {id}>
    {#each interfaces as iface, i}
      <div class="detail-card">
        <button type="button" class="detail-card-header" class:expandable={hasDetails(iface)} onclick={() => hasDetails(iface) && toggle(i)}>
          <div class="detail-card-header-left">
            {#if hasDetails(iface)}
              <span class="expand-icon" data-motion class:open={expanded[i]}>
                <svg viewBox="0 0 12 12" fill="none"><path d="M3 4.5L6 7.5L9 4.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>
              </span>
            {/if}
            <span class="pill pill-type">{iface.type}</span>
            <span class="detail-card-title">{iface.name}</span>
            {#if iface.port != null}<span class="pill">:{iface.port}</span>{/if}
            {#if iface.visibility}<span class="pill">{iface.visibility}</span>{/if}
            <LifecycleBadge lifecycle={iface.lifecycle} detail={false} />
            {#if iface.compatibilityEpoch}<IdentityBadge label={`Epoch ${iface.compatibilityEpoch}`} tone="neutral" title="Raised each time this interface's behavior changes incompatibly while its spec stays the same." describe />{/if}
          </div>
          {#if iface.hasContractFile}
            <span class="detail-card-sub" title={iface.contractFile || undefined}>{iface.contractFile || 'has contract'}</span>
          {/if}
        </button>

        {#if iface.lifecycle || iface.guarantees}
          <div class="iface-behavior">
            {#if iface.lifecycle}<p class="iface-lifecycle">{lifecycleText(iface.lifecycle)}</p>{/if}
            {#if iface.guarantees}<GuaranteeTerms guarantees={iface.guarantees} label={`Guarantees of ${iface.name}`} />{/if}
          </div>
        {/if}

        {#if expanded[i] && hasDetails(iface)}
          <div class="detail-card-body">
            {#if iface.endpoints?.length > 0}
              <table class="detail-card-table">
                <thead><tr><th>Method</th><th>Path</th><th>Summary</th></tr></thead>
                <tbody>
                  {#each eps.slice(iface.endpoints) as ep, ei (ei)}
                    <tr>
                      <td><span class="badge {methodClass(ep.method)}">{ep.method}</span></td>
                      <td><code>{ep.path}</code></td>
                      <td class="text-2">{ep.summary || ''}</td>
                    </tr>
                  {/each}
                </tbody>
              </table>
              <ShowAll cap={eps} total={iface.endpoints.length} noun="endpoints" />
            {/if}
            {#if iface.contractContent}
              <details class="contract-content">
                <summary>Raw contract</summary>
                <pre>{iface.contractContent}</pre>
              </details>
            {/if}
          </div>
        {/if}
      </div>
    {/each}
  </CollapsibleSection>
{:else}
  <SectionState title="Interfaces" bind:open {id} />
{/if}

<style>
  .detail-card-header-left { flex-wrap: wrap; flex: 1 1 16rem; }
  .pill-type { background: var(--c-info-bg, var(--c-accent-bg)); color: var(--c-info, var(--c-accent)); font-size: var(--text-xs); flex-shrink: 0; }
  /* The contract file ellipsizes rather than pushing the interface name to one word per line. */
  .detail-card-sub { max-width: 40%; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .text-2 { color: var(--c-text-2); }
  .iface-behavior { display: flex; flex-direction: column; gap: var(--sp-1); padding: 0 var(--sp-3) var(--sp-3); min-width: 0; }
  .iface-lifecycle { margin: 0; font-size: var(--text-sm); color: var(--c-text-2); overflow-wrap: anywhere; }
  .contract-content { margin-top: var(--sp-2); }
  .contract-content summary { cursor: pointer; color: var(--c-text-3); font-size: var(--text-sm); }
</style>
