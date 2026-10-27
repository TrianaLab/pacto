/**
 * Where a delivery page links a service name. A service the fleet knows opens its
 * fleet page. One the fleet does not know (a service with no contract yet, or the
 * demo's delivery story) opens its row on Environments instead of a fleet page
 * that only says "Not found".
 */
import { api } from './api.ts';
import { NAME_LOOKUP_LIMIT } from './entityResolve.ts';
import { serviceUrl, deliveryEnvironmentsUrl } from './router.ts';

// The fleet's service names, read once. null until read, and for good when the
// read fails or its page did not carry every service: a link nobody can check
// stays a fleet link, as it always was.
const fleet = $state<{ names: Set<string> | null }>({ names: null });
let asked = false;

function ask(): void {
  asked = true;
  Promise.resolve()
    .then(() => api.fleetEntities({ kinds: ['service'], limit: NAME_LOOKUP_LIMIT }))
    .then((page) => {
      if (page && !page.truncated) fleet.names = new Set((page.entities ?? []).flatMap((e) => [e.label, e.key]));
    })
    .catch(() => {});
}

export function deliveryServiceUrl(name: string): string {
  if (!asked) ask();
  return fleet.names && !fleet.names.has(name) ? deliveryEnvironmentsUrl({ q: name }) : serviceUrl(name);
}
