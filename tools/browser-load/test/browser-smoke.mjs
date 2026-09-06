// Fixture rendering smoke only: loopback HTTP, no Agent, relay, proxy, or TLS claim.
import { createServer } from 'node:http';
import { once } from 'node:events';
import assert from 'node:assert/strict';
import { chromium } from 'playwright';
import { responseFor } from '../fixture.mjs';

const origins = [];
const servers = [];
let browser;
try {
  for (let index = 0; index < 3; index++) {
    const server = createServer((request, response) => {
      const result = responseFor(request.url, origins);
      response.writeHead(result.status, { 'content-type': result.type, 'access-control-allow-origin': '*', 'cache-control': 'no-store' });
      response.end(result.body);
    });
    server.listen(0, '127.0.0.1');
    await once(server, 'listening');
    servers.push(server);
    origins.push(`http://127.0.0.1:${server.address().port}`);
  }
  browser = await chromium.launch({ headless: true });
  const context = await browser.newContext();
  const page = await context.newPage();
  let requests = 0;
  page.on('requestfinished', () => requests++);
  for (let navigation = 0; navigation < 2; navigation++) {
    await page.goto(origins[0]);
    await page.waitForFunction(() => window.fixtureComplete || window.fixtureError);
    assert.equal(await page.evaluate(() => window.fixtureError), null);
    assert.equal(await page.evaluate(() => window.fixtureComplete), true);
    assert.equal(await page.locator('img').count(), 24);
  }
  assert.ok(requests >= 74);
  console.log(JSON.stringify({ smoke: 'loopback HTTP fixture rendering only', chromium: browser.version(), navigations: 2, requests, phoneQualification: 'unverified' }));
  await context.close();
} finally {
  await browser?.close();
  await Promise.all(servers.map(server => new Promise(resolve => server.close(resolve))));
}
