<script>
  // The state badges of one Stage, the same on the Delivery board and on a
  // service page. `heldHref` links the waiting count to where the held changes are.
  let { row: r, heldHref = '' } = $props();
</script>

<span class="ev-badges">
  {#if r.verified}<span class="badge badge-ok">Verified</span>{/if}
  {#if r.failed}<span class="badge badge-err">Failed</span>{/if}
  {#if r.frozen}<span class="badge badge-warn" title={r.frozenReason || 'Nothing moves here until a person unfreezes it'}>Frozen</span>{/if}
  {#if r.autoHold}<span class="badge badge-info" title="A person or agent rolled this environment back or promoted it back to a version it already verified, so automation does not promote here until a person unfreezes it or promotes a version it has not verified yet">Auto-hold</span>{/if}
  {#if r.held || r.heldUnknown}
    {@const held = r.heldUnknown ? '? waiting' : `${r.held} waiting`}
    {#if heldHref}<a class="badge badge-warn" href={heldHref} title={r.heldUnknown ? "The Stage's operations could not be read, so what waits is not known" : undefined}>{held}</a>
    {:else}<span class="badge badge-warn" title={r.heldUnknown ? "The Stage's operations could not be read, so what waits is not known" : undefined}>{held}</span>{/if}
  {/if}
  {#if r.pointer && !r.verified && !r.failed}<span class="badge badge-neutral">Not verified</span>{/if}
</span>

<style>
  .ev-badges { display: flex; gap: var(--sp-1); flex-wrap: wrap; }
</style>
