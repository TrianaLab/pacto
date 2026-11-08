<script>
  // HubNode (SPEC 3, r5): the one thing a figure flows into or out of, a flat 44px
  // rounded square with the subject's kind glyph. Accent when it is the subject's
  // identity (this offering, the snapshot), neutral when it is a population; never a
  // status tone (S10). Its label sits beside or under it, never on it. Wires attach to
  // its box (`a`).
  import { KIND_GLYPH } from '../../lib/figure.ts';

  /** @type {{ tone?: 'accent' | 'neutral', kind: keyof typeof KIND_GLYPH, a: string, m?: string }} */
  let { tone = 'accent', kind, a, m = '' } = $props();
</script>

<span class="hub mk t-{tone}" data-a={a} data-m={m || undefined} aria-hidden="true">
  <svg width="44" height="44" viewBox="0 0 44 44" focusable="false">
    <rect class="hub-f" x="1" y="1" width="42" height="42" rx="9" />
    <path class="hub-g" transform="translate(10 10)" d={KIND_GLYPH[kind]} />
  </svg>
</span>
