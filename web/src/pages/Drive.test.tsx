import { describe, expect, it } from 'vitest';
import { parentsFirst, trashNotice } from './Drive';

describe('trashNotice', () => {
  it('names a workspace\'s own trash period', () => {
    expect(trashNotice(30)).toBe('Items here are deleted permanently after 30 days.');
  });
  it('never promises to keep trash when the workspace follows the drive-wide period', () => {
    expect(trashNotice(0)).toBe('Items here may be deleted permanently after the trash period your administrator sets.');
  });
});

describe('parentsFirst', () => {
  it('restores a trashed folder before the trashed folders inside it, whatever the listing order', () => {
    const f = (id: string, parent: string) => ({ id, name: id, parent, trashed: true });
    const trashed = [f('a-docs', 'z-project'), f('deep', 'a-docs'), f('z-project', ''), f('other', 'live')];
    expect(parentsFirst(trashed, trashed).map(d => d.id)).toEqual(['z-project', 'other', 'a-docs', 'deep']);
  });
});
