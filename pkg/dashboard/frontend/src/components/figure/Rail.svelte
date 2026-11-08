<script>
  // Rail (SPEC 3): a track of stations in causal order. Each station is a Node with its
  // name and one line of meaning under it (HTML, wrapping, never cut). A station the
  // subject did not reach is dimmed and drawn at rest (the caller dashes its wire). The
  // token, when there is one, stands in its slot above the station where the subject is
  // now, and that station says so in words in the token's tone. Figure draws the wires
  // between the anchors `f0` to `f{n-1}` and walks the token through the slots `tk-{i}`.
  import Node from './Node.svelte';
  import Token from './Token.svelte';

  /** @typedef {{ key: string, label: string, sub: string, tone: string, hatch?: boolean, glyph?: any, shape?: 'circle' | 'square' | 'diamond', reached: boolean, here?: string }} RailStation */
  /** @type {{ stations: RailStation[], token?: { tone: string, at: number } | null }} */
  let { stations, token = null } = $props();
  const BADGE = { err: 'badge-err', warn: 'badge-warn', ok: 'badge-ok', frost: 'badge-info', accent: 'badge-accent', neutral: 'badge-neutral' };
</script>

<ol class="rail" class:has-tok={!!token} style:--n={stations.length}>
  {#each stations as s, i (s.key)}
    <li class="rl-st" class:rl-off={!s.reached}>
      {#if token}<span class="rl-slot" data-tok={i}>{#if token.at === i}<Token tone={token.tone} />{/if}</span>{/if}
      <Node tone={s.tone} hatch={s.hatch} glyph={s.glyph ?? ''} shape={s.shape ?? 'circle'} big a="f{i}" m="f{i}" />
      <span class="rl-l">
        <b><bdi>{s.label}</bdi></b>
        <span class="t-meta">{s.sub}</span>
        {#if s.here}<span class="badge {BADGE[token?.tone ?? 'accent']} here">{s.here}</span>{/if}
      </span>
    </li>
  {/each}
</ol>
