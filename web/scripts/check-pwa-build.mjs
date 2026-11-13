import assert from 'node:assert/strict';
import { log } from 'node:console';
import { readFileSync } from 'node:fs';
import { URL } from 'node:url';

const worker = readFileSync(new URL('../dist/generated/sw.js', import.meta.url), 'utf8');
const pushWorker = readFileSync(new URL('../dist/generated/push-sw.js', import.meta.url), 'utf8');
const precache = worker.match(/precacheAndRoute\(\[([\s\S]*?)\],\s*\{\}\)/);
assert.ok(precache, 'service worker must contain a precache manifest');

const urls = [...precache[1].matchAll(/url:"([^"]+)"/g)].map(([, url]) => url);
assert.ok(urls.includes('index.html'), 'precache must include the app shell');
assert.ok(urls.some((url) => /^assets\/index-.+\.js$/.test(url)), 'precache must include the app JavaScript');
assert.ok(urls.some((url) => /^assets\/.+\.css$/.test(url)), 'precache must include application CSS');
assert.ok(urls.some((url) => /^assets\/.+\.woff2$/.test(url)), 'precache must include local fonts');
assert.match(worker, /push-sw\.js/, 'service worker must import the push event handlers');
assert.match(pushWorker, /addEventListener\(['"]push['"]/, 'push worker must handle push events');
assert.match(pushWorker, /notificationclick/, 'push worker must handle notification clicks');

log(`PWA precache verified (${urls.length} entries)`);
