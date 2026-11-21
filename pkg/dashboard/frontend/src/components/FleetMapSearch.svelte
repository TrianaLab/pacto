<script>
  /**
   * Find a service on the fleet map: the shared entity combobox over services,
   * each suggestion with its status glyph, domain and owner team and the typed
   * text marked. Arrows move, Enter focuses the service on the map, Esc closes
   * the list.
   */
  import EntityCombobox from './EntityCombobox.svelte';
  import { statusLabel, statusTone } from '../lib/format';

  // onfocus(serviceKey) focuses the chosen service on the map.
  let { onfocus = (_service) => {} } = $props();
  let value = $state('');

  const GLYPH = { err: '✕', warn: '▲', ok: '●', info: 'i', neutral: '○' };

  /** parts splits a name around the first match of the typed text. */
  function parts(name, q) {
    const i = q ? name.toLowerCase().indexOf(q.toLowerCase()) : -1;
    return i < 0 ? [name, '', ''] : [name.slice(0, i), name.slice(i, i + q.length), name.slice(i + q.length)];
  }
</script>

{#snippet option(r, q)}
  {@const [a, m, b] = parts(r.label, q)}
  {@const tone = statusTone(r.status)}
  {@const ctx = [r.businessDomain, r.owner].filter(Boolean).join(' · ')}
  <span class="fms-opt">
    <span class="fms-glyph fms-{tone}" title={statusLabel(r.status)} aria-hidden="true">{GLYPH[tone]}</span>
    <span class="visually-hidden">{statusLabel(r.status)}</span>
    <!-- Both ellipsize: the whole value is in the title. -->
    <bdi class="fms-name" title={r.label}>{a}{#if m}<mark>{m}</mark>{/if}{b}</bdi>
    <span class="fms-ctx" title={ctx || undefined}>{ctx}</span>
  </span>
{/snippet}

<div class="fms" data-testid="fleet-map-search" role="search">
  <EntityCombobox id="fleet-map-find" bind:value kinds={['service']} label="Find a service on the map" placeholder="Find a service" testid="fleet-map-find" {option}
    onselect={(r) => onfocus(r.key)} />
</div>

<style>
  .fms { min-inline-size: 12rem; max-inline-size: 20rem; flex: 1 1 14rem; }
  .fms-opt { display: inline-flex; align-items: baseline; gap: var(--sp-2); min-inline-size: 0; }
  .fms-glyph { flex: none; font-weight: 700; inline-size: 1em; text-align: center; }
  .fms-err { color: var(--c-err); }
  .fms-warn { color: var(--c-warn); }
  .fms-ok { color: var(--c-ok); }
  .fms-info { color: var(--c-info); }
  .fms-neutral { color: var(--c-text-3); }
  .fms-name { font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .fms-name mark { background: var(--c-accent-bg); color: inherit; border-radius: 2px; }
  .fms-ctx { color: var(--c-text-3); font-size: var(--text-xs); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
</style>
