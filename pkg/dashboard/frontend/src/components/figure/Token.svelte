<script>
  // Token (SPEC 3, r5): "the change is here". A callout in the change's tone whose notch
  // points at its station: down in a row of stations, right in the vertical layout (the
  // figure's data-w="col" picks the shape). Never a slab, so it never reads as a station.
  /** @type {{ tone: string }} */
  let { tone } = $props();
</script>

<span class="tok mk t-{tone}" aria-hidden="true">
  <svg width="48" height="38" viewBox="0 0 48 38" focusable="false">
    <g class="tok-d">
      <path class="tok-b" d="M8 3 H40 A5 5 0 0 1 45 8 V22 A5 5 0 0 1 40 27 H29 L24 34 L19 27 H8 A5 5 0 0 1 3 22 V8 A5 5 0 0 1 8 3 Z" />
      <path class="tok-g" d="M15 15 H32 M27 10 L32 15 L27 20" />
    </g>
    <g class="tok-r">
      <path class="tok-b" d="M8 4 H34 A5 5 0 0 1 39 9 V15 L46 19 L39 23 V29 A5 5 0 0 1 34 34 H8 A5 5 0 0 1 3 29 V9 A5 5 0 0 1 8 4 Z" />
      <path class="tok-g" d="M12 19 H29 M24 14 L29 19 L24 24" />
    </g>
  </svg>
</span>
