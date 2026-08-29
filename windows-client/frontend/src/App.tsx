import { FormEvent, useCallback, useEffect, useState } from 'react'
import { AgentQr, api, Status } from './api'

const emptyStatus: Status = {
  paired: false, ownerReady: false, clientReady: false, running: false, relay: 'offline', agentAvailable: false,
  activeStreams: 0, bytesUp: 0, bytesDown: 0, port: 1080,
}

function readableBytes(value: number) {
  if (value < 1024) return `${value} B`
  if (value < 1024 ** 2) return `${(value / 1024).toFixed(1)} KB`
  return `${(value / 1024 ** 2).toFixed(1)} MB`
}

export default function App() {
  const [status, setStatus] = useState<Status>(emptyStatus)
  const [tab, setTab] = useState<'setup' | 'phone' | 'proxy' | 'owner'>('setup')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [phoneQr, setPhoneQr] = useState<AgentQr | null>(null)

  const refresh = useCallback(async () => {
    try { setStatus(await api().GetStatus()) } catch { setError('Unable to refresh Mobile Egress. Please try again.') }
  }, [])

  useEffect(() => {
    void refresh()
    const timer = window.setInterval(() => void refresh(), 2000)
    return () => window.clearInterval(timer)
  }, [refresh])

  async function action(work: () => Promise<void>) {
    setBusy(true); setError('')
    try { await work(); await refresh(); return true }
    catch { setError('Unable to complete that action. Please try again.'); return false }
    finally { setBusy(false) }
  }

  useEffect(() => {
    if (!phoneQr) return
    const remaining = new Date(phoneQr.expiresAt).getTime() - Date.now()
    if (remaining <= 0) { setPhoneQr(null); return }
    const timer = window.setTimeout(() => setPhoneQr(null), remaining)
    return () => window.clearTimeout(timer)
  }, [phoneQr])

  async function bootstrapOwner(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const data = new FormData(event.currentTarget)
    await action(() => api().BootstrapOwner(String(data.get('bundle'))))
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

  async function issueAgentQr() {
    setBusy(true); setError('')
    try { setPhoneQr(await api().IssueAgentQr()) }
    catch { setError('Unable to create a phone pairing code. Please try again.') } finally { setBusy(false) }
  }

  async function revoke(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = event.currentTarget
    const serial = String(new FormData(form).get('serial'))
    if (await action(() => api().Revoke(serial))) form.reset()
  }

  const ready = status.relay === 'connected' && status.agentAvailable
  return <main className="shell">
    <header>
      <div><p className="eyebrow">Selective cellular route</p><h1>Mobile Egress</h1></div>
      <div className={`health ${ready ? 'ready' : ''}`}><span />{ready ? 'Agent ready' : status.relay === 'connected' ? 'Agent offline' : 'Relay offline'}</div>
    </header>
    <nav>
      <button className={tab === 'setup' ? 'active' : ''} onClick={() => setTab('setup')}>Setup</button>
      <button className={tab === 'phone' ? 'active' : ''} onClick={() => setTab('phone')}>Phone</button>
      <button className={tab === 'proxy' ? 'active' : ''} onClick={() => setTab('proxy')}>Proxy</button>
      <button className={tab === 'owner' ? 'active' : ''} onClick={() => setTab('owner')}>Owner</button>
    </nav>
    {error && <div className="error" role="alert">{error}</div>}

    {tab === 'setup' && <section className="stack">
      {!status.ownerReady ? <article className="card">
        <h2>Set up this Windows installation</h2><p>Paste the Owner invitation supplied by your relay administrator. It is used once to create this installation's Owner and Windows Client identities.</p>
        <form onSubmit={bootstrapOwner}>
          <label>Owner invitation<textarea name="bundle" required rows={5} autoComplete="off" spellCheck={false} /></label>
          <button className="primary" disabled={busy}>Set up securely</button>
        </form>
      </article> : !status.clientReady ? <article className="card">
        <h2>Finish Windows client setup</h2><p>The Owner identity is ready, but the local proxy client needs a fresh enrollment.</p>
        <button className="primary" onClick={() => void action(() => api().RetryClientSetup())} disabled={busy}>Retry Windows client setup</button>
      </article> : <article className="card"><h2>Windows setup complete</h2><p>Owner controls and the local Windows client are ready.</p></article>}
    </section>}

    {tab === 'phone' && <section className="stack">
      {!status.ownerReady ? <article className="card"><h2>Complete Owner setup first</h2><p>The Owner identity is required before a phone can be paired.</p></article> : <article className="card">
        <h2>Pair your Android phone</h2><p>Open Mobile Egress on your phone, choose Scan QR, and scan this short-lived code.</p>
        {phoneQr ? <div className="qr-card"><img src={phoneQr.imageDataUrl} alt="Phone pairing QR code" /><p>Expires {new Date(phoneQr.expiresAt).toLocaleTimeString()}. Generate a new code to replace it.</p><button onClick={() => void issueAgentQr()} disabled={busy}>Replace QR code</button></div> : <button className="primary" onClick={() => void issueAgentQr()} disabled={busy}>Generate phone QR code</button>}
      </article>}
    </section>}

    {tab === 'proxy' && <section className="stack">
      {!status.clientReady ? <article className="card"><h2>Finish Windows client setup first</h2><p>The local SOCKS5 proxy starts only after the Windows Client identity is ready.</p></article> : <>
        <div className="metric-grid">
          <article className="metric"><span>Active</span><strong>{status.activeStreams} / 4</strong></article>
          <article className="metric"><span>Uploaded</span><strong>{readableBytes(status.bytesUp)}</strong></article>
          <article className="metric"><span>Downloaded</span><strong>{readableBytes(status.bytesDown)}</strong></article>
        </div>
        <article className="card">
          <div className="row"><div><h2>Loopback SOCKS5</h2><p>Authenticated and fixed to 127.0.0.1.</p></div><span className={`pill ${status.running ? 'on' : ''}`}>{status.running ? 'Running' : 'Stopped'}</span></div>
          <form className="inline" onSubmit={toggleProxy}>
            <label>Port<input name="port" type="number" min="1" max="65535" defaultValue={status.port ?? 1080} disabled={status.running} /></label>
            <button className="primary" disabled={busy}>{status.running ? 'Stop proxy' : 'Start proxy'}</button>
          </form>
          <div className="copyline"><code>{status.proxy ?? 'socks5://***:***@127.0.0.1:1080'}</code><button onClick={() => void copyProxy()} disabled={busy}>Copy with credentials</button></div>
        </article>
      </>}
    </section>}

    {tab === 'owner' && <section className="stack">
      {!status.ownerReady ? <article className="card"><h2>Owner identity required</h2><p>Complete setup with the Owner invitation to manage phone access.</p></article> : <>
        <article className="card"><h2>Recover the local Windows Client</h2><p>Use this flow after the current local Client certificate is revoked or must be replaced. The Owner identity remains separate and is never used by the SOCKS proxy.</p>
          <div className="serialline"><span>Current local Client certificate serial</span><code>{status.clientSerial ?? 'No local Client enrolled'}</code></div>
          <ol className="recovery-steps"><li>Record the Client serial shown above.</li><li>Revoke that serial with the form below.</li><li>Choose Replace Client to enroll a fresh local Client.</li><li>Start the proxy again and verify the selected application's egress.</li></ol>
        </article>
        <article className="card danger"><h2>Revoke certificate</h2><p>Revocation closes that identity's active relay session. A failed request keeps the entered serial so you can verify it and retry.</p>
          <form className="inline" onSubmit={revoke}><label>Certificate serial<input name="serial" required pattern="[0-9A-Fa-f]+" /></label><button disabled={busy}>Revoke</button></form>
        </article>
        <article className="card"><h2>Replace Client</h2><p>This uses the Owner identity to issue and consume a fresh Client invitation in memory, replaces only the local Client after enrollment succeeds, and stops any old local proxy session.</p>
          <button className="primary" onClick={() => void action(() => api().ReplaceClient())} disabled={busy || !status.clientReady}>Replace Client</button>
        </article>
      </>}
    </section>}
    <footer>Closing this window keeps Mobile Egress in the tray. Quit from the tray to stop the proxy.</footer>
  </main>
}
