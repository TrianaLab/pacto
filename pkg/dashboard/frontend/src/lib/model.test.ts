/**
 * Tests for the dashboard data model shapes consumed by the frontend.
 * These validate that the frontend correctly handles the API contract:
 * - configurations[] (plural array) replaces configuration (singular object)
 * - policies[] (plural array) replaces policy (singular object)
 */
import { describe, it, expect } from 'vitest';

// Simulate the ServiceDetails shape as returned by the API
interface ConfigurationInfo {
  name?: string;
  hasSchema: boolean;
  schema?: string;
  ref?: string;
  valueKeys?: string[];
  secretKeys?: string[];
  values?: Array<{ key: string; value: string; type: string }>;
}

interface PolicyInfo {
  hasSchema: boolean;
  schema?: string;
  ref?: string;
  content?: string;
  values?: Array<{ key: string; value: string; type: string }>;
}

interface ServiceDetails {
  name: string;
  configurations?: ConfigurationInfo[];
  policies?: PolicyInfo[];
}

// These mirror the logic used in the Svelte components
function hasConfigurations(detail: ServiceDetails): boolean {
  return (detail.configurations?.length ?? 0) > 0;
}

function isMultiConfig(configs: ConfigurationInfo[]): boolean {
  return configs.length > 1 || (configs.length === 1 && !!configs[0].name);
}

function hasPolicies(detail: ServiceDetails): boolean {
  return (detail.policies?.length ?? 0) > 0;
}

describe('configurations — data shape', () => {
  it('no configuration field → section hidden', () => {
    const detail: ServiceDetails = { name: 'svc' };
    expect(hasConfigurations(detail)).toBe(false);
  });

  it('empty configurations array → section hidden', () => {
    const detail: ServiceDetails = { name: 'svc', configurations: [] };
    expect(hasConfigurations(detail)).toBe(false);
  });

  it('single unnamed config → not multi', () => {
    const configs: ConfigurationInfo[] = [
      { hasSchema: true, schema: 'configuration/schema.json' },
    ];
    expect(isMultiConfig(configs)).toBe(false);
  });

  it('single named config → treated as multi', () => {
    const configs: ConfigurationInfo[] = [
      { name: 'app', hasSchema: true, schema: 'configuration/schema.json' },
    ];
    expect(isMultiConfig(configs)).toBe(true);
  });

  it('multiple configs → treated as multi', () => {
    const configs: ConfigurationInfo[] = [
      { name: 'app', hasSchema: true, schema: 'config/app.json' },
      { name: 'shared', hasSchema: false, ref: 'oci://ghcr.io/org/shared:1.0' },
    ];
    expect(isMultiConfig(configs)).toBe(true);
  });

  it('legacy single config preserves all fields', () => {
    const config: ConfigurationInfo = {
      hasSchema: true,
      schema: 'configuration/schema.json',
      values: [
        { key: 'LOG_LEVEL', value: 'info', type: 'string' },
        { key: 'PORT', value: '8080', type: 'number' },
      ],
      secretKeys: ['DB_PASSWORD'],
    };
    expect(config.values).toHaveLength(2);
    expect(config.secretKeys).toHaveLength(1);
    expect(config.schema).toBe('configuration/schema.json');
  });

  it('ref-based config has no schema', () => {
    const config: ConfigurationInfo = {
      hasSchema: false,
      ref: 'oci://ghcr.io/org/shared-config:1.0.0',
    };
    expect(config.ref).toBeTruthy();
    expect(config.schema).toBeUndefined();
    expect(config.hasSchema).toBe(false);
  });

  it('config with valueKeys only (k8s source)', () => {
    const config: ConfigurationInfo = {
      hasSchema: false,
      valueKeys: ['DB_HOST', 'DB_PORT'],
      secretKeys: ['DB_PASSWORD'],
    };
    expect(config.valueKeys).toHaveLength(2);
    expect(config.values).toBeUndefined();
  });
});

