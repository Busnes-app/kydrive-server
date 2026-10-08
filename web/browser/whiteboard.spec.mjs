import { test, expect } from '@playwright/test';

const png = 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg==';
const base = { angle: 0, strokeColor: '#1e1e1e', backgroundColor: 'transparent', fillStyle: 'solid', strokeWidth: 2, strokeStyle: 'solid', roughness: 1, opacity: 100, groupIds: [], frameId: null, roundness: null, boundElements: null, updated: 1, link: null, locked: false, version: 1, versionNonce: 1 };
// A scene as excalidraw.com writes it: bent arrow, deleted element, embedded image and a custom background.
const fixture = {
  type: 'excalidraw', version: 2, source: 'https://excalidraw.com',
  elements: [
    { ...base, id: 'arrow', type: 'arrow', x: 100, y: 100, width: 200, height: 100, seed: 1, isDeleted: false, points: [[0, 0], [200, 0], [200, 100]], lastCommittedPoint: null, startBinding: null, endBinding: null, startArrowhead: null, endArrowhead: 'arrow', elbowed: false },
    { ...base, id: 'gone', type: 'rectangle', x: 400, y: 100, width: 80, height: 80, seed: 2, isDeleted: true },
    { ...base, id: 'label', type: 'text', x: 400, y: 300, width: 120, height: 25, seed: 4, isDeleted: false, text: 'Hand-drawn', originalText: 'Hand-drawn', fontSize: 20, fontFamily: 5, textAlign: 'left', verticalAlign: 'top', containerId: null, autoResize: true, lineHeight: 1.25 },
    { ...base, id: 'cjk', type: 'text', x: 400, y: 360, width: 120, height: 25, seed: 5, isDeleted: false, text: '你好世界', originalText: '你好世界', fontSize: 20, fontFamily: 5, textAlign: 'left', verticalAlign: 'top', containerId: null, autoResize: true, lineHeight: 1.25 },
    { ...base, id: 'img', type: 'image', x: 100, y: 300, width: 64, height: 64, seed: 3, isDeleted: false, fileId: 'pixel', status: 'saved', scale: [1, 1], crop: null },
  ],
  appState: { gridSize: 20, viewBackgroundColor: '#ffeedd' },
  files: { pixel: { mimeType: 'image/png', id: 'pixel', dataURL: png, created: 1 } },
};

test('whiteboard opens, edits and saves Excalidraw scenes without loss', async ({ page }, testInfo) => {
  const violations = [];
  const external = [];
  page.on('console', m => { if (/Content Security Policy|violates.*directive/i.test(m.text())) violations.push(m.text()); });
  const fonts = [];
  const cjk = [];
  page.on('response', r => {
    if (!r.url().includes('/excalidraw/fonts/')) return;
    fonts.push(r.status());
    if (r.url().includes('/Xiaolai/')) cjk.push(r.status());
  });
  page.on('request', r => { if (!r.url().startsWith('http://127.0.0.1:5391/') && !/^(data|blob):/.test(r.url())) external.push(r.url()); });

  await page.goto('/');
  expect((await page.request.post('/api/auth/login', { data: { username: 'admin', password: 'BrowserUpdated456!' } })).ok()).toBe(true);
  const csrf = (await page.context().cookies()).find(c => c.name === 'ky_csrf').value;
  const headers = { 'X-CSRF-Token': csrf };
  const ws = await (await page.request.post('/api/drive/personal-workspace', { headers })).json();
  const upload = (rev, id, body) => page.request.post(`/api/drive/workspaces/${ws.id}/uploads?${new URLSearchParams({ name: `scene-${testInfo.project.name}.excalidraw`, parent: '', revision: String(rev), ...(id && { file: id }) })}`, { headers: { ...headers, 'Content-Type': 'application/octet-stream' }, data: body });
  const created = await upload(0, '', JSON.stringify(fixture));
  expect(created.ok(), await created.text()).toBe(true);
  const { id } = await created.json();
  const scene = async () => (await page.request.get(`/api/drive/files/${id}/download`)).json();
  const revision = async () => (await (await page.request.get(`/api/drive/files/${id}`)).json()).revision;
  const opened = await revision();

  await page.goto(`/whiteboard.html?file=${id}`);
  const canvas = page.locator('canvas.interactive');
  await expect(canvas).toBeVisible();
  await page.waitForTimeout(1500); // Longer than the autosave debounce.
  expect(await revision()).toBe(opened);

  const box = await canvas.boundingBox();
  await page.getByTitle(/^Rectangle/).click();
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  await page.mouse.move(box.x + box.width / 2 + 80, box.y + box.height / 2 + 60, { steps: 5 });
  await page.mouse.up();
  await expect.poll(revision).toBe(opened + 1);
  // Desktop shows "Saved" top right; mobile shows the status only while it is not "Saved".
  await expect(page.getByRole('status').filter({ hasText: /Saving|Not saved|Offline/ })).toHaveCount(0);

  const saved = await scene();
  expect(saved.type).toBe('excalidraw');
  expect(saved.appState.viewBackgroundColor).toBe('#ffeedd');
  expect(saved.files.pixel.dataURL).toBe(png);
  const live = saved.elements.filter(e => !e.isDeleted);
  expect(live.find(e => e.id === 'arrow').points).toEqual([[0, 0], [200, 0], [200, 100]]);
  expect(live.find(e => e.id === 'img').fileId).toBe('pixel');
  expect(live.find(e => e.id === 'label').text).toBe('Hand-drawn');
  expect(live.find(e => e.id === 'gone')).toBeUndefined();
  const drawn = live.find(e => e.type === 'rectangle');
  expect(drawn).toMatchObject({ seed: expect.any(Number), versionNonce: expect.any(Number), version: expect.any(Number) });
  await page.screenshot({ path: testInfo.outputPath('whiteboard.png') });

  // A revision written elsewhere must stop autosave rather than overwrite it.
  expect((await upload(opened + 1, id, JSON.stringify(fixture))).ok()).toBe(true);
  await page.getByTitle(/^Ellipse/).click();
  await page.mouse.move(box.x + 60, box.y + box.height - 160);
  await page.mouse.down();
  await page.mouse.move(box.x + 140, box.y + box.height - 100, { steps: 5 });
  await page.mouse.up();
  await expect(page.getByRole('status').filter({ hasText: 'Not saved' })).toContainText(/revision conflict/i);
  await page.screenshot({ path: testInfo.outputPath('conflict.png') });
  expect(await revision()).toBe(opened + 2);

  expect(fonts.length).toBeGreaterThan(0);
  expect(cjk.length).toBeGreaterThan(0);
  expect(fonts.every(s => s === 200)).toBe(true);
  expect(violations).toEqual([]);
  expect(external).toEqual([]);
});
