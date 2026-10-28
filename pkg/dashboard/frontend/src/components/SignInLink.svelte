<script>
  import { loginUrl, signIn, hostSignsIn } from '../lib/session.svelte.ts';

  // Every Sign in link: the login URL that comes back to `hash`. A host that
  // signs people in itself (the browser demo) takes the click through signIn,
  // so the page keeps its state. fresh asks for a new login.
  let { hash, fresh = false, class: cls = 'btn btn-primary', children } = $props();
</script>

<a class={cls} href={loginUrl(hash, fresh)} onclick={(e) => { if (hostSignsIn()) { e.preventDefault(); signIn(hash, fresh); } }}>{@render children()}</a>
