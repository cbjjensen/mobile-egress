import test from 'node:test';
import assert from 'node:assert/strict';
import { matrix, summarize, validateConfig } from '../metrics.mjs';
import { responseFor, startFixture } from '../fixture.mjs';

test('paired matrix covers both protocols, five profiles, and three pairs', () => {
  const tasks = matrix();
  assert.equal(tasks.length, 60);
  assert.equal(new Set(tasks.map(t => t.id)).size, 60);
  for (const contexts of [1, 10, 25, 50, 100]) {
    for (const protocol of ['h1', 'h2']) {
      assert.equal(tasks.filter(t => t.contexts === contexts && t.protocol === protocol).length, 6);
    }
  }
  assert.ok(tasks.every(t => t.warmupSeconds === 60 && t.measureSeconds === 180));
});

test('throughput excludes late completions while qualification includes the measured start cohort', () => {
  const report = summarize([
    { ok: true, durationMs: 100, finishedMs: 100 },
    { ok: true, durationMs: 200, finishedMs: 200 },
    { ok: true, durationMs: 300, finishedMs: 180100 },
    { ok: false, durationMs: 30000, finishedMs: 30000 },
  ], { requests: 90, bytes: 9000, failures: 1, connectionIds: ['1', '1', '2'], protocols: ['h2'] }, 180);
  assert.equal(report.pagesPerMinute, 2 / 3);
  assert.equal(report.requestsPerSecond, 0.5);
  assert.equal(report.successFraction, 0.75);
  assert.equal(report.navigationMedianMs, 200);
  assert.equal(report.navigationP95Ms, 300);
  assert.equal(report.observedBrowserConnections, 2);
  assert.equal(report.qualification, 'failed');
  assert.equal(report.activeAgentStreams, null);
});

test('passing browser traffic cannot claim phone qualification without external telemetry', () => {
  const report = summarize([{ ok: true, durationMs: 10, finishedMs: 10 }], {}, 180);
  assert.equal(report.navigationThresholdMet, true);
  assert.equal(report.qualification, 'unverified');
  assert.equal(report.sessionResets, null);
  assert.equal(report.queueOccupancy, null);
  assert.equal(report.phoneMemory, null);
});

test('warmup crossing completions count for throughput but not qualification and measured tails do the reverse', () => {
  const report = summarize([
    { ok: true, startedMs: -100, durationMs: 200, finishedMs: 100 },
    { ok: false, startedMs: -50, durationMs: 200, finishedMs: 150 },
    { ok: true, startedMs: 1000, durationMs: 500, finishedMs: 1500 },
    { ok: true, startedMs: 179900, durationMs: 300, finishedMs: 180200 },
    { ok: true, startedMs: -1000, durationMs: 100, finishedMs: -900 },
  ], {}, 180);
  assert.equal(report.pagesPerMinute, 2 / 3);
  assert.equal(report.navigationsStarted, 2);
  assert.equal(report.navigationsSucceeded, 2);
  assert.equal(report.successFraction, 1);
  assert.equal(report.navigationFailures, 0);
  assert.equal(report.navigationP95Ms, 500);
});

test('configuration requires HTTPS origins and authenticated local HTTP proxy', () => {
  const config = { origins: ['https://shop.example:8443', 'https://shop.example:8444', 'https://shop.example:8445'], proxy: 'http://127.0.0.1:8080', phoneId: 'lab-phone', buildId: 'abc', variant: 'baseline' };
  assert.doesNotThrow(() => validateConfig(config, { PROXY_USERNAME: 'user', PROXY_PASSWORD: 'secret' }));
  assert.doesNotThrow(() => validateConfig({ ...config, proxy: 'http://127.0.0.2:1081' }, { PROXY_USERNAME: 'user', PROXY_PASSWORD: 'secret' }));
  assert.throws(() => validateConfig({ ...config, origins: ['http://example.com'] }, {}));
  assert.throws(() => validateConfig({ ...config, proxy: 'http://public.example:8080' }, {}));
  assert.throws(() => validateConfig(config, {}));
  assert.throws(() => validateConfig({ ...config, origins: ['https://localhost:8443', 'https://localhost:8444', 'https://localhost:8445'] }, { PROXY_USERNAME: 'u', PROXY_PASSWORD: 'p' }));
});

test('fixture rejects repeated listener ports before reading keys or opening sockets', () => {
  assert.throws(() => startFixture({ origins: ['https://one.example:8443', 'https://two.example:8443', 'https://three.example:8445'] }), /distinct listener ports/);
});

test('retail fixture serves multiple origins, scripts, images, and validated API data', () => {
  const origins = ['https://shop.example:8443', 'https://shop.example:8444', 'https://shop.example:8445'];
  const page = responseFor('/', origins);
  assert.equal(page.status, 200);
  for (const origin of origins) assert.ok(page.body.toString().includes(origin));
  assert.ok(page.body.toString().includes('window.fixtureComplete'));
  assert.ok(page.body.toString().includes('integrity='));
  assert.equal(responseFor('/asset/0.svg', origins).type, 'image/svg+xml');
  const api = JSON.parse(responseFor('/api/0', origins).body);
  assert.equal(api.padding.length, 2048);
  assert.equal(api.id, 0);
  assert.equal(responseFor('/unknown', origins).status, 404);
});
