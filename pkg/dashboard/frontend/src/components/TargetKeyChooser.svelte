<script>
  // A target key that does not name one target for certain (a 3-part key that names
  // targets in more than one cluster, one match while a source is incomplete, or a
  // stored key whose target has no known cluster) never resolves to a guess: the
  // server answers 409 with its reason and the candidates, and the reader chooses.
  // hrefFor builds each candidate's link, so the entity page and the graph each keep
  // their own destination.
  let { message = '', candidates = [], hrefFor } = $props();

  const sentence = $derived.by(() => {
    const m = message.trim();
    if (!m) return 'This target key does not name one target for certain. Choose one:';
    return m.charAt(0).toUpperCase() + m.slice(1) + (/[.:]$/.test(m) ? '' : '.');
  });
</script>

<section class="ev-choose" data-testid="target-key-chooser" aria-label="Targets this key names">
  <p class="t-body">{sentence}</p>
  <ul>
    {#each candidates as c (c.key)}
      <li><a href={hrefFor(c.key)} title={c.label}><span class="ev-choose-label">{c.label}</span> <bdi class="ev-choose-key">{c.key}</bdi></a></li>
    {/each}
  </ul>
</section>

<style>
  .ev-choose ul { list-style: none; margin: var(--sp-2) 0 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .ev-choose a { display: inline-flex; flex-wrap: wrap; align-items: baseline; gap: var(--sp-2); min-inline-size: 0; max-inline-size: 100%; }
  .ev-choose-label { overflow-wrap: anywhere; }
  .ev-choose-key { color: var(--c-text-3); font-family: var(--font-mono, monospace); font-size: var(--text-sm); overflow-wrap: anywhere; }
  @media (pointer: coarse) { .ev-choose a { min-block-size: 44px; align-items: center; } }
</style>
