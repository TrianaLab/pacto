<script>
  // Glyph (SPEC 3): the kind of a thing (service, revision, target, owner, domain,
  // offering, golden path, source, environment) as a 20px or 32px outline icon. It names
  // a kind, never a state, so it is never toned, and it is decoration beside the words
  // that name the thing (aria-hidden).
  import { KIND_GLYPH } from '../../lib/figure.ts';

  /** @type {{ kind: keyof typeof KIND_GLYPH, size?: 'sm' | 'md' }} */
  let { kind, size = 'sm' } = $props();
</script>

<svg class="kind-glyph kind-glyph--{size}" viewBox="0 0 24 24" aria-hidden="true" focusable="false" data-kind={kind}>
  <path d={KIND_GLYPH[kind]} />
</svg>
