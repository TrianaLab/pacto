<script>
  // The exact service a delivery list is narrowed to, from a service page's
  // link, and the way back to every service.
  let { service = '', domain = '', clear = '' } = $props();
</script>

{#if service}
  <p class="t-body-2" data-testid="delivery-service-filter">
    Only the Stages of <strong>{service}</strong>{#if domain}{' '}in {domain}{/if}.
    <a href={clear}>Show every service</a>
  </p>
{/if}
