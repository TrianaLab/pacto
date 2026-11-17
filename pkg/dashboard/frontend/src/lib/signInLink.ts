// The sign-in link the chart's NOTES print: http://127.0.0.1:3000/#token=<jwt>.
// The token sits in the fragment, which the browser never sends to a server, and
// it leaves the address bar and the history before the hash router reads it.
// A plain module, no runes: a reassigned module $state cannot be exported.

const KEY = 'pacto.signInLink';
const PREFIX = '#token=';
let pending: string | null = null;

function strip(loc: Location, hist: History): boolean {
  if (!loc.hash.startsWith(PREFIX)) return false;
  const token = loc.hash.slice(PREFIX.length);
  hist.replaceState(hist.state, '', loc.pathname + loc.search);
  pending = token; // empty: the command that printed the link minted no token
  return true;
}

/** Takes a `#token=` link out of the URL before the app mounts. A link opened in
 *  a tab that is already open is a fragment navigation that runs no script, so a
 *  hashchange listener strips it too, keeps it across one reload and reloads. */
export function takeSignInLink(loc: Location = location, hist: History = history, win: Window = window): void {
  const kept = win.sessionStorage.getItem(KEY);
  if (kept !== null) {
    win.sessionStorage.removeItem(KEY);
    pending = kept;
  }
  strip(loc, hist);
  win.addEventListener('hashchange', () => {
    if (!strip(loc, hist)) return;
    if (pending !== null) win.sessionStorage.setItem(KEY, pending);
    loc.reload();
  });
}

/** Whether a JWT's `exp` has passed. The decode is for wording only: the server
 *  verifies the token, so an unreadable one counts as not expired. */
export function jwtExpired(token: string, now = Date.now()): boolean {
  try {
    const exp = JSON.parse(atob(token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/'))).exp;
    return typeof exp === 'number' && exp * 1000 <= now;
  } catch {
    return false;
  }
}

/** The token a sign-in link carried (empty when it carried none), once; then null. */
export function consumeSignInLink(): { token: string; expired: boolean } | null {
  if (pending === null) return null;
  const token = pending;
  pending = null;
  return { token, expired: jwtExpired(token) };
}
