import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import {
  downloadConfig, prepareDownloads, publishDownloads, verifyPublicDownload, createR2Transport, parseArguments, verifyPublishedRelease,
} from './mobile-egress-downloads.mjs';

const base = 'https://pub-854a819dc52143fcaa714026721d9d4b.r2.dev';
const source = 'a'.repeat(40);
const hash = bytes => createHash('sha256').update(bytes).digest('hex');
const config = () => ({ R2_ACCOUNT_ID: 'b'.repeat(32), R2_BUCKET: 'order-tracker-downloads', R2_PUBLIC_BASE_URL: base,
  AWS_ACCESS_KEY_ID: 'test-key', AWS_SECRET_ACCESS_KEY: 'test-secret' });
async function fixture(t, releases = [{ version: '2.0.0', platforms: ['windows', 'macos'] }]) {
  const directory = await mkdtemp(join(tmpdir(), 'mobile-egress-r2-test-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const requests = [];
  for (const release of releases) {
    const androidOnly = release.platforms.length === 1 && release.platforms[0] === 'android';
    const definitions = androidOnly ? [[`zfnf-mobile-egress-android-${release.version}.apk`, `android/app/build/outputs/apk/release/zfnf-mobile-egress-android-${release.version}.apk`]] : [
      ['MobileEgressClientSetup.exe', `windows-client/build/release/mobile-egress-client-windows-${release.version}/MobileEgressClientSetup.exe`],
      [`mobile-egress-client-macos-${release.version}-arm64.pkg`, `windows-client/build/release/mobile-egress-client-macos-${release.version}-arm64.pkg`],
    ];
    const freeze = { schemaVersion: 1, tag: `v${release.version}`, sourceCommit: source,
      components: androidOnly ? ['Android'] : ['Desktop'], artifacts: [] };
    for (const [name, path] of definitions) {
      const bytes = Buffer.from(`signed fixture ${release.version}: ${name}`);
      const file = join(directory, path);
      await mkdir(join(file, '..'), { recursive: true }); await writeFile(file, bytes);
      freeze.artifacts.push({ name, digest: `sha256:${hash(bytes)}` });
    }
    await mkdir(join(directory, 'windows-client/build/release'), { recursive: true });
    await writeFile(join(directory, `windows-client/build/release/mobile-egress-${release.version}.freeze.json`), JSON.stringify(freeze));
    requests.push({ ...release, sourceCommit: source });
  }
  return { directory, request: { schemaVersion: 1, channel: 'pilot', releases: requests },
    prepare: request => prepareDownloads({ repositoryRoot: directory, request: request ?? { schemaVersion: 1, channel: 'pilot', releases: requests }, resolveTag: async () => source }) };
}
function remote(plan) {
  const objects = new Map(); const writes = [];
  const headers = item => ({ 'content-length': String(item.size), 'content-type': item.contentType,
    'content-disposition': `attachment; filename="${item.name}"`, 'cache-control': 'public, max-age=31536000, immutable' });
  const metadata = item => ({ ContentLength: item.size, Metadata: { sha256: item.sha256 }, ETag: '"object-etag"' });
  return { objects, writes, metadata, headers,
    async head(key) { return objects.get(key)?.head; },
    async putImmutable(item) {
      if (objects.has(item.key)) throw new Error('Conditional write conflict');
      writes.push(item.key); objects.set(item.key, { head: metadata(item), body: await readFile(item.file), headers: headers(item) });
    },
    async putCatalog(contents, expectedETag) {
      const previous = objects.get('mobile-egress/downloads.json');
      if ((previous?.head?.ETag ?? null) !== expectedETag) throw new Error('Catalog changed concurrently');
      writes.push('mobile-egress/downloads.json');
      const body = Buffer.from(contents);
      objects.set('mobile-egress/downloads.json', { body, head: { ContentLength: body.length, Metadata: { sha256: hash(body) }, ETag: '"new-catalog"' } });
    },
    async fetch(url) {
      const entry = objects.get(new URL(url).pathname.slice(1));
      return entry ? new Response(entry.body, { headers: entry.headers ?? { 'content-type': 'application/json', 'content-length': String(entry.body.length), 'cache-control': 'no-store' } }) : new Response('', { status: 404 });
    },
  };
}

test('publisher refuses other buckets, origins, missing credentials, and unsafe URL forms', () => {
  for (const patch of [{ R2_BUCKET: 'different-bucket' }, { R2_PUBLIC_BASE_URL: 'http://example.com' },
    { R2_PUBLIC_BASE_URL: `${base}/order-tracker` }, { R2_PUBLIC_BASE_URL: `${base}:443` },
    { R2_PUBLIC_BASE_URL: `${base}?x=y` }, { R2_PUBLIC_BASE_URL: 'https://user:pass@example.com' },
    { R2_ACCOUNT_ID: '../account' }, { AWS_SECRET_ACCESS_KEY: '' }]) {
    assert.throws(() => downloadConfig({ ...config(), ...patch }));
  }
  assert.equal(downloadConfig(config()).endpoint, `https://${'b'.repeat(32)}.r2.cloudflarestorage.com`);
});
test('dry run is default and publishing needs explicit pilot confirmation', () => {
  assert.equal(parseArguments(['--plan', 'plan.json']).publish, false);
  assert.equal(parseArguments(['--plan', 'plan.json', '--dry-run']).publish, false);
  assert.throws(() => parseArguments(['--plan', 'plan.json', '--publish']), /pilot/i);
  assert.throws(() => parseArguments(['--plan', 'plan.json', '--publish', '--pilot', '--dry-run']));
  assert.throws(() => parseArguments(['--plan', 'plan.json', '--clobber']));
  assert.equal(parseArguments(['--plan', 'plan.json', '--publish', '--pilot']).publish, true);
});
test('catalog includes only explicit platform selections, including independently frozen versions', async t => {
  const f = await fixture(t, [{ version: '2.0.0', platforms: ['windows', 'macos'] }, { version: '2.0.1', platforms: ['android'] }]);
  const plan = await f.prepare();
  assert.deepEqual(Object.keys(plan.catalog.platforms), ['windows', 'macos', 'android']);
  assert.equal(plan.catalog.platforms.windows.url, `${base}/mobile-egress/2.0.0/MobileEgressClientSetup.exe`);
  assert.equal(plan.catalog.platforms.android.url, `${base}/mobile-egress/2.0.1/zfnf-mobile-egress-android-2.0.1.apk`);
  assert.equal(plan.catalog.channel, 'pilot');
  const partial = await f.prepare({ ...f.request, releases: [f.request.releases[0]] });
  assert.equal(partial.catalog.platforms.android, undefined);
});
test('rejects legacy, unsafe, duplicate and mismatched source selections before publication', async t => {
  const f = await fixture(t);
  for (const version of ['1.1.7', '../2.0.0', '2.0.0/next', '02.0.0', '2.0.0-pilot']) {
    await assert.rejects(f.prepare({ ...f.request, releases: [{ ...f.request.releases[0], version }] }));
  }
  await assert.rejects(f.prepare({ ...f.request, channel: 'stable' }), /pilot/i);
  await assert.rejects(f.prepare({ ...f.request, releases: [{ ...f.request.releases[0], platforms: ['android'] }] }), /absent/i);
  await assert.rejects(f.prepare({ ...f.request, releases: [{ ...f.request.releases[0], sourceCommit: 'c'.repeat(40) }] }), /source/i);
  await assert.rejects(prepareDownloads({ repositoryRoot: f.directory, request: f.request, resolveTag: async () => 'd'.repeat(40) }), /source|tag/i);
  await assert.rejects(f.prepare({ ...f.request, releases: [f.request.releases[0], f.request.releases[0]] }), /duplicate/i);
});
test('freeze cannot introduce unexpected filenames or altered local bytes', async t => {
  const f = await fixture(t); const path = join(f.directory, 'windows-client/build/release/mobile-egress-2.0.0.freeze.json');
  const freeze = JSON.parse(await readFile(path, 'utf8'));
  await writeFile(path, JSON.stringify({ ...freeze, artifacts: [...freeze.artifacts, { name: '../private.key', digest: `sha256:${'f'.repeat(64)}` }] }));
  await assert.rejects(f.prepare(), /artifact|freeze/i);
  await writeFile(path, JSON.stringify(freeze));
  await writeFile(join(f.directory, 'windows-client/build/release/mobile-egress-client-windows-2.0.0/MobileEgressClientSetup.exe'), 'different bytes');
  await assert.rejects(f.prepare(), /digest|hash/i);
});
test('preflights all immutable objects before any write, stopping on a later conflict', async t => {
  const f = await fixture(t), plan = await f.prepare(), r = remote(plan);
  const last = plan.artifacts.at(-1);
  r.objects.set(last.key, { head: { ...r.metadata(last), Metadata: { sha256: 'f'.repeat(64) } } });
  await assert.rejects(publishDownloads(plan, r), /differs|conflict/i);
  assert.deepEqual(r.writes, []);
});
test('incorrect bytes hidden behind matching object metadata block all writes', async t => {
  const f = await fixture(t), plan = await f.prepare(), r = remote(plan), last = plan.artifacts.at(-1);
  r.objects.set(last.key, { head: r.metadata(last), body: Buffer.alloc(last.size, 42), headers: r.headers(last) });
  await assert.rejects(publishDownloads(plan, r), /digest|SHA-256/i);
  assert.deepEqual(r.writes, []);
});
test('interrupted uploads resume immutable assets without promoting an incomplete catalog', async t => {
  const f = await fixture(t), plan = await f.prepare(), r = remote(plan), realPut = r.putImmutable;
  r.putImmutable = async function(item) { if (item === plan.artifacts[1]) throw new Error('Network interruption'); return realPut.call(this, item); };
  await assert.rejects(publishDownloads(plan, r), /interruption/);
  assert.equal(r.objects.has('mobile-egress/downloads.json'), false);
  r.putImmutable = realPut;
  await publishDownloads(plan, r);
  assert.equal(r.writes.filter(key => key === plan.artifacts[0].key).length, 1);
  assert.equal(r.writes.at(-1), 'mobile-egress/downloads.json');
  assert.deepEqual(JSON.parse(r.objects.get('mobile-egress/downloads.json').body), plan.catalog);
});
test('public byte or download-header failures prevent catalog promotion', async t => {
  const f = await fixture(t), plan = await f.prepare();
  for (const fault of ['bytes', 'size', 'content-type', 'content-disposition', 'cache-control', 'redirect']) {
    const r = remote(plan), fetch = r.fetch;
    r.fetch = async function(url, init) {
      const response = await fetch.call(this, url, init);
      if (fault === 'redirect') return new Response(null, { status: 302, headers: { location: 'https://example.com' } });
      if (fault === 'bytes') return new Response(Buffer.alloc(plan.artifacts[0].size, 42), { headers: response.headers });
      const headers = new Headers(response.headers);
      headers.set(fault === 'size' ? 'content-length' : fault, 'wrong');
      return new Response(response.body, { headers });
    };
    await assert.rejects(publishDownloads(plan, r), /public|download|header|digest|hash/i);
    assert.equal(r.objects.has('mobile-egress/downloads.json'), false, fault);
  }
});
test('public verifier requests identity bytes with a bound and refuses redirect or oversized bodies', async t => {
  const f = await fixture(t), plan = await f.prepare(), item = plan.artifacts[0], r = remote(plan);
  await r.putImmutable(item);
  await verifyPublicDownload(item, async (url, init) => {
    assert.equal(init.redirect, 'error'); assert.equal(init.headers['Accept-Encoding'], 'identity'); assert.ok(init.signal);
    return r.fetch(url);
  });
  await assert.rejects(verifyPublicDownload(item, async () => new Response(Buffer.alloc(item.size + 1), { headers: r.headers(item) })), /size|large|bytes/i);
  await assert.rejects(verifyPublicDownload(item, async () => { throw new DOMException('Timed out', 'TimeoutError'); }), /Public download verification failed/);
});
test('a concurrent immutable writer or catalog promotion is never overwritten', async t => {
  const f = await fixture(t), plan = await f.prepare(), r = remote(plan), put = r.putImmutable;
  r.putImmutable = async function(item) { this.objects.set(item.key, { head: { ...this.metadata(item), Metadata: { sha256: 'f'.repeat(64) } } }); return put.call(this, item); };
  await assert.rejects(publishDownloads(plan, r), /conflict/i);
  assert.equal(r.objects.has('mobile-egress/downloads.json'), false);
  const c = remote(plan), fetch = c.fetch;
  c.fetch = async function(url, init) { const response = await fetch.call(this, url, init); this.objects.set('mobile-egress/downloads.json', { head: { ETag: '"concurrent"' }, body: Buffer.from('preserved') }); return response; };
  await assert.rejects(publishDownloads(plan, c), /concurrent/);
  assert.equal(c.objects.get('mobile-egress/downloads.json').body.toString(), 'preserved');
});
test('catalog success requires public bytes and no-store headers after conditional promotion', async t => {
  const f = await fixture(t), plan = await f.prepare(), r = remote(plan), fetch = r.fetch;
  r.fetch = async function(url, init) {
    if (url.endsWith('/downloads.json')) return new Response('stale catalog', { headers: { 'content-type': 'application/json', 'cache-control': 'public, max-age=31536000' } });
    return fetch.call(this, url, init);
  };
  await assert.rejects(publishDownloads(plan, r), /catalog/i);
});
test('remote GitHub tag and frozen release digest must agree before R2 publication', async t => {
  const f = await fixture(t), plan = await f.prepare();
  const release = { tagName: 'v2.0.0', isDraft: false, isPrerelease: true,
    assets: plan.artifacts.map(a => ({ name: a.name, state: 'uploaded', size: a.size, digest: `sha256:${a.sha256}` })) };
  const command = async (program, args) => program === 'gh' ? JSON.stringify(release) :
    args.includes('ls-remote') ? `${source}\trefs/tags/v2.0.0` : 'https://github.com/cbjjensen/mobile-egress.git';
  await verifyPublishedRelease(plan, f.directory, command);
  await assert.rejects(verifyPublishedRelease(plan, f.directory, async (program, args) => args.includes('ls-remote') ? `${'c'.repeat(40)}\trefs/tags/v2.0.0` : command(program, args)), /source/i);
  release.assets[0].digest = `sha256:${'f'.repeat(64)}`;
  await assert.rejects(verifyPublishedRelease(plan, f.directory, command), /artifact/i);
});
test('R2 transport uses conditional writes and sanitizes command failures', async t => {
  const f = await fixture(t), plan = await f.prepare(), calls = [];
  const transport = createR2Transport(downloadConfig(config()), { run: async (program, args) => {
    calls.push(args); return '{}';
  } });
  await transport.putImmutable(plan.artifacts[0]);
  assert.ok(calls[0].includes('--if-none-match')); assert.equal(calls[0][calls[0].indexOf('--if-none-match') + 1], '*');
  assert.ok(calls[0].includes('mobile-egress/2.0.0/MobileEgressClientSetup.exe'));
  assert.equal(calls[0].some(arg => arg.includes('test-secret')), false);
  const failed = createR2Transport(downloadConfig(config()), { run: async () => { throw new Error('secret upstream body'); } });
  await assert.rejects(failed.head(plan.artifacts[0].key), error => /inspect R2/.test(error.message) && !error.message.includes('secret'));
  const unavailable = createR2Transport(downloadConfig(config()), { run: async () => '{"Bucket":"","Key":""}' });
  await assert.rejects(unavailable.checkConditionalSupport(), /conditional PutObject/);
  const supported = createR2Transport(downloadConfig(config()), { run: async () => '{"IfMatch":"","IfNoneMatch":""}' });
  await supported.checkConditionalSupport();
});
