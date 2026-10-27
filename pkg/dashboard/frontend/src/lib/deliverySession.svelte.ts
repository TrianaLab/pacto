/**
 * Who the dashboard acts as. One read of /api/session shared by the sign-in
 * gate, the identity bar and every delivery view: the identity mode, whether
 * the caller signed in (and how freshly) and who they are.
 */
import { api, type DeliverySession } from './api.ts';

export const deliverySession = $state<{ data: DeliverySession | null; error: unknown; loaded: boolean }>({
  data: null,
  error: null,
  loaded: false,
});

let inflight: Promise<void> | null = null;

/** loadSession reads the session; concurrent callers share one request. It
 *  never rejects: a failure lands in deliverySession.error. */
export function loadSession(): Promise<void> {
  inflight ??= Promise.resolve()
    .then(() => api.session())
    .then((s) => { deliverySession.data = s; deliverySession.error = null; })
    .catch((e) => { deliverySession.error = e; })
    .finally(() => { deliverySession.loaded = true; inflight = null; });
  return inflight;
}
