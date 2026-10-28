/**
 * Who the dashboard acts as, and how people sign in to it. One read of
 * /api/session shared by the sign-in gate, the identity bar and every view:
 * the identity mode, whether the caller signed in and who they are. Where
 * delivery is served, the store also holds who delivery classifies the caller
 * as (their class and the published team rule).
 */
import { api, type DeliveryCaller, type Session } from './api.ts';

export const session = $state<{
  data: Session | null;
  caller: DeliveryCaller | null;
  error: unknown;
  loaded: boolean;
}>({ data: null, caller: null, error: null, loaded: false });

let inflight: Promise<void> | null = null;

/** loadSession reads the session and, for a signed-in caller, who delivery
 *  classifies them as; concurrent callers share one request. It never
 *  rejects: a failure lands in session.error. A delivery read that fails
 *  leaves session.caller empty, and the views then offer no decision. */
export function loadSession(): Promise<void> {
  inflight ??= Promise.resolve()
    .then(() => api.session())
    .then(async (s) => {
      session.data = s;
      session.error = null;
      session.caller = s?.signedIn ? await api.deliveryCaller().catch(() => null) : null;
    })
    .catch((e) => { session.error = e; })
    .finally(() => { session.loaded = true; inflight = null; });
  return inflight;
}

/** How to get a Kubernetes token to sign in with. */
export const TOKEN_DOCS = 'https://pacto.run/latest/integrations/kubernetes/dashboard-authentication/#token-sign-in';

/** Who the signed-in person is, as personLabel reads it. */
export interface Me {
  user?: string;
  name?: string;
  groups?: string[] | null;
}

/**
 * me is the signed-in person as the API server names them: their username,
 * their groups (the identity their Kubernetes RBAC is checked for) and the
 * name their identity provider gave, which only ever labels that username.
 * The header, every Person, the inbox and the decision read it, so they all
 * name the same person. Read inside $derived, it follows the session.
 */
export function me(): Me {
  const d = session.data;
  return { user: d?.user, name: d?.name, groups: d?.groups };
}

/**
 * personLabel names a cluster username for a reader: the name of a person who
 * signed in to this dashboard, a service account by its own name and an
 * `issuer#subject` username by its subject. Person prints the full username
 * next to it, never only in a tooltip.
 */
export function personLabel(user: string | undefined, people?: Record<string, string> | null, self: Me | null = me()): string {
  if (!user) return '';
  if (people?.[user]) return people[user];
  if (self?.name && self.user === user) return self.name;
  const sa = /^system:serviceaccount:[^:]+:(.+)$/.exec(user);
  if (sa) return sa[1];
  const hash = user.lastIndexOf('#');
  return hash > 0 && hash < user.length - 1 ? user.slice(hash + 1) : user;
}

/**
 * teamGroups are the groups that can name a team. A Kubernetes system: group
 * (system:authenticated) names no team, as the promoter's team rule says.
 */
export function teamGroups(groups: string[] | undefined | null): string[] {
  return (groups ?? []).filter((g) => !g.startsWith('system:'));
}

/**
 * signIn sends the person to sign in and back to `hash`; fresh makes them log
 * in again. A host with no /auth routes handles it itself: the browser demo
 * sets window.__pactoSignIn (beside __pactoServe) to its simulated sign-in,
 * because leaving the page would lose everything the tab simulated.
 */
export function signIn(hash: string, fresh = false): void {
  const hook = (window as unknown as { __pactoSignIn?: (hash: string, fresh: boolean) => void }).__pactoSignIn;
  if (typeof hook === 'function') hook(hash, fresh);
  else location.assign(loginUrl(hash, fresh));
}

/** hostSignsIn: the host handles sign-in itself (see signIn). */
export function hostSignsIn(): boolean {
  return typeof (window as unknown as { __pactoSignIn?: unknown }).__pactoSignIn === 'function';
}

/** loginUrl signs in and comes back to `hash`; fresh makes the person log in again. */
export function loginUrl(hash: string, fresh = false): string {
  const ret = '/' + (hash.startsWith('#') ? hash : `#${hash}`);
  return `/auth/login?return=${encodeURIComponent(ret)}${fresh ? '&fresh=1' : ''}`;
}
