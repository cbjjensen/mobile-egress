import { FormEvent, useCallback, useEffect, useRef, useState } from 'react'
import { api, ClientFleet, ClientInvitation, ManagedNode } from './api'
import { nodeProxyActions } from './proxy-actions.js'

type Props = { bridgeReady: boolean; disabled: boolean; awsReservations: string[]; onAddAWS: () => void; onNodes: (nodes: ManagedNode[]) => void }

export function ClientsPanel({ bridgeReady, disabled, awsReservations, onAddAWS, onNodes }: Props) {
  const [fleet, setFleet] = useState<ClientFleet>({ nodes: [], pending: [] })
  const [invitation, setInvitation] = useState<ClientInvitation | null>(null)
  const [working, setWorking] = useState('')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const loading = useRef(false)
  const mounted = useRef(true)
  const refresh = useCallback(async () => {
    if (loading.current) return
    loading.current = true
    try {
      const next = await api().RefreshClients()
      if (mounted.current) { setFleet({ ...next, nodes: next.nodes ?? [], pending: next.pending ?? [] }); onNodes(next.nodes ?? []) }
    } catch { if (mounted.current) setError('Unable to refresh Clients. Check the local bridge and try again.') }
    finally { loading.current = false }
  }, [onNodes])

  useEffect(() => {
    mounted.current = true
    void refresh()
    const timer = window.setInterval(() => void refresh(), 4000)
    return () => { mounted.current = false; window.clearInterval(timer) }
  }, [refresh])

  useEffect(() => {
    if (!invitation || invitation.resuming) return
    const remaining = new Date(invitation.expiresAt).getTime() - Date.now()
    if (remaining <= 0) { setInvitation(null); return }
    const timer = window.setTimeout(() => setInvitation(null), remaining)
    return () => window.clearTimeout(timer)
  }, [invitation])

  async function action(name: string, work: () => Promise<void>) {
    setWorking(name); setError(''); setNotice('')
    try { await work() }
    catch (reason) { setError(reason instanceof Error ? reason.message : typeof reason === 'string' ? reason : 'Unable to complete the Client action.') }
    finally { setWorking(''); await refresh() }
  }

  async function invite(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const name = String(new FormData(event.currentTarget).get('displayName') ?? '').trim()
    await action('invite', async () => setInvitation(await api().IssueClientInvitation(name)))
  }

  async function copy(actionName: string, value: () => Promise<string>) {
    await action(actionName, async () => { await navigator.clipboard.writeText(await value()); setNotice('Copied to clipboard.') })
  }

  const busy = disabled || !!working
  const occupied = new Set([...fleet.nodes.map(node => node.nodeId), ...fleet.pending.map(pending => pending.nodeId), ...awsReservations]).size
  return <>
    <article className="card">
      <div className="row"><div><p className="step-label">Step 4</p><h2>Add Windows/Mac Client</h2></div><button onClick={onAddAWS} disabled={busy}>Add from AWS</button></div>
      <p>Install Mobile Egress Client on the machine running your applications, then paste a pairing invitation into its Client app. Keep this controller open until pairing completes. AWS is optional.</p>
      <p>Download the Client installer for Windows x64 or Apple Silicon Mac from the official release.</p>
      <button disabled={busy} onClick={() => void action('downloads', () => api().OpenClientDownloads())}>Open Client downloads</button>
      <form onSubmit={invite} className="form-grid"><label>Client name<input name="displayName" maxLength={80} required placeholder="Work Mac" autoComplete="off" /></label><button className="primary" disabled={busy || !bridgeReady || occupied >= 10}>{working === 'invite' ? 'Creating invitation…' : 'Create pairing invitation'}</button></form>
      {!bridgeReady && <p className="note">Finish local bridge setup before pairing a Client.</p>}
      {invitation && <div className="issued"><label>One-time pairing invitation<textarea aria-label="Client pairing invitation" readOnly value={invitation.invitation} rows={3} /></label><small>{invitation.resuming ? 'Resume only in the Client app that already saved this invitation. Original new-pairing deadline: ' : 'New pairing expires at '}{new Date(invitation.expiresAt).toLocaleTimeString()}. Paste only on your workload machine.</small><button disabled={busy} onClick={() => void copy('copy-invitation', async () => invitation.invitation)}>Copy invitation</button></div>}
    </article>
    {error && <div className="error" role="alert">{error}</div>}
    {notice && <p className="note" role="status">{notice}</p>}
    {fleet.connectionError && <p className="note" role="status">{fleet.connectionError}</p>}
    {fleet.pending.length > 0 && <article className="card"><h2>Pending pairing</h2><p>If the bridge address changed during pairing, show and paste the invitation again. Once configuration arrives, copy its connection update below and import it in the Client app.</p><div className="managed-list">{fleet.pending.map(pending => <div className="managed" key={pending.nodeId}>
      <div><strong>{pending.displayName}</strong><small>{new Date(pending.expiresAt).getTime() <= Date.now() ? 'Invitation expired or awaiting recovery' : 'Waiting for the Client app'}</small></div>
      <div className="actions"><button disabled={busy} onClick={() => void action('resume-invite', async () => setInvitation(await api().ClientInvitation(pending.nodeId)))}>Show invitation</button><button disabled={busy} onClick={() => { if (window.confirm(`Cancel pairing for ${pending.displayName}? Its pending Client access will be revoked.`)) void action('cancel-invite', async () => { await api().CancelClientInvitation(pending.nodeId); if (invitation?.nodeId === pending.nodeId) setInvitation(null) }) }}>Cancel pairing</button></div>
    </div>)}</div></article>}
    <article className="card"><div className="row"><h2>Clients ({occupied} / 10)</h2><button onClick={() => void refresh()} disabled={busy}>Refresh Clients</button></div>
      {fleet.nodes.length === 0 ? <p>No configured Clients yet. Add a Windows or Mac machine above.</p> : <div className="managed-list">{fleet.nodes.map(node => {
        const actions = nodeProxyActions(node)
        return <div className="managed" key={node.nodeId}>
          <div><strong>{node.displayName || node.instanceId}</strong><small>{node.platform === 'macos' ? 'Mac' : 'Windows'} · {node.management === 'paired' ? 'Paired Client' : 'AWS / SSM'} · v{node.serviceVersion}</small><small>{node.health === 'configuring' ? 'Configuration pending' : node.health} · {node.connectionKnown ? node.connected ? 'Connected' : 'Offline' : 'Connection status unavailable'}</small></div>
          <div><code>{node.proxy}</code>{actions.guidance && <small>{actions.guidance}</small>}{node.management === 'paired' && <small>Update or repair using the installer on this Client machine.</small>}</div>
          <div className="actions">
            <button className="primary" disabled={busy || actions.primaryDisabled} onClick={() => void copy('copy-proxy', () => api().ClientProxyLine(node.nodeId))}>Copy proxy line</button>
            <button disabled={busy || actions.secondaryDisabled} onClick={() => void copy('copy-socks', () => api().ClientSOCKSProxyURL(node.nodeId))}>Copy SOCKS5 URL</button>
            {node.management === 'paired' ? <button disabled={busy} onClick={() => void copy('copy-update', () => api().ExportClientEndpointUpdate(node.nodeId))}>Copy connection update</button> : <><button disabled={busy} onClick={() => void action('update', async () => { await api().UpdateEC2Node(node.instanceId) })}>Update</button><button disabled={busy} onClick={() => void action('repair', async () => { await api().RepairEC2Node(node.instanceId) })}>Repair</button></>}
            <button disabled={busy} onClick={() => { if (window.confirm(`Revoke ${node.displayName || node.instanceId}? Active proxy connections will close. This permanently disables its saved Client identity; update or repair will not restore access.`)) void action('revoke', async () => { await api().RevokeClient(node.nodeId) }) }}>Revoke</button>
          </div>
        </div>
      })}</div>}
    </article>
  </>
}
