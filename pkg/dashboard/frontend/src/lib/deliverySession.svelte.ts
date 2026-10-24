/**
 * Who the delivery views act as. One read of /api/session shared by every
 * delivery view: the identity mode, whether the caller signed in (and how
 * freshly) and who the apiserver says they are.
 */
import { api, type DeliverySession } from './api.ts';

export const deliverySession = $state<{ data: DeliverySession | null; error: unknown; loaded: boolean }>({
  data: null,
  error: null,
  loaded: false,
});

let inflight: Promise<void> | null = null;

/** loadSession reads the session; concurrent callers share one request. */
export function loadSession(): Promise<void> {
  inflight ??= api.session()
    .then((s) => { deliverySession.data = s; deliverySession.error = null; })
    .catch((e) => { deliverySession.error = e; })
    .finally(() => { deliverySession.loaded = true; inflight = null; });
  return inflight;
}
