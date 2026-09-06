import test from 'node:test';
import assert from 'node:assert/strict';
import { once } from 'node:events';
import { createServer } from 'node:net';
import { Agent, get } from 'node:https';
import { connect } from 'node:http2';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { startFixture } from '../fixture.mjs';

test('HTTPS H1 and H2 requests retain their actual connection telemetry identity', { timeout: 10000 }, async () => {
  const directory = mkdtempSync(join(tmpdir(), 'browser-load-tls-'));
  const telemetryPath = join(directory, 'events.jsonl');
  const ca = readFileSync(new URL('fixtures/localhost.crt', import.meta.url));
  const reservations = Array.from({ length: 3 }, () => createServer());
  let servers = [];
  const sockets = new Set();
  const agent = new Agent({ keepAlive: true, maxSockets: 1, ca, rejectUnauthorized: true });
  let session;
  try {
    for (const server of reservations) { server.listen(0, '127.0.0.1'); await once(server, 'listening'); }
    const origins = reservations.map(server => `https://127.0.0.1:${server.address().port}`);
    await Promise.all(reservations.map(server => new Promise(resolve => server.close(resolve))));
    servers = startFixture({ origins, telemetryPath, keyPath: new URL('fixtures/localhost.test-key', import.meta.url), certPath: new URL('fixtures/localhost.crt', import.meta.url) });
    for (const server of servers) server.on('secureConnection', socket => {
      sockets.add(socket);
      socket.once('close', () => sockets.delete(socket));
    });
    await Promise.all(servers.map(server => once(server, 'listening')));

    for (let id = 0; id < 2; id++) {
      const body = await new Promise((resolve, reject) => {
        get(`${origins[0]}/api/${id}`, { agent, ALPNProtocols: ['http/1.1'] }, response => {
          assert.equal(response.socket.authorized, true);
          assert.equal(response.httpVersion, '1.1');
          let text = '';
          response.setEncoding('utf8');
          response.on('data', chunk => { text += chunk; });
          response.on('end', () => resolve(text));
          response.on('error', reject);
        }).on('error', reject);
      });
      assert.equal(JSON.parse(body).id, id);
    }

    session = connect(origins[1], { ca, rejectUnauthorized: true });
    await once(session, 'connect');
    assert.equal(session.socket.authorized, true);
    assert.equal(session.alpnProtocol, 'h2');
    for (let id = 0; id < 2; id++) {
      const request = session.request({ ':path': `/api/${id}` });
      let body = '';
      request.setEncoding('utf8');
      request.on('data', chunk => { body += chunk; });
      request.end();
      await once(request, 'end');
      assert.equal(JSON.parse(body).id, id);
    }

    const events = readFileSync(telemetryPath, 'utf8').trim().split('\n').map(JSON.parse);
    const opens = events.filter(event => event.type === 'connection-open');
    assert.equal(opens.length, 2);
    for (const [version, alpn] of [['1.1', 'http/1.1'], ['2.0', 'h2']]) {
      const requests = events.filter(event => event.type === 'request' && event.httpVersion === version);
      assert.equal(requests.length, 2);
      const opened = opens.find(event => event.alpn === alpn);
      assert.ok(opened);
      assert.equal(requests[0].connectionId, opened.connectionId);
      assert.equal(requests[1].connectionId, opened.connectionId);
    }
  } finally {
    agent.destroy();
    session?.destroy();
    await Promise.all([...sockets].map(socket => new Promise(resolve => {
      socket.once('close', resolve);
      socket.destroy();
    })));
    await Promise.all([...servers, ...reservations].map(server => new Promise(resolve => server.close(resolve))));
    rmSync(directory, { recursive: true, force: true });
  }
});
