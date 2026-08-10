import { readFile, stat } from 'node:fs/promises';
import { resolve } from 'node:path';

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

console.log('Chrome extension manifest and referenced assets are valid.');
