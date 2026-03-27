export function pct(n, t) {
  return t > 0 ? Math.round((n / t) * 100) : 0;
}

export function extractServiceName(ref) {
  if (!ref) return '';
  ref = ref.replace(/^oci:\/\//, '');
  const parts = ref.split('/');
  let name = parts[parts.length - 1];
  const idx = name.indexOf(':');
  if (idx > 0) name = name.substring(0, idx);
  return name;
}

export function getSources(svc) {
  return (svc.sources || [svc.source]).filter(Boolean);
}

export function isMonitoredPhase(phase) {
  return phase === 'Healthy' || phase === 'Degraded' || phase === 'Invalid';
}

export function phaseBadgeClass(phase) {
  const p = phase || 'Unknown';
  return (
    { Healthy: 'badge-ok', Degraded: 'badge-warning', Invalid: 'badge-critical', Unknown: 'badge-neutral', Reference: 'badge-neutral' }[p] ||
    'badge-neutral'
  );
}

export function complianceBadgeClass(status) {
  return { OK: 'badge-ok', WARNING: 'badge-warning', ERROR: 'badge-critical', REFERENCE: 'badge-neutral' }[status] || 'badge-neutral';
}

export function complianceScoreClass(score, status) {
  if (status === 'ERROR' || score < 50) return 'compliance-score-error';
  if (status === 'WARNING' || score < 80) return 'compliance-score-warning';
  return 'compliance-score-ok';
}

export function classificationBadgeClass(c) {
  if (c === 'NON_BREAKING') return 'badge-ok';
  if (c === 'POTENTIAL_BREAKING') return 'badge-warning';
  if (c === 'BREAKING') return 'badge-critical';
  return 'badge-neutral';
}

export function classificationLabel(c) {
  if (c === 'NON_BREAKING') return 'non-breaking';
  if (c === 'POTENTIAL_BREAKING') return 'potential breaking';
  if (c === 'BREAKING') return 'breaking';
  return c;
}

export function sourcePillClass(type) {
  return 'source-pill-' + type;
}

export const sourceTooltips = {
  k8s: 'Kubernetes: live CRD status from the cluster operator',
  oci: 'OCI Registry: contract versions pushed to container registries',
  local: 'Local: contract from the working directory (pacto.yaml)',
};

export const sourceColors = {
  k8s: 'var(--info)',
  oci: 'var(--accent)',
  local: 'var(--neutral)',
};

export const validationCatalog = {
  ContractValid: { category: 'contract', label: 'Contract Structure', severity: 'error' },
  ServiceExists: { category: 'infrastructure', label: 'Service Exists', severity: 'error' },
  WorkloadExists: { category: 'infrastructure', label: 'Workload Exists', severity: 'error' },
  PortsValid: { category: 'networking', label: 'Port Alignment', severity: 'error' },
  HealthEndpointValid: { category: 'networking', label: 'Health Endpoint', severity: 'error' },
  MetricsEndpointValid: { category: 'networking', label: 'Metrics Endpoint', severity: 'error' },
  WorkloadTypeMatch: { category: 'workload', label: 'Workload Type', severity: 'error' },
  StateModelMatch: { category: 'state', label: 'State Model', severity: 'error' },
  UpgradeStrategyMatch: { category: 'lifecycle', label: 'Upgrade Strategy', severity: 'warning' },
  GracefulShutdownMatch: { category: 'lifecycle', label: 'Graceful Shutdown', severity: 'warning' },
  ImageMatch: { category: 'image', label: 'Container Image', severity: 'error' },
  HealthTimingMatch: { category: 'health', label: 'Health Probe Timing', severity: 'warning' },
};

export function lookupValidation(type) {
  return validationCatalog[type] || { category: 'other', label: type, severity: 'error' };
}

export function hasValidationPath(d, prefix) {
  if (!d?.validation) return false;
  const issues = (d.validation.errors || []).concat(d.validation.warnings || []);
  return issues.some((e) => e.path?.indexOf(prefix) !== -1);
}

export function statusColor(status) {
  return (
    { Healthy: 'var(--ok)', Degraded: 'var(--warning)', Invalid: 'var(--critical)', Unmonitored: 'var(--neutral)', External: 'var(--neutral)' }[
      status
    ] || 'var(--neutral)'
  );
}

export function methodBadgeClass(method) {
  const m = (method || '').toUpperCase();
  if (m === 'GET') return 'badge-ok';
  if (m === 'POST') return 'badge-info';
  if (m === 'DELETE') return 'badge-critical';
  if (m === 'PUT' || m === 'PATCH') return 'badge-warning';
  return 'badge-neutral';
}
