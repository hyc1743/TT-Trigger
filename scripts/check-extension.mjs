import { readFile, readdir, stat } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';

const root = resolve(import.meta.dirname, '..');
const manifestPath = resolve(root, 'extension/manifest.json');
const manifest = JSON.parse(await readFile(manifestPath, 'utf8'));

if (manifest.manifest_version !== 3) throw new Error('manifest_version must be 3');
if (manifest.background?.type !== 'module') throw new Error('background service worker must be a module');

const paths = [
  manifest.background.service_worker,
  manifest.action.default_popup,
  ...(manifest.options_page ? [manifest.options_page] : []),
  ...Object.values(manifest.icons),
  ...Object.values(manifest.action.default_icon)
];

for (const relativePath of new Set(paths)) {
  await stat(resolve(root, 'extension', relativePath));
}

for (const name of await readdir(resolve(root, 'extension'))) {
  if (!name.endsWith('.js')) continue;
  const modulePath = resolve(root, 'extension', name);
  const source = await readFile(modulePath, 'utf8');
  for (const match of source.matchAll(/(?:from\s+|import\s*)['"](\.\/[^'"]+)['"]/g)) {
    await stat(resolve(dirname(modulePath), match[1]));
  }
}

console.log('Chrome extension manifest and referenced assets are valid.');
