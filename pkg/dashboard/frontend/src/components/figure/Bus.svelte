<script>
  // Bus (SPEC 3, r2): fan-in. N wires end at N distinct points on one vertical bar
  // (`in0`..`in{n-1}`), one wire leaves it (`a`), so wires never meet in a single point.
  // A mark, no words.
  /** @type {{ n: number, a?: string }} */
  let { n, a = 'bus' } = $props();
</script>

<div class="bus mk" data-a={a} aria-hidden="true">
  {#each Array.from({ length: n }, (_, k) => k) as k (k)}<i data-a="in{k}"></i>{/each}
</div>
