<script>
  // Station (SPEC 3, r5): a flat rounded bar on a track, one per environment. It is never
  // toned by what waits in it (S10): severity lives in the bars and counts beside it, so
  // it is neutral, or accent when it is the subject. It anchors on its top, sides and
  // bottom (`a-t`, `a-l`, `a-r`, `a-b`) and carries no words.
  /** @type {{ tone?: 'neutral' | 'accent', w?: number, a: string, m?: string }} */
  let { tone = 'neutral', w = 96, a, m = '' } = $props();
</script>

<svg class="stn mk t-{tone}" width={w} height="28" viewBox="0 0 {w} 28" aria-hidden="true" focusable="false" data-m={m || undefined}>
  <rect x="2" y="6" width={w - 4} height="16" rx="5" />
  <circle r="1" fill="none" cx={w / 2} cy="6" data-a="{a}-t" />
  <circle r="1" fill="none" cx="2" cy="14" data-a="{a}-l" />
  <circle r="1" fill="none" cx={w - 2} cy="14" data-a="{a}-r" />
  <circle r="1" fill="none" cx={w / 2} cy="22" data-a="{a}-b" />
</svg>