describe('policies — data shape', () => {
  it('no policies field → section hidden', () => {
    const detail: ServiceDetails = { name: 'svc' };
    expect(hasPolicies(detail)).toBe(false);
  });

  it('empty policies array → section hidden', () => {
    const detail: ServiceDetails = { name: 'svc', policies: [] };
    expect(hasPolicies(detail)).toBe(false);
  });

  it('single local policy', () => {
    const policies: PolicyInfo[] = [
      { hasSchema: true, schema: 'policy/schema.json' },
    ];
    expect(policies).toHaveLength(1);
    expect(policies[0].hasSchema).toBe(true);
    expect(policies[0].ref).toBeUndefined();
  });

  it('single ref policy', () => {
    const policies: PolicyInfo[] = [
      { hasSchema: false, ref: 'oci://ghcr.io/org/platform-policy:1.0.0' },
    ];
    expect(policies).toHaveLength(1);
    expect(policies[0].ref).toBeTruthy();
    expect(policies[0].hasSchema).toBe(false);
  });

  it('mixed local + ref policies', () => {
    const policies: PolicyInfo[] = [
      { hasSchema: true, schema: 'policy/custom.json' },
      { hasSchema: false, ref: 'oci://ghcr.io/org/http-policy:1.0.0' },
      { hasSchema: false, ref: 'oci://ghcr.io/org/security-policy:2.0.0' },
    ];
    expect(policies).toHaveLength(3);
    const local = policies.filter(p => p.hasSchema);
    const remote = policies.filter(p => !!p.ref);
    expect(local).toHaveLength(1);
    expect(remote).toHaveLength(2);
  });

  it('policy with values (schema properties)', () => {
    const policy: PolicyInfo = {
      hasSchema: true,
      schema: 'policy/schema.json',
      values: [
        { key: 'service.owner', value: '(any)', type: 'object' },
        { key: 'runtime.workload', value: '(any)', type: 'string' },
      ],
    };
    expect(policy.values).toHaveLength(2);
  });

  it('policy with raw content', () => {
    const policy: PolicyInfo = {
      hasSchema: false,
      ref: 'oci://ghcr.io/org/policy:1.0',
      content: '{"type":"object","required":["service"]}',
    };
    expect(policy.content).toBeTruthy();
  });
});

describe('service detail — combined model', () => {
  it('service with both configurations and policies', () => {
    const detail: ServiceDetails = {
      name: 'api-gateway',
      configurations: [
        { name: 'app', hasSchema: true, schema: 'config/app.json' },
        { name: 'shared', hasSchema: false, ref: 'oci://ghcr.io/org/shared:1.0' },
      ],
      policies: [
        { hasSchema: false, ref: 'oci://ghcr.io/org/http-policy:1.0' },
      ],
    };
    expect(hasConfigurations(detail)).toBe(true);
    expect(hasPolicies(detail)).toBe(true);
    expect(isMultiConfig(detail.configurations!)).toBe(true);
  });

  it('service with configurations but no policies', () => {
    const detail: ServiceDetails = {
      name: 'internal-svc',
      configurations: [
        { hasSchema: true, schema: 'configuration/schema.json' },
      ],
    };
    expect(hasConfigurations(detail)).toBe(true);
    expect(hasPolicies(detail)).toBe(false);
    expect(isMultiConfig(detail.configurations!)).toBe(false);
  });

  it('service with policies but no configurations', () => {
    const detail: ServiceDetails = {
      name: 'platform-policy',
      policies: [
        { hasSchema: true, schema: 'policy/schema.json' },
      ],
    };
    expect(hasConfigurations(detail)).toBe(false);
    expect(hasPolicies(detail)).toBe(true);
  });

  it('service with neither configurations nor policies', () => {
    const detail: ServiceDetails = { name: 'minimal-svc' };
    expect(hasConfigurations(detail)).toBe(false);
    expect(hasPolicies(detail)).toBe(false);
  });
});
