<script>
  // A target's environment, its own column beside the target name. A target that
  // reported none says so rather than leaving a blank that reads as "no environment".
  let { env = '' } = $props();
</script>

<span class="pl-env" class:is-unknown={!env} title={env || 'Environment unknown'}>{#if env}<bdi>{env}</bdi>{:else}Environment unknown{/if}</span>
