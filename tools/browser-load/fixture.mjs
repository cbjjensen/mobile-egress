import { createHash } from 'node:crypto';
import { createSecureServer } from 'node:http2';
import { readFileSync, appendFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';

const script = id => `window.fixtureScripts=(window.fixtureScripts||[]);window.fixtureScripts.push(${id});`;
const sha = body => createHash('sha256').update(body).digest('base64');

export function responseFor(path, origins) {
  const name = path.split('?')[0];
  let type = 'text/html; charset=utf-8';
  let body;
  let status = 200;
  if (name === '/') {
    const scripts = Array.from({ length: 6 }, (_, id) => `<script src="${origins[id % origins.length]}/script/${id}.js" integrity="sha256-${sha(script(id))}" crossorigin="anonymous"></script>`).join('');
    const images = Array.from({ length: 24 }, (_, id) => `<img width="120" height="120" src="${origins[id % origins.length]}/asset/${id}.svg" alt="Product ${id}">`).join('');
    body = `<!doctype html><html><head><title>Synthetic retail load fixture</title><link rel="icon" href="data:,">${scripts}</head><body><h1>Controlled catalog</h1>${images}<script>
window.fixtureComplete=false;
window.fixtureError=null;
window.addEventListener('load', async () => {
  try {
    if (window.fixtureScripts?.length !== 6 || new Set(window.fixtureScripts).size !== 6) throw Error('script-integrity');
    await Promise.all(Array.from(document.images, image => image.decode()));
    const origins=${JSON.stringify(origins)};
    await Promise.all(Array.from({length:6}, async (_, id) => {
      const response=await fetch(origins[id % origins.length]+'/api/'+id);
      if (!response.ok) throw Error('api-status');
      const data=await response.json();
      if(data.id!==id || data.padding!=='x'.repeat(2048)) throw Error('api-integrity');
    }));
    window.fixtureComplete=true;
  } catch(error) { window.fixtureError=error.message; }
});</script></body></html>`;
  } else if (/^\/script\/[0-5]\.js$/.test(name)) {
    type = 'text/javascript'; body = script(Number(name.match(/\d+/)[0]));
  } else if (/^\/asset\/(?:[0-9]|1[0-9]|2[0-3])\.svg$/.test(name)) {
    type = 'image/svg+xml';
    body = `<svg xmlns="http://www.w3.org/2000/svg" width="120" height="120"><metadata>${'controlled-fixture-'.repeat(2048)}</metadata><rect width="120" height="120" fill="#568a9f"/><text x="8" y="60">Item ${name.match(/\d+/)[0]}</text></svg>`;
  } else if (/^\/api\/[0-5]$/.test(name)) {
    type = 'application/json'; body = JSON.stringify({ id: Number(name.match(/\d+/)[0]), padding: 'x'.repeat(2048) });
  } else { status = 404; body = 'Not found'; }
  return { status, type, body: Buffer.from(body) };
}

export function startFixture(config) {
  if (!Array.isArray(config.origins) || config.origins.length < 3) throw Error('At least three origins required');
  const urls = config.origins.map(origin => new URL(origin));
  if (urls.some(url => url.protocol !== 'https:')) throw Error('HTTPS required');
  if (new Set(urls.map(url => Number(url.port || 443))).size !== urls.length) throw Error('Fixture origins require distinct listener ports');
  const key = readFileSync(config.keyPath);
  const cert = readFileSync(config.certPath);
  let nextConnection = 0;
  const sockets = new Map();
  // HTTP/2 exposes a proxy for the TLS socket on requests. The live endpoint tuple
  // identifies the same connection through both that proxy and secureConnection.
  const socketKey = socket => JSON.stringify([socket.localAddress, socket.localPort, socket.remoteAddress, socket.remotePort]);
  const log = event => { if (config.telemetryPath) appendFileSync(config.telemetryPath, JSON.stringify({ at: new Date().toISOString(), ...event }) + '\n'); };
  const servers = urls.map(url => {
    const server = createSecureServer({ key, cert, allowHTTP1: true }, (request, response) => {
      const result = responseFor(request.url, config.origins);
      response.writeHead(result.status, { 'content-type': result.type, 'content-length': result.body.length, 'cache-control': 'no-store', 'access-control-allow-origin': '*', 'timing-allow-origin': '*' });
      response.end(result.body);
      log({ type: 'request', connectionId: sockets.get(socketKey(request.socket)), httpVersion: request.httpVersion, bytes: result.body.length, status: result.status });
    });
    server.on('secureConnection', socket => {
      const connectionId = ++nextConnection;
      const key = socketKey(socket);
      sockets.set(key, connectionId);
      log({ type: 'connection-open', connectionId, alpn: socket.alpnProtocol, activeConnections: sockets.size });
      socket.on('close', () => { sockets.delete(key); log({ type: 'connection-close', connectionId, activeConnections: sockets.size }); });
    });
    server.on('sessionError', () => log({ type: 'http2-session-error' }));
    server.listen(Number(url.port || 443), config.bindHost ?? '127.0.0.1');
    return server;
  });
  return servers;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const config = JSON.parse(readFileSync(process.argv[2], 'utf8'));
  const servers = startFixture(config);
  for (const server of servers) server.on('listening', () => console.log(`Fixture HTTPS listener ready on port ${server.address().port}`));
}
