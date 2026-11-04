<script>
  import IdentityBadge from './IdentityBadge.svelte';
  import { abbreviateDigests, formatCount } from '../lib/format.ts';
  import { formatDate } from '../lib/dateFormat.ts';
  import { signatureState } from '../lib/distributions.ts';

  // The bundle signature check and the OCI referrers of one revision, as the source
  // that read it from a registry reported them. `bundle` is null when no source that
  // read the revision checks signatures. The signer is always the identity the
  // contract's own publish.sign declares, so "verified" never renders without it.
  // An unverified signature names only the expected identity: who signed it is unknown.
  let { bundle = null } = $props();
  const sig = $derived(bundle?.signature ?? null);
  const st = $derived(signatureState(sig));
  const refs = $derived(bundle?.referrers ?? { items: [], count: 0, total: 0 });
  const reasonTone = $derived(st.value === 'unverified' ? 'err' : st.value === 'unsigned' ? 'warn' : 'neutral');
</script>

<div class="bp" data-testid="bundle-provenance">
  {#if !bundle}
    <p class="bp-note">Not checked. No source that read this revision checks signatures; Pacto checks them when it reads a revision from a registry. If a registry source could not reach the registry, its limitation is listed on this page.</p>
  {:else}
    <div class="bp-row">
      <span class="bp-k">Signature</span>
      <IdentityBadge label={st.label} tone={st.tone} />
      {#if st.value === 'verified'}<span>Signed by the contract's publish.sign identity</span>{/if}
    </div>
    {#if sig?.signer}<div class="bp-row"><span class="bp-k">Signer</span><span class="bp-v">{sig.signer}</span></div>{/if}
    {#if sig?.issuer}<div class="bp-row"><span class="bp-k">Issuer</span><span class="bp-v">{sig.issuer}</span></div>{/if}
    {#if sig?.expectedSigner}<div class="bp-row"><span class="bp-k">Expected signer</span><span class="bp-v">{sig.expectedSigner}</span></div>{/if}
    {#if sig?.expectedIssuer}<div class="bp-row"><span class="bp-k">Expected issuer</span><span class="bp-v">{sig.expectedIssuer}</span></div>{/if}
    <!-- A count Pacto never took is not zero. -->
    <div class="bp-row"><span class="bp-k">Signature bundles</span><span>{bundle.referrersUnavailable ? 'Unknown' : formatCount(sig?.bundles ?? 0)}</span></div>
    {#if sig?.reason}<p class="bp-reason tone-{reasonTone}" data-testid="bundle-reason">{sig.reason}</p>{/if}
    {#if bundle.referrersUnavailable}<p class="bp-note">Referrers unavailable: {bundle.referrersUnavailable}</p>{/if}
    {#if (refs.items ?? []).length}
      <table class="bp-kv">
        <thead><tr><th scope="col">Artifact type</th><th scope="col">Digest</th><th scope="col">Created</th></tr></thead>
        <tbody>
          {#each refs.items as r, i (`${r.digest}:${i}`)}
            <tr>
              <td>{r.artifactType}</td>
              <td title={r.digest}>{abbreviateDigests(r.digest)}</td>
              <td>{r.created ? formatDate(r.created) : 'Created unknown'}</td>
            </tr>
          {/each}
        </tbody>
      </table>
      {#if refs.truncated}<p class="bp-note">Showing {formatCount(refs.count)} of {formatCount(refs.total)} referrers.</p>{/if}
    {:else if !bundle.referrersUnavailable}
      <p class="bp-note">No referrers are attached to this revision.</p>
    {/if}
    <div class="bp-row"><span class="bp-k">Checked by</span><span>{bundle.source}</span>{#if bundle.checkedAt}<span class="bp-note">{formatDate(bundle.checkedAt)}</span>{/if}</div>
  {/if}
</div>

<style>
  .bp { display: flex; flex-direction: column; gap: var(--sp-2); }
  .bp-row { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; min-width: 0; }
  .bp-k { color: var(--c-text-3); font: var(--role-label-font); font-variant-caps: var(--role-label-caps); letter-spacing: var(--role-label-tracking); }
  .bp-v { overflow-wrap: anywhere; min-width: 0; }
  .bp-note { margin: 0; font-size: var(--text-sm); color: var(--c-text-3); overflow-wrap: anywhere; }
  .bp-reason { margin: 0; font-size: var(--text-sm); overflow-wrap: anywhere; padding-left: var(--sp-2); border-left: 3px solid var(--tone-c, var(--c-neutral)); color: var(--c-text-2); }
  .tone-err { --tone-c: var(--c-err); }
  .tone-warn { --tone-c: var(--c-warn); }
  .tone-neutral { --tone-c: var(--c-neutral); }
  .bp-kv { width: 100%; border-collapse: collapse; font-size: var(--text-sm); }
  .bp-kv th { text-align: left; color: var(--c-text-3); padding: var(--sp-1) var(--sp-2) var(--sp-1) 0; font: var(--role-label-font); font-variant-caps: var(--role-label-caps); letter-spacing: var(--role-label-tracking); }
  .bp-kv td { padding: var(--sp-1) var(--sp-2) var(--sp-1) 0; color: var(--c-text-2); border-top: 1px solid var(--c-border); overflow-wrap: anywhere; vertical-align: top; }
</style>
