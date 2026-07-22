import { describe, it, expect } from 'vitest';
import { graphBreadcrumbs } from './breadcrumbs';

describe('graphBreadcrumbs', () => {
  it('is just Fleet with no group or focus', () => {
    expect(graphBreadcrumbs({ group: '', focus: '' })).toEqual([{ label: 'Fleet' }]);
  });

  it('links Fleet then the focused node', () => {
    expect(graphBreadcrumbs({ group: '', focus: 'payments' })).toEqual([
      { label: 'Fleet', href: '#/graph' },
      { label: 'payments' },
    ]);
  });

  it('shows the By owner crumb when grouped without focus', () => {
    expect(graphBreadcrumbs({ group: 'owner', focus: '' })).toEqual([
      { label: 'Fleet', href: '#/graph' },
      { label: 'By owner' },
    ]);
  });
});
