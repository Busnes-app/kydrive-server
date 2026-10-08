import { describe, expect, it } from 'vitest';
import { breadcrumb, workspace, type Folder } from './client';

const f = (id: string, parent: string): Folder => ({ id, parent, name: id.toUpperCase(), trashed: false });

describe('breadcrumb', () => {
  it('walks from the root to the folder', () => {
    expect(breadcrumb([f('a', ''), f('b', 'a'), f('c', 'b')], 'c').map(x => x.id)).toEqual(['a', 'b', 'c']);
  });
  it('is empty at the workspace root and for unknown folders', () => {
    expect(breadcrumb([f('a', '')], '')).toEqual([]);
    expect(breadcrumb([f('a', '')], 'missing')).toEqual([]);
  });
  it('stops on a cycle instead of looping', () => {
    expect(breadcrumb([f('a', 'b'), f('b', 'a')], 'a').length).toBeLessThanOrEqual(2);
  });
});

describe('workspace guard', () => {
  it('requires retention fields', () => {
    const base = { id: 'w', name: 'W', role: 'manager', kind: 'shared', quota: 1, used: 0 };
    expect(workspace(base)).toBe(false);
    expect(workspace({ ...base, trash_days: 0, keep_versions: 0 })).toBe(true);
  });
});
