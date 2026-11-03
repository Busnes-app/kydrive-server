import { describe, expect, it } from 'vitest';
import { trashNotice } from './Drive';

describe('trashNotice', () => {
  it('names a workspace\'s own trash period', () => {
    expect(trashNotice(30)).toBe('Items here are deleted permanently after 30 days.');
  });
  it('never promises to keep trash when the workspace follows the drive-wide period', () => {
    expect(trashNotice(0)).toBe('Items here may be deleted permanently after the trash period your administrator sets.');
  });
});
