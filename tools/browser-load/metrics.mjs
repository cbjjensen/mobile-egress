export function matrix() {
  const tasks = [];
  for (const contexts of [1, 10, 25, 50, 100]) for (const protocol of ['h1', 'h2']) {
    for (let pair = 1; pair <= 3; pair++) {
      // Alternate order to limit a systematic baseline-first temperature bias.
      for (const variant of pair === 2 ? ['updated', 'baseline'] : ['baseline', 'updated']) {
        tasks.push({ id: `${variant}-${protocol}-${contexts}-p${pair}`, variant, protocol, contexts, pair, warmupSeconds: 60, measureSeconds: 180 });
      }
    }
  }
  return tasks;
}

export function validateConfig(config, env = process.env) {
  if (!Array.isArray(config.origins) || config.origins.length < 3 || new Set(config.origins).size < 3) throw Error('Three distinct HTTPS origins required');
  for (const origin of config.origins) {
    const url = new URL(origin);
    if (url.protocol !== 'https:' || url.origin !== origin || url.username || url.password) throw Error('Use bare HTTPS origins without credentials');
    if (url.hostname === 'localhost' || url.hostname.endsWith('.localhost') || url.hostname === '[::1]' || url.hostname.startsWith('127.')) throw Error('Loopback origins bypass Chromium proxies; use the public fixture');
  }
  const proxy = new URL(config.proxy);
  const loopback = ['localhost', '[::1]'].includes(proxy.hostname) || (isIP(proxy.hostname) === 4 && proxy.hostname.startsWith('127.'));
  if (proxy.protocol !== 'http:' || !loopback || proxy.username || proxy.password) throw Error('An explicit local HTTP proxy is required');
  if (!env.PROXY_USERNAME || !env.PROXY_PASSWORD) throw Error('Set PROXY_USERNAME and PROXY_PASSWORD');
  if (!config.phoneId || !config.buildId || !['baseline', 'updated'].includes(config.variant)) throw Error('phoneId, buildId, and baseline/updated variant required');
}

export function summarize(navigations, network, seconds) {
  const cohort = navigations.filter(n => (n.startedMs ?? 0) >= 0 && (n.startedMs ?? 0) < seconds * 1000);
  const successes = cohort.filter(n => n.ok && n.durationMs <= 30000);
  const windowCompletions = navigations.filter(n => n.ok && n.durationMs <= 30000 && n.finishedMs >= 0 && n.finishedMs <= seconds * 1000);
  const durations = successes.map(n => n.durationMs).sort((a, b) => a - b);
  const quantile = p => durations.length ? durations[Math.ceil(p * durations.length) - 1] : null;
  const successFraction = cohort.length ? successes.length / cohort.length : 0;
  const navigationThresholdMet = cohort.length > 0 && successFraction >= 0.99;
  return {
    schemaVersion: 1,
    measuredSeconds: seconds, navigationsStarted: cohort.length, navigationsSucceeded: successes.length,
    pagesPerMinute: windowCompletions.length * 60 / seconds,
    requestsPerSecond: (network.requests ?? 0) / seconds, responseBytes: network.bytes ?? 0,
    navigationMedianMs: quantile(0.5), navigationP95Ms: quantile(0.95), successFraction,
    navigationFailures: cohort.length - successes.length, requestFailures: network.failures ?? 0,
    observedBrowserConnections: new Set(network.connectionIds ?? []).size,
    observedProtocols: [...new Set(network.protocols ?? [])],
    navigationThresholdMet, qualification: navigationThresholdMet ? 'unverified' : 'failed',
    activeAgentStreams: null, sessionResets: null, queueOccupancy: null, phoneMemory: null,
    unverified: ['stable connectivity', 'actual proxy/Agent active streams', 'avoidable session resets', 'queue occupancy', 'phone/relay memory and sustained leak', 'end-to-end content integrity beyond fixture checks'],
  };
}
import { isIP } from 'node:net';
