import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, fireEvent, render } from '@testing-library/react';
import { FileIcon } from './FileIcon';

afterEach(cleanup);

describe('FileIcon', () => {
  it('asks for a thumbnail of an image, per revision, and falls back to the icon', () => {
    const { container } = render(<FileIcon file={{ id: 'f1', name: 'Photo.JPG', revision: 3 }} />);
    const img = container.querySelector('img');
    expect(img?.getAttribute('src')).toBe('/api/drive/files/f1/thumbnail?r=3');
    fireEvent.error(img!);
    expect(container.querySelector('img')).toBeNull();
    expect(container.querySelector('svg.drive-file-icon')).not.toBeNull();
  });

  it('never asks for a thumbnail of a non-image', () => {
    for (const name of ['report.docx', 'paper.pdf', 'logo.svg', 'README']) {
      const { container } = render(<FileIcon file={{ id: 'x', name }} />);
      expect(container.querySelector('img')).toBeNull();
      expect(container.querySelector('svg.drive-file-icon')).not.toBeNull();
      cleanup();
    }
  });
});
