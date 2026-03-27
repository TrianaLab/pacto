import { writable, derived } from 'svelte/store';
import { getSources, isMonitoredPhase } from './helpers.js';

// Navigation
export const currentView = writable('list'); // 'list' | 'detail'
export const currentService = writable(null);
export const currentTab = writable('overview');
export const overviewView = writable('table'); // 'table' | 'graph'
export const pendingRef = writable(null);
export const pendingCompat = writable(null);

// Data
export const services = writable([]);
export const serviceDetails = writable({}); // { [name]: detail }
export const serviceVersions = writable({}); // { [name]: versions[] }
export const serviceAggregated = writable({}); // { [name]: aggregated }
export const dependents = writable([]);
export const crossRefs = writable({ references: [], referencedBy: [] });
export const graphData = writable(null);
export const sourcesInfo = writable([]);
export const discovering = writable(false);
export const appVersion = writable(null);

// Filters
export const phaseFilter = writable('all');
export const enabledSources = writable({}); // empty = all enabled
export const searchTerm = writable('');

export function isSourceEnabled(src, enabled) {
  const keys = Object.keys(enabled);
  return keys.length === 0 || !!enabled[src];
}

export function toggleSourceClick(src, enabled) {
  const keys = Object.keys(enabled);
  const next = { ...enabled };
  if (keys.length === 0) {
    return { [src]: true };
  } else if (next[src]) {
    if (keys.length === 1) {
      return {};
    } else {
      delete next[src];
      return next;
    }
  } else {
    next[src] = true;
    return next;
  }
}

// Derived: filtered services for the list view
export const filteredServices = derived(
  [services, phaseFilter, enabledSources, searchTerm],
  ([$services, $filter, $enabled, $search]) => {
    return $services.filter((svc) => {
      const phase = svc.phase;
      const sources = getSources(svc);
      const phaseMatch =
        $filter === 'all' || ($filter === 'Unknown' ? !isMonitoredPhase(phase) : $filter === phase);
      const sourceMatch = sources.some((s) => isSourceEnabled(s, $enabled));
      if (!phaseMatch || !sourceMatch) return false;
      if ($search) {
        const text = [svc.name, svc.owner || '', svc.version || '', sources.join(' ')].join(' ').toLowerCase();
        if (!text.includes($search.toLowerCase())) return false;
      }
      return true;
    });
  }
);

// Derived: stats counts from filtered services
export const stats = derived(filteredServices, ($filtered) => {
  let healthy = 0, degraded = 0, invalid = 0, unknown = 0;
  for (const svc of $filtered) {
    if (svc.phase === 'Healthy') healthy++;
    else if (svc.phase === 'Degraded') degraded++;
    else if (svc.phase === 'Invalid') invalid++;
    else unknown++;
  }
  const total = healthy + degraded + invalid + unknown;
  const monitored = healthy + degraded + invalid;
  return { total, monitored, healthy, degraded, invalid, unknown };
});

// Navigation helpers
export function navigateTo(view, svc, ref, compat) {
  currentView.set(view);
  currentService.set(svc || null);
  pendingRef.set(ref || null);
  pendingCompat.set(compat || null);
  currentTab.set('overview');
  if (view === 'list') {
    phaseFilter.set('all');
  }
  const wantHash = view === 'list' ? '#' : '#service/' + encodeURIComponent(svc);
  if ((location.hash || '#') !== wantHash) {
    history.pushState(null, '', wantHash);
  }
}

export function serviceExists(name, serviceList) {
  return serviceList.some((s) => s.name === name);
}

export function resolveServiceName(name, serviceList) {
  if (!name) return name;
  if (serviceExists(name, serviceList)) return name;
  const stripped = name.replace(/-pacto$/, '');
  if (stripped !== name && serviceExists(stripped, serviceList)) return stripped;
  return name;
}

// Initialize from hash
export function initFromHash() {
  const hash = location.hash;
  if (hash === '#graph') {
    overviewView.set('graph');
  } else if (hash.startsWith('#service/')) {
    const svc = decodeURIComponent(hash.substring(9));
    currentView.set('detail');
    currentService.set(svc);
  }
}
