import { chromium } from 'playwright';
import { appendFileSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { createHash, randomUUID } from 'node:crypto';
import { resolve } from 'node:path';
import { matrix, summarize, validateConfig } from './metrics.mjs';

if (process.argv[2] === '--plan') {
  console.log(JSON.stringify({ schemaVersion: 1, tasks: matrix(), soak: { measureSeconds: 1800, warmupSeconds: 60, contexts: 'highest qualifying profile', paired: true } }, null, 2));
  process.exit(0);
}
const config = JSON.parse(readFileSync(process.argv[2], 'utf8'));
validateConfig(config);
const taskId = process.argv[3];
let task = matrix().find(t => t.id === taskId);
if (taskId === 'soak') {
  if (![1, 10, 25, 50, 100].includes(config.soakContexts) || !['h1', 'h2'].includes(config.soakProtocol) || !config.qualificationEvidence) throw Error('Soak requires highest qualifying profile, protocol, and qualificationEvidence path');
  task = { id: 'soak', variant: config.variant, contexts: config.soakContexts, protocol: config.soakProtocol, warmupSeconds: 60, measureSeconds: 1800 };
}
if (!task || task.variant !== config.variant) throw Error('Choose a plan task matching the installed baseline/updated variant');
const output = resolve(config.outputDirectory ?? 'results', `${new Date().toISOString().replaceAll(':', '-')}-${task.id}-${randomUUID()}`);
mkdirSync(output, { recursive: true });
const write = (file, value) => writeFileSync(resolve(output, file), JSON.stringify(value, null, 2) + '\n');
const append = (file, value) => appendFileSync(resolve(output, file), JSON.stringify(value) + '\n');
const hashFile = file => createHash('sha256').update(readFileSync(file)).digest('hex');
const packageLock = JSON.parse(readFileSync(new URL('package-lock.json', import.meta.url)));
const metadata = {
  schemaVersion: 1, runId: output.split(/[\\/]/).at(-1), task, phoneId: config.phoneId,
  buildId: config.buildId, fixtureId: config.fixtureId ?? null, origins: config.origins,
  proxy: config.proxy, playwrightVersion: packageLock.packages['node_modules/playwright'].version,
  lockfileSha256: hashFile(new URL('package-lock.json', import.meta.url)),
  fixtureSha256: hashFile(new URL('fixture.mjs', import.meta.url)),
  contextPolicy: 'fresh at start; recreate every ten navigations; repeat navigation otherwise',
  tlsVerification: true, externalTelemetry: config.telemetryPath ? { sha256: hashFile(config.telemetryPath) } : null,
  qualificationEvidence: config.qualificationEvidence ? { sha256: hashFile(config.qualificationEvidence) } : null,
};
write('metadata.json', metadata);

const browser = await chromium.launch({
  headless: true,
  proxy: { server: config.proxy, username: process.env.PROXY_USERNAME, password: process.env.PROXY_PASSWORD },
  args: ['--disable-quic', ...(task.protocol === 'h1' ? ['--disable-http2'] : [])],
});
metadata.chromiumVersion = browser.version();
metadata.startedAt = new Date().toISOString();
write('metadata.json', metadata);
const started = performance.now();
const measureStart = started + task.warmupSeconds * 1000;
const measureEnd = measureStart + task.measureSeconds * 1000;
const measuring = () => performance.now() >= measureStart && performance.now() < measureEnd;
const navigations = [];
const network = { requests: 0, bytes: 0, failures: 0, connectionIds: new Set(), protocols: new Set() };
let fatalError = null;
const sample = setInterval(() => append('runner-memory.jsonl', { at: new Date().toISOString(), measured: measuring(), rssBytes: process.memoryUsage().rss, heapUsedBytes: process.memoryUsage().heapUsed }), 1000);

async function worker(workerId) {
  let context;
  let page;
  let iteration = 0;
  let generation = 0;
  try {
    while (performance.now() < measureEnd) {
      if (iteration % 10 === 0) {
        await context?.close();
        context = await browser.newContext({ ignoreHTTPSErrors: false, serviceWorkers: 'block' });
        page = await context.newPage();
        generation++;
        const cdp = await context.newCDPSession(page);
        await cdp.send('Network.enable');
        cdp.on('Network.loadingFinished', event => {
          if (!measuring()) return;
          network.requests++;
          network.bytes += event.encodedDataLength;
        });
        cdp.on('Network.loadingFailed', event => {
          if (!measuring()) return;
          network.failures++;
          append('request-failures.jsonl', { at: new Date().toISOString(), workerId, error: event.errorText, canceled: event.canceled ?? false });
        });
        cdp.on('Network.responseReceived', event => {
          if (!measuring()) return;
          const response = event.response;
          network.connectionIds.add(String(response.connectionId));
          network.protocols.add(response.protocol);
          if (response.status >= 400) network.failures++;
          append('connections.jsonl', { at: new Date().toISOString(), workerId, generation, connectionId: response.connectionId, reused: response.connectionReused, protocol: response.protocol, status: response.status });
        });
      }
      if (performance.now() >= measureEnd) break;
      const navigationStart = performance.now();
      let ok = false;
      let error = null;
      try {
        const response = await page.goto(`${config.origins[0]}/?worker=${workerId}&iteration=${iteration}`, { timeout: 30000, waitUntil: 'load' });
        if (!response?.ok()) throw Error('navigation-status');
        const remaining = Math.max(1, 30000 - (performance.now() - navigationStart));
        await page.waitForFunction(() => window.fixtureComplete || window.fixtureError, null, { timeout: remaining });
        const fixtureError = await page.evaluate(() => window.fixtureError);
        if (fixtureError) throw Error(`fixture-${fixtureError}`);
        ok = true;
      } catch (caught) { error = caught.name === 'TimeoutError' ? 'navigation-timeout' : caught.message.split('\n')[0]; }
      const row = { workerId, generation, iteration, freshContext: iteration % 10 === 0, ok, error, durationMs: performance.now() - navigationStart, startedMs: navigationStart - measureStart, finishedMs: performance.now() - measureStart };
      if (row.finishedMs >= 0) { navigations.push(row); append('navigations.jsonl', row); }
      iteration++;
    }
  } finally { await context?.close(); }
}

try {
  const results = await Promise.allSettled(Array.from({ length: task.contexts }, (_, id) => worker(id)));
  const failed = results.find(result => result.status === 'rejected');
  if (failed) fatalError = failed.reason?.message ?? 'worker-failed';
} finally {
  clearInterval(sample);
  await browser.close();
}
const report = summarize(navigations, { ...network, connectionIds: [...network.connectionIds], protocols: [...network.protocols] }, task.measureSeconds);
report.protocolVerified = report.observedProtocols.length === 1 && report.observedProtocols[0] === (task.protocol === 'h2' ? 'h2' : 'http/1.1');
if (!report.protocolVerified || fatalError) report.qualification = 'failed';
report.fatalError = fatalError;
report.finishedAt = new Date().toISOString();
write('summary.json', report);
console.log(`Results: ${output}`);
console.log(JSON.stringify({ qualification: report.qualification, pagesPerMinute: report.pagesPerMinute, successFraction: report.successFraction, protocolVerified: report.protocolVerified }));
if (report.qualification === 'failed') process.exitCode = 1;
