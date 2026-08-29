import { FormEvent, useCallback, useEffect, useState } from 'react'
import { api, Pairing, Status } from './api'

const emptyStatus: Status = {
  paired: false, running: false, relay: 'offline', agentAvailable: false,
  activeStreams: 0, bytesUp: 0, bytesDown: 0, port: 1080,
}

function readableBytes(value: number) {
  if (value < 1024) return `${value} B`
  if (value < 1024 ** 2) return `${(value / 1024).toFixed(1)} KB`
  return `${(value / 1024 ** 2).toFixed(1)} MB`
}

export default function App() {
  const [status, setStatus] = useState<Status>(emptyStatus)
  const [tab, setTab] = useState<'proxy' | 'owner'>('proxy')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [pairing, setPairing] = useState<Pairing | null>(null)

  const refresh = useCallback(async () => {
    try { setStatus(await api().GetStatus()) } catch (reason) { setError(String(reason)) }
  }, [])

  useEffect(() => {
    void refresh()
    const timer = window.setInterval(() => void refresh(), 2000)
    return () => window.clearInterval(timer)
  }, [refresh])

  async function action(work: () => Promise<void>) {
    setBusy(true); setError('')
    try { await work(); await refresh() } catch (reason) { setError(String(reason)) } finally { setBusy(false) }
  }

  async function pair(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const data = new FormData(event.currentTarget)
    await action(() => api().Pair(String(data.get('bundle'))))
  }

  async function toggleProxy(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (status.running) return action(() => api().StopProxy())
    const port = Number(new FormData(event.currentTarget).get('port'))
    await action(() => api().StartProxy(port))
  }

  async function copyProxy() {
    await action(async () => { await navigator.clipboard.writeText(await api().ProxyLine()) })
  }

  async function issue(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setBusy(true); setError('')
    try { setPairing(await api().IssuePairing(String(new FormData(event.currentTarget).get('role')))) }
    catch (reason) { setError(String(reason)) } finally { setBusy(false) }
  }

  async function revoke(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const serial = String(new FormData(event.currentTarget).get('serial'))
    await action(() => api().Revoke(serial))
    event.currentTarget.reset()
  }

  const ready = status.relay === 'connected' && status.agentAvailable
  return <main className="shell">
    <header>
      <div><p className="eyebrow">Selective cellular route</p><h1>Mobile Egress</h1></div>
      <div className={`health ${ready ? 'ready' : ''}`}><span />{ready ? 'Agent ready' : status.relay === 'connected' ? 'Agent offline' : 'Relay offline'}</div>
    </header>
    <nav>
      <button className={tab === 'proxy' ? 'active' : ''} onClick={() => setTab('proxy')}>Proxy</button>
      <button className={tab === 'owner' ? 'active' : ''} onClick={() => setTab('owner')}>Owner</button>
    </nav>
    {error && <div className="error" role="alert">{error}</div>}

    {tab === 'proxy' && <section className="stack">
      {!status.paired ? <article className="card">
        <h2>Pair this Windows device</h2><p>Import the owner-provided bundle containing the relay address, pinned CA, and one-time capability.</p>
        <form onSubmit={pair}>
          <label>Pairing bundle<textarea name="bundle" required rows={5} autoComplete="off" spellCheck={false} /></label>
          <button className="primary" disabled={busy}>Pair securely</button>
        </form>
      </article> : <>
        <div className="metric-grid">
          <article className="metric"><span>Active</span><strong>{status.activeStreams} / 4</strong></article>
          <article className="metric"><span>Uploaded</span><strong>{readableBytes(status.bytesUp)}</strong></article>
          <article className="metric"><span>Downloaded</span><strong>{readableBytes(status.bytesDown)}</strong></article>
        </div>
        <article className="card">
          <div className="row"><div><h2>Loopback SOCKS5</h2><p>Authenticated and fixed to 127.0.0.1.</p></div><span className={`pill ${status.running ? 'on' : ''}`}>{status.running ? 'Running' : 'Stopped'}</span></div>
          {status.role === 'client' ? <form className="inline" onSubmit={toggleProxy}>
            <label>Port<input name="port" type="number" min="1" max="65535" defaultValue={status.port ?? 1080} disabled={status.running} /></label>
            <button className="primary" disabled={busy}>{status.running ? 'Stop proxy' : 'Start proxy'}</button>
          </form> : <p className="note">Owner identities manage devices. Pair a client identity to run the local proxy.</p>}
          <div className="copyline"><code>{status.proxy ?? 'socks5://***:***@127.0.0.1:1080'}</code><button onClick={() => void copyProxy()} disabled={busy}>Copy with credentials</button></div>
        </article>
      </>}
    </section>}

    {tab === 'owner' && <section className="stack">
      {status.role !== 'owner' ? <article className="card"><h2>Owner identity required</h2><p>Pair this app with the relay's owner capability to issue or revoke device access.</p></article> : <>
        <article className="card"><h2>Issue pairing capability</h2>
          <form className="inline" onSubmit={issue}><label>Role<select name="role"><option value="agent">Android agent</option><option value="client">Windows client</option></select></label><button className="primary" disabled={busy}>Issue</button></form>
          {pairing && <div className="issued"><code>{pairing.bundle}</code><button onClick={() => void navigator.clipboard.writeText(pairing.bundle)}>Copy secure bundle</button><small>Expires {new Date(pairing.expiresAt).toLocaleString()}</small></div>}
        </article>
        <article className="card danger"><h2>Revoke device</h2><p>Revocation closes that identity's active relay session.</p>
          <form className="inline" onSubmit={revoke}><label>Certificate serial<input name="serial" required pattern="[0-9A-Fa-f]+" /></label><button disabled={busy}>Revoke</button></form>
        </article>
      </>}
    </section>}
    <footer>Closing this window keeps Mobile Egress in the tray. Quit from the tray to stop the proxy.</footer>
  </main>
}
