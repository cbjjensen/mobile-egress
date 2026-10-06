import { createHash } from 'node:crypto';
import { execFile } from 'node:child_process';
import { createReadStream } from 'node:fs';
import { copyFile, lstat, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';

export const publicBase = 'https://pub-854a819dc52143fcaa714026721d9d4b.r2.dev';
const bucket = 'order-tracker-downloads';
const catalogKey = 'mobile-egress/downloads.json';
const repository = 'cbjjensen/mobile-egress';
const platforms = ['windows', 'macos', 'android'];
const immutableCache = 'public, max-age=31536000, immutable';
const maxArtifactBytes = 1024 * 1024 * 1024;
const exec = promisify(execFile);

async function run(program, args) {
  return (await exec(program, args, { encoding: 'utf8', maxBuffer: 1024 * 1024, timeout: 10 * 60 * 1000,
    windowsHide: true, env: { ...process.env, AWS_EC2_METADATA_DISABLED: 'true',
      AWS_REQUEST_CHECKSUM_CALCULATION: 'WHEN_REQUIRED', AWS_RESPONSE_CHECKSUM_VALIDATION: 'WHEN_REQUIRED' } })).stdout.trim();
}

export function downloadConfig(env) {
  if (!/^[a-f0-9]{32}$/.test(env.R2_ACCOUNT_ID ?? '')) throw new Error('Invalid R2 account ID');
  if (env.R2_BUCKET !== bucket || env.R2_PUBLIC_BASE_URL !== publicBase) {
    throw new Error('Use the existing approved R2 bucket and exact public HTTPS origin');
  }
  if (!env.AWS_ACCESS_KEY_ID || !env.AWS_SECRET_ACCESS_KEY) throw new Error('R2 upload credentials are missing');
  return { bucket, base: publicBase, endpoint: `https://${env.R2_ACCOUNT_ID}.r2.cloudflarestorage.com` };
}

function checkVersion(version) {
  if (typeof version !== 'string' || !/^2\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$/.test(version)) {
    throw new Error('Expected a canonical 2.x release version; pilot is a channel, not a filename suffix');
  }
}

function definition(version, platform) {
  checkVersion(version);
  if (platform === 'windows') return { name: 'MobileEgressClientSetup.exe',
    path: `windows-client/build/release/mobile-egress-client-windows-${version}/MobileEgressClientSetup.exe`, contentType: 'application/octet-stream' };
  if (platform === 'macos') return { name: `mobile-egress-client-macos-${version}-arm64.pkg`,
    path: `windows-client/build/release/mobile-egress-client-macos-${version}-arm64.pkg`, contentType: 'application/octet-stream' };
  if (platform === 'android') return { name: `zfnf-mobile-egress-android-${version}.apk`,
    path: `android/app/build/outputs/apk/release/zfnf-mobile-egress-android-${version}.apk`, contentType: 'application/vnd.android.package-archive' };
  throw new Error('Unknown download platform');
}

async function fileIdentity(file) {
  const info = await lstat(file);
  if (!info.isFile() || info.size < 1 || info.size > maxArtifactBytes) throw new Error('Artifact must be a regular, nonempty file within the upload size bound');
  const digest = createHash('sha256'); let size = 0;
  for await (const chunk of createReadStream(file)) {
    size += chunk.length;
    if (size > maxArtifactBytes) throw new Error('Artifact grew beyond the upload size bound');
    digest.update(chunk);
  }
  if (size !== info.size) throw new Error('Artifact changed while checking its size');
  return { sha256: digest.digest('hex'), size };
}

async function readJSONFile(file, label) {
  try {
    const info = await lstat(file);
    if (!info.isFile() || info.size > 65536) throw new Error();
    return JSON.parse(await readFile(file, 'utf8'));
  } catch { throw new Error(`Missing or invalid ${label}; expected a regular JSON file up to 64 KiB`); }
}

function validateFreeze(freeze, release) {
  if (freeze.schemaVersion !== 1 || freeze.tag !== `v${release.version}` || freeze.sourceCommit !== release.sourceCommit) {
    throw new Error('Freeze version or source does not match the requested release');
  }
  const components = freeze.components;
  if (!Array.isArray(components) || !components.length || new Set(components).size !== components.length ||
    components.some(c => !['Desktop', 'Windows', 'Android'].includes(c)) || components.includes('Desktop') && components.includes('Windows')) {
    throw new Error('Invalid frozen component scope');
  }
  const included = platforms.filter(p => p === 'android' ? components.includes('Android') :
    p === 'macos' ? components.includes('Desktop') : components.includes('Desktop') || components.includes('Windows'));
  const names = included.map(p => definition(release.version, p).name);
  if (!Array.isArray(freeze.artifacts) || freeze.artifacts.length !== names.length ||
    new Set(freeze.artifacts.map(a => a.name)).size !== names.length ||
    freeze.artifacts.some(a => !names.includes(a.name) || !/^sha256:[a-f0-9]{64}$/.test(a.digest ?? ''))) {
    throw new Error('Frozen artifact set contains missing, duplicate or unexpected artifacts');
  }
  return included;
}

export async function prepareDownloads({ repositoryRoot, request, resolveTag }) {
  if (request?.schemaVersion !== 1 || request.channel !== 'pilot') throw new Error('An explicit schemaVersion 1 pilot plan is required');
  if (!Array.isArray(request.releases) || !request.releases.length || request.releases.length > 3) throw new Error('Select one to three frozen releases');
  if (typeof resolveTag !== 'function') throw new Error('Release tag provenance verification is required');
  const artifacts = []; const seen = new Set();
  for (const release of request.releases) {
    checkVersion(release.version);
    if (!/^[a-f0-9]{40}$/.test(release.sourceCommit ?? '')) throw new Error('Invalid release source commit');
    if (!Array.isArray(release.platforms) || !release.platforms.length || release.platforms.some(p => !platforms.includes(p))) throw new Error('Select explicit supported platforms');
    for (const platform of release.platforms) {
      if (seen.has(platform)) throw new Error('Duplicate platform selection');
      seen.add(platform);
    }
    const freezePath = join(repositoryRoot, `windows-client/build/release/mobile-egress-${release.version}.freeze.json`);
    const freeze = await readJSONFile(freezePath, 'release freeze record');
    const included = validateFreeze(freeze, release);
    if (await resolveTag(freeze.tag) !== release.sourceCommit) throw new Error('Release tag source differs from the frozen source');
    for (const platform of release.platforms) {
      if (!included.includes(platform)) throw new Error('Selected platform is absent from the frozen release');
      const def = definition(release.version, platform);
      const file = join(repositoryRoot, def.path);
      const identity = await fileIdentity(file);
      if (`sha256:${identity.sha256}` !== freeze.artifacts.find(a => a.name === def.name).digest) throw new Error(`Local artifact digest differs from frozen release: ${def.name}`);
      const key = `mobile-egress/${release.version}/${def.name}`;
      artifacts.push({ ...identity, platform, version: release.version, sourceCommit: release.sourceCommit,
        name: def.name, contentType: def.contentType, file, key, url: `${publicBase}/${key}` });
    }
  }
  artifacts.sort((a, b) => platforms.indexOf(a.platform) - platforms.indexOf(b.platform));
  const catalog = { schemaVersion: 1, channel: 'pilot', platforms: {} };
  for (const { platform, version, sourceCommit, url, sha256, size } of artifacts) catalog.platforms[platform] = { version, sourceCommit, url, sha256, size };
  return { artifacts, catalog };
}

function objectMatches(head, item) {
  return head?.ContentLength === item.size && head?.Metadata?.sha256 === item.sha256;
}

export async function verifyPublicDownload(item, fetcher = fetch) {
  let response;
  try {
    response = await fetcher(item.url, { redirect: 'error', headers: { 'Accept-Encoding': 'identity' }, signal: AbortSignal.timeout(120000) });
    const h = response.headers;
    const isCatalog = item.key === catalogKey;
    if (response.status !== 200 || response.redirected || h.get('content-length') !== String(item.size) ||
      h.get('content-type')?.split(';')[0].trim() !== item.contentType ||
      (!isCatalog && h.get('content-disposition') !== `attachment; filename="${item.name}"`) ||
      h.get('cache-control')?.replaceAll(' ', '') !== (isCatalog ? 'no-store' : immutableCache.replaceAll(' ', '')) ||
      ![null, 'identity'].includes(h.get('content-encoding')) || !response.body) throw new Error('Public download response headers do not match the release');
    const digest = createHash('sha256'); let size = 0;
    for await (const chunk of response.body) {
      size += chunk.length;
      if (size > item.size) throw new Error('Public download exceeds the expected byte size');
      digest.update(chunk);
    }
    if (size !== item.size || digest.digest('hex') !== item.sha256) throw new Error('Public download size or SHA-256 digest does not match');
  } catch (error) {
    if (response?.body && !response.body.locked) await response.body.cancel().catch(() => {});
    throw new Error(`Public download verification failed for ${item.name}: ${error.message?.startsWith('Public download') ? error.message : 'request failed'}`);
  }
}

export async function publishDownloads(plan, transport) {
  const previousCatalog = await transport.head(catalogKey);
  if (previousCatalog && (typeof previousCatalog.ETag !== 'string' || !previousCatalog.ETag)) throw new Error('Existing catalog has no concurrency token');
  const missing = [];
  // No writes until every selected artifact and every existing versioned object agrees.
  for (const item of plan.artifacts) {
    const local = await fileIdentity(item.file);
    if (local.sha256 !== item.sha256 || local.size !== item.size) throw new Error('Local artifact changed after freeze validation');
    const existing = await transport.head(item.key);
    if (existing && !objectMatches(existing, item)) throw new Error(`Existing immutable object differs: ${item.name}`);
    if (existing) await verifyPublicDownload(item, transport.fetch?.bind(transport));
    else missing.push(item);
  }
  for (const item of missing) await transport.putImmutable(item);
  for (const item of plan.artifacts) {
    if (!objectMatches(await transport.head(item.key), item)) throw new Error(`R2 upload verification failed: ${item.name}`);
    await verifyPublicDownload(item, transport.fetch?.bind(transport));
  }
  const contents = `${JSON.stringify(plan.catalog, null, 2)}\n`;
  await transport.putCatalog(contents, previousCatalog?.ETag ?? null);
  const expected = { size: Buffer.byteLength(contents), sha256: createHash('sha256').update(contents).digest('hex') };
  if (!objectMatches(await transport.head(catalogKey), expected)) throw new Error('Catalog write outcome could not be verified; inspect before retrying');
  try {
    await verifyPublicDownload({ ...expected, key: catalogKey, name: 'downloads.json', url: `${publicBase}/${catalogKey}`, contentType: 'application/json' }, transport.fetch?.bind(transport));
  } catch { throw new Error('Public catalog verification failed after promotion; inspect before retrying'); }
  return plan.catalog;
}

function safeKey(key) {
  if (key === catalogKey) return;
  const match = /^mobile-egress\/([^/]+)\/([^/]+)$/.exec(key);
  if (!match) throw new Error('Unsafe R2 object key');
  checkVersion(match[1]);
  if (!platforms.some(p => definition(match[1], p).name === match[2])) throw new Error('Unexpected R2 artifact name');
}

export function createR2Transport(config, dependencies = {}) {
  const command = dependencies.run ?? run;
  const aws = args => command('aws', [...args, '--endpoint-url', config.endpoint, '--region', 'auto', '--no-cli-pager']);
  async function put(file, key, sha256, type, condition, immutable) {
    safeKey(key);
    const args = ['s3api', 'put-object', '--bucket', config.bucket, '--key', key, '--body', file,
      '--metadata', `sha256=${sha256}`, '--content-type', type, '--cache-control', immutable ? immutableCache : 'no-store', ...condition];
    if (immutable) args.push('--content-disposition', `attachment; filename="${key.split('/').at(-1)}"`);
    try { await aws(args); } catch { throw new Error(`R2 conditional upload failed for ${key}; inspect object state before retrying`); }
  }
  return {
    async head(key) {
      safeKey(key);
      try { return JSON.parse(await aws(['s3api', 'head-object', '--bucket', config.bucket, '--key', key, '--output', 'json'])); }
      catch (error) {
        if (/\((404|NoSuchKey|NotFound)\)/.test(String(error.stderr))) return undefined;
        throw new Error('Could not inspect R2 object; upload stopped');
      }
    },
    async putImmutable(item) { await put(item.file, item.key, item.sha256, item.contentType, ['--if-none-match', '*'], true); },
    async putCatalog(contents, previousETag) {
      const directory = await mkdtemp(join(tmpdir(), 'mobile-egress-r2-catalog-'));
      try {
        const file = join(directory, 'downloads.json'); await writeFile(file, contents, { mode: 0o600, flag: 'wx' });
        await put(file, catalogKey, createHash('sha256').update(contents).digest('hex'), 'application/json',
          previousETag === null ? ['--if-none-match', '*'] : ['--if-match', previousETag], false);
      } finally { await rm(directory, { recursive: true, force: true }); }
    },
    fetch,
    async checkConditionalSupport() {
      try {
        const skeleton = JSON.parse(await command('aws', ['s3api', 'put-object', '--generate-cli-skeleton', 'input']));
        if (!Object.hasOwn(skeleton, 'IfMatch') || !Object.hasOwn(skeleton, 'IfNoneMatch')) throw new Error();
      } catch { throw new Error('AWS CLI with conditional PutObject support is required; do not fall back to unconditional copy'); }
    },
  };
}

export function parseArguments(args) {
  const options = { publish: false, pilot: false, dryRun: false };
  for (let i = 0; i < args.length; i++) {
    const arg = args[i];
    if (arg === '--plan' && !options.plan && args[i + 1] && !args[i + 1].startsWith('--')) options.plan = args[++i];
    else if (arg === '--publish' && !options.publish) options.publish = true;
    else if (arg === '--dry-run' && !options.dryRun) options.dryRun = true;
    else if (arg === '--pilot' && !options.pilot) options.pilot = true;
    else throw new Error('Unknown or duplicate argument. Use --plan FILE [--dry-run | --publish --pilot]');
  }
  if (!options.plan || options.publish && options.dryRun) throw new Error('Use --plan FILE [--dry-run | --publish --pilot]');
  if (options.publish && !options.pilot) throw new Error('Publishing requires explicit --pilot confirmation');
  return options;
}

export async function verifyPublishedRelease(plan, repositoryRoot, command = run) {
  let origin;
  try { origin = await command('git', ['-C', repositoryRoot, 'remote', 'get-url', 'origin']); } catch { throw new Error('Cannot verify repository origin'); }
  if (![`https://github.com/${repository}.git`, `https://github.com/${repository}`, `git@github.com:${repository}.git`].includes(origin)) throw new Error('Unexpected repository origin');
  for (const version of new Set(plan.artifacts.map(a => a.version))) {
    const selected = plan.artifacts.filter(a => a.version === version); let release, refs;
    try {
      release = JSON.parse(await command('gh', ['release', 'view', `v${version}`, '--repo', repository, '--json', 'tagName,isDraft,isPrerelease,assets']));
      refs = await command('git', ['-C', repositoryRoot, 'ls-remote', '--tags', 'origin', `refs/tags/v${version}`, `refs/tags/v${version}^{}`]);
    } catch { throw new Error('Could not verify the published GitHub release and remote tag'); }
    const lines = refs.split('\n').map(line => line.trim().split(/\s+/));
    const target = lines.find(([, ref]) => ref === `refs/tags/v${version}^{}`) ?? lines.find(([, ref]) => ref === `refs/tags/v${version}`);
    if (target?.[0] !== selected[0].sourceCommit || release.tagName !== `v${version}` || release.isDraft || !release.isPrerelease) throw new Error('Published pilot release/source mismatch');
    for (const item of selected) {
      const matches = release.assets.filter(a => a.name === item.name);
      if (matches.length !== 1 || matches[0].state !== 'uploaded' || matches[0].size !== item.size || matches[0].digest !== `sha256:${item.sha256}`) throw new Error(`Published artifact differs from frozen bytes: ${item.name}`);
    }
  }
}

async function main() {
  const options = parseArguments(process.argv.slice(2));
  const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
  const requestFile = resolve(options.plan);
  const request = await readJSONFile(requestFile, 'download plan');
  const plan = await prepareDownloads({ repositoryRoot, request,
    resolveTag: async tag => {
      try { return await run('git', ['-C', repositoryRoot, 'rev-parse', '--verify', `${tag}^{commit}`]); }
      catch { throw new Error('Frozen local release tag is missing'); }
    } });
  if (!options.publish) {
    console.log(JSON.stringify({ dryRun: true, localFreezeAndTagVerified: true, remoteObjectsChecked: false, catalog: plan.catalog }, null, 2));
    return;
  }
  await verifyPublishedRelease(plan, repositoryRoot);
  const config = downloadConfig(process.env);
  const transport = createR2Transport(config);
  await transport.checkConditionalSupport();
  // Snapshot already verified signed bytes. Never read signing keys, rebuild, or re-sign.
  const directory = await mkdtemp(join(tmpdir(), 'mobile-egress-r2-publish-'));
  try {
    const copies = [];
    for (const item of plan.artifacts) {
      const target = join(directory, item.version, item.name); await mkdir(dirname(target), { recursive: true, mode: 0o700 });
      await copyFile(item.file, target);
      const identity = await fileIdentity(target);
      if (identity.sha256 !== item.sha256 || identity.size !== item.size) throw new Error('Snapshot changed from verified release bytes');
      copies.push({ ...item, file: target });
    }
    await publishDownloads({ ...plan, artifacts: copies }, transport);
    console.log(JSON.stringify({ published: true, channel: 'pilot', catalogUrl: `${publicBase}/${catalogKey}`, platforms: plan.catalog.platforms }, null, 2));
  } finally { await rm(directory, { recursive: true, force: true }); }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch(error => { console.error(`Mobile Egress download publication stopped: ${error.message}`); process.exitCode = 1; });
}
