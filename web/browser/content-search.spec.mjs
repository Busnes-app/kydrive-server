// Content search end to end: a word inside a file is found once indexed, with its snippet
// highlighted as text.
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { test, expect } from '@playwright/test';

// Shares file-management's saved session to stay under 20 sign-ins a minute.
const sessionFile = 'test-results/file-management-session.json';

async function signIn(page) {
  await page.goto('/');
  const saved = await readFile(sessionFile, 'utf8').then(JSON.parse, () => null);
  if (saved) {
    await page.context().addCookies(saved);
    if ((await page.request.get('/api/auth/me')).ok()) return { 'X-CSRF-Token': saved.find(c => c.name === 'ky_csrf').value };
  }
  expect((await page.request.post('/api/auth/login', { data: { username: 'admin', password: 'BrowserUpdated456!' } })).ok()).toBe(true);
  const cookies = await page.context().cookies();
  await mkdir('test-results', { recursive: true });
  await writeFile(sessionFile, JSON.stringify(cookies));
  return { 'X-CSRF-Token': cookies.find(c => c.name === 'ky_csrf').value };
}

test('finds a file by a word inside it and highlights it', async ({ page }, testInfo) => {
  test.setTimeout(90000);
  const headers = await signIn(page);
  await page.request.post('/api/drive/personal-workspace', { headers, data: {} });
  const mine = (await (await page.request.get('/api/drive/workspaces')).json()).find(w => w.kind === 'personal');
  const word = `quokka${testInfo.project.name.replace(/\W/g, '')}`;
  const name = `notes-${word.slice(6)}.md`;
  const body = `# Planning\n\nThe <b>${word}</b> budget is due on Friday.\n`;
  expect((await page.request.post(`/api/drive/workspaces/${mine.id}/uploads?name=${name}&parent=&revision=0`, { headers, data: body })).status()).toBe(201);
  // The indexer picks the change up within seconds; the API answers with a content hit.
  await expect.poll(async () => {
    const r = await (await page.request.get(`/api/drive/search?q=${word}`)).json();
    return r.results.find(h => h.name === name)?.match;
  }, { timeout: 60000, intervals: [1000] }).toEqual(['content']);

  await page.reload();
  await page.getByLabel('Search files').fill(word.toUpperCase());
  const mark = page.locator('.drive-search-snippet mark');
  await expect(mark).toHaveText(word);
  // Markup in the file stays text: no <b> element is created from it.
  await expect(page.locator('.drive-search-snippet b')).toHaveCount(0);
  await expect(page.locator('.drive-search-snippet')).toContainText('<b>');
  await page.getByRole('search').getByRole('button', { name: new RegExp(name.replace('.', '\\.')) }).click();
  await expect(page.getByRole('navigation', { name: 'Folder path' })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});
