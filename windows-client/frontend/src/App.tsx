import { FormEvent, useCallback, useEffect, useRef, useState } from 'react'
import { AgentQr, api, AWSAccount, BridgeStatus, DeviceAuthorization, EC2Instance, EndpointMigration, ManagedNode } from './api'
import { ActivityEvent, ActivitySeverity, appendActivityEvent, filterActivityEvents, formatActivityEvents } from './activity-log.js'
import awsPermissionsPolicy from './aws-permissions-policy.json'
import { requiresSSMRoleConfirmation, shouldSkipSSMProfileSetup, ssmWaitingLiveText, ssmWaitingStatusText, waitForSSMOnline } from './ssm-progress.js'

const emptyBridge: BridgeStatus = { tailscaleInstalled: false, tailscaleOnline: false, funnelReady: false, relayReady: false, ownerReady: false, ready: false, needsRotation: false }
const requiredPermissionsPolicy = JSON.stringify(awsPermissionsPolicy, null, 2)
type SSMProgress = {
  phase: 'preparing' | 'waiting' | 'timeout'
  startedAt: number
  lastCheckedAt?: number
  checkCount: number
  setupSkipped?: boolean
}

function errorMessage(reason: unknown) {
  if (reason instanceof Error) return reason.message
  if (typeof reason === 'string' && reason.trim()) return reason
  return 'Unable to complete that action.'
}

export default function App() {
  const [tab, setTab] = useState<'bridge' | 'phone' | 'nodes' | 'settings'>('bridge')
  const [bridge, setBridge] = useState<BridgeStatus>(emptyBridge)
  const [phoneQr, setPhoneQr] = useState<AgentQr | null>(null)
  const [migrationQr, setMigrationQr] = useState<EndpointMigration | null>(null)
  const [authorization, setAuthorization] = useState<DeviceAuthorization | null>(null)
  const [accounts, setAccounts] = useState<AWSAccount[]>([])
  const [selectedAccount, setSelectedAccount] = useState('')
  const [roles, setRoles] = useState<string[]>([])
  const [instances, setInstances] = useState<EC2Instance[]>([])
  const [nodes, setNodes] = useState<ManagedNode[]>([])
  const [pendingNodes, setPendingNodes] = useState<string[]>([])
  const [awsReady, setAWSReady] = useState(false)
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  const [showPolicy, setShowPolicy] = useState(false)
  const [showSessionToken, setShowSessionToken] = useState(false)
  const [showSecret, setShowSecret] = useState(false)
  const [ssmProgress, setSSMProgress] = useState<Record<string, SSMProgress>>({})
  const [clock, setClock] = useState(Date.now())
  const [activityEvents, setActivityEvents] = useState<ActivityEvent[]>([])
  const [activityFilter, setActivityFilter] = useState('all')
  const activitySequence = useRef(0)

  function recordActivity(instanceId: string, instanceName: string, actionName: string, severity: ActivitySeverity, message: string) {
    activitySequence.current += 1
    setActivityEvents(current => appendActivityEvent(current, {
      id: `${Date.now()}-${activitySequence.current}`,
      timestamp: new Date().toISOString(),
      instanceId,
      instanceName,
      action: actionName,
      severity,
      message,
    }))
  }

  function instanceName(instanceId: string) {
    return instances.find(instance => instance.id === instanceId)?.name ?? ''
  }

  const refresh = useCallback(async () => {
    try {
      const [bridgeStatus, managed, pending] = await Promise.all([api().GetBridgeStatus(), api().ManagedNodes(), api().PendingEC2NodeReservations()])
      setBridge(bridgeStatus); setNodes(managed ?? []); setPendingNodes(pending ?? [])
    } catch { setError('Unable to refresh local bridge status.') }
  }, [])

  useEffect(() => {
    void refresh()
    const timer = window.setInterval(() => void refresh(), 4000)
    return () => window.clearInterval(timer)
  }, [refresh])

  useEffect(() => {
    if (!Object.values(ssmProgress).some(progress => progress.phase === 'waiting')) return
    setClock(Date.now())
    const timer = window.setInterval(() => setClock(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [ssmProgress])

  useEffect(() => {
    if (!phoneQr) return
    const remaining = new Date(phoneQr.expiresAt).getTime() - Date.now()
    if (remaining <= 0) { setPhoneQr(null); return }
    const timer = window.setTimeout(() => setPhoneQr(null), remaining)
    return () => window.clearTimeout(timer)
  }, [phoneQr])

  useEffect(() => {
    if (!migrationQr) return
    const remaining = new Date(migrationQr.expiresAt).getTime() - Date.now()
    if (remaining <= 0) { setMigrationQr(null); return }
    const timer = window.setTimeout(() => setMigrationQr(null), remaining)
    return () => window.clearTimeout(timer)
  }, [migrationQr])

  async function action(name: string, work: () => Promise<void>) {
    setBusy(name); setError('')
    try { await work(); await refresh(); return true }
    catch (reason) { setError(errorMessage(reason)); return false }
    finally { setBusy('') }
  }

  async function installTailscale() {
    await action('tailscale-install', async () => { await api().InstallTailscale() })
  }

  async function connectTailscale() {
    await action('tailscale-connect', async () => { setBridge(await api().ConnectTailscale()) })
  }

  async function setupBridge() {
    await action('bridge', async () => { setBridge(await api().SetupLocalBridge()) })
  }

  async function rotateBridge() {
    await action('rotate', async () => {
      setMigrationQr(await api().RotateLocalBridge())
      setPhoneQr(null)
      setTab('phone')
    })
  }

  async function repairBridge() {
    await action('relay-repair', async () => { setBridge(await api().RepairLocalBridge()) })
  }

  async function issueAgentQr() {
    await action('qr', async () => { setPhoneQr(await api().IssueAgentQr()) })
  }

  async function beginIdentityCenter(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const data = new FormData(event.currentTarget)
    await action('sso-begin', async () => {
      setAuthorization(await api().BeginAWSIdentityCenter(String(data.get('startUrl')), String(data.get('region'))))
    })
  }

  async function openIdentityCenterConsole() {
    await action('sso-console', async () => { await api().OpenAWSIdentityCenterConsole() })
  }

  async function openIAMUserCreateConsole() {
    await action('iam-user-console', async () => { await api().OpenAWSIAMUserCreateConsole() })
  }

  async function copyRequiredPermissions() {
    await action('copy-policy', async () => { await navigator.clipboard.writeText(requiredPermissionsPolicy) })
  }

  async function completeIdentityCenter() {
    await action('sso-complete', async () => {
      const result = await api().CompleteAWSIdentityCenter()
      setAccounts(result ?? []); setAuthorization(null)
      if (result?.length === 1) await chooseAccount(result[0].id)
    })
  }

  async function chooseAccount(accountId: string) {
    setSelectedAccount(accountId); setRoles([])
    if (!accountId) return
    setRoles(await api().AWSIdentityCenterRoles(accountId) ?? [])
  }

  async function selectRole(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const role = String(new FormData(event.currentTarget).get('role'))
    await action('sso-role', async () => {
      await api().SelectAWSIdentityCenterRole(selectedAccount, role)
      setAWSReady(true); setInstances(await api().ListEC2Instances() ?? [])
    })
  }

  async function saveAccessKeys(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = event.currentTarget
    const data = new FormData(form)
    await action('access-key', async () => {
      await api().SaveAWSAccessKeys(String(data.get('accessKeyId')), String(data.get('secretAccessKey')), String(data.get('sessionToken') ?? ''))
      setAWSReady(true); setInstances(await api().ListEC2Instances() ?? []); form.reset()
    })
  }

  async function refreshInstances() {
    let count = 0
    const refreshed = await action('inventory', async () => {
      const inventory = await api().ListEC2Instances() ?? []
      count = inventory.length
      setInstances(inventory); setAWSReady(true)
    })
    recordActivity('', '', 'Inventory', refreshed ? 'success' : 'error', refreshed ? `Found ${count} supported EC2 ${count === 1 ? 'instance' : 'instances'}.` : 'Refresh failed. See the error banner.')
  }

  async function prepareSSM(instance: EC2Instance) {
    if (shouldSkipSSMProfileSetup(instance)) {
      recordActivity(instance.id, instance.name, 'SSM', 'success', 'Instance is already SSM online. Profile setup skipped.')
      return
    }

    const existingProfile = Boolean(instance.profileArn && instance.roleName)
    recordActivity(instance.id, instance.name, 'SSM', 'info', existingProfile ? 'Verifying the attached profile and SSM permissions.' : 'Preparing the instance profile.')
    const startedAt = Date.now()
    setClock(startedAt)
    setSSMProgress(current => ({ ...current, [instance.id]: { phase: 'preparing', startedAt, checkCount: 0 } }))
    let preparedRoleName = ''
    let setupSkipped = false
    let cancelled = false
    const prepared = await action(`ssm-${instance.id}`, async () => {
      try {
        const result = await api().EnsureInstanceSSM(instance.id, false)
        preparedRoleName = result.roleName
        setupSkipped = !result.changed
      }
      catch (reason) {
        if (!instance.profileArn || !instance.roleName || !requiresSSMRoleConfirmation(reason)) throw reason
        if (!window.confirm(`Add AmazonSSMManagedInstanceCore to existing role ${instance.roleName}? The app will not replace its instance profile.`)) { cancelled = true; return }
        const result = await api().EnsureInstanceSSM(instance.id, true)
        preparedRoleName = result.roleName
        setupSkipped = !result.changed
      }
    })
    if (!prepared || !preparedRoleName) {
      setSSMProgress(current => { const next = { ...current }; delete next[instance.id]; return next })
      recordActivity(instance.id, instance.name, 'SSM', cancelled ? 'warning' : 'error', cancelled ? 'Existing-role change cancelled.' : 'Preparation failed. See the error banner.')
      return
    }

    recordActivity(instance.id, instance.name, 'SSM', 'success', setupSkipped ? 'Profile and SSM permissions already configured. Checking readiness.' : 'SSM setup completed. Checking readiness.')
    setInstances(current => current.map(item => item.id === instance.id
      ? { ...item, profileArn: item.profileArn || 'attaching', roleName: preparedRoleName }
      : item))
    setSSMProgress(current => ({ ...current, [instance.id]: { phase: 'waiting', startedAt, checkCount: 0, setupSkipped } }))
    let checkCount = 0
    try {
      const result = await waitForSSMOnline({
        checkOnline: async () => await api().InstanceSSMOnline(instance.id),
        onCheck: online => {
          const checkedAt = Date.now()
          checkCount += 1
          recordActivity(instance.id, instance.name, 'SSM check', online ? 'success' : 'info', online ? 'Instance reported SSM online.' : `Check ${checkCount}: waiting for registration.`)
          setInstances(current => current.map(item => item.id === instance.id ? { ...item, ssmOnline: online } : item))
          setSSMProgress(current => {
            const progress = current[instance.id]
            if (!progress) return current
            return { ...current, [instance.id]: { ...progress, lastCheckedAt: checkedAt, checkCount: progress.checkCount + 1 } }
          })
        },
        onOnline: async () => {
          setSSMProgress(current => { const next = { ...current }; delete next[instance.id]; return next })
          if (!nodes.some(node => node.instanceId === instance.id)) {
            await installNode({ ...instance, profileArn: instance.profileArn || 'attached', roleName: preparedRoleName, ssmOnline: true })
          }
        },
      })
      if (result.online) return
      setSSMProgress(current => ({ ...current, [instance.id]: { ...(current[instance.id] ?? { startedAt, checkCount: 0 }), phase: 'timeout' } }))
      setError('The SSM profile is attached, but this instance did not come online within five minutes. Confirm the EC2 instance has outbound HTTPS, then retry the status check.')
      recordActivity(instance.id, instance.name, 'SSM check', 'error', 'Timed out after five minutes. Confirm outbound HTTPS and retry.')
    } catch (reason) {
      setSSMProgress(current => ({ ...current, [instance.id]: { ...(current[instance.id] ?? { startedAt, checkCount: 0 }), phase: 'timeout' } }))
      setError(`The SSM profile is attached, but its status could not be refreshed. ${errorMessage(reason)}`)
      recordActivity(instance.id, instance.name, 'SSM check', 'error', 'Status refresh failed. See the error banner.')
    }
  }

  async function installNode(instance: EC2Instance) {
    recordActivity(instance.id, instance.name, 'Client install', 'info', 'Installing the signed Windows Client through SSM.')
    const installed = await action(`install-${instance.id}`, async () => {
      await api().InstallEC2Node(instance.id)
      setNodes(await api().ManagedNodes() ?? [])
    })
    recordActivity(instance.id, instance.name, 'Client install', installed ? 'success' : 'error', installed ? 'Client installed successfully.' : 'Installation failed. See the error banner.')
  }

  async function copyNodeProxy(instanceId: string) {
    const copied = await action(`copy-${instanceId}`, async () => { await navigator.clipboard.writeText(await api().NodeProxyLine(instanceId)) })
    recordActivity(instanceId, instanceName(instanceId), 'Proxy credentials', copied ? 'success' : 'error', copied ? 'Credentials copied to the clipboard.' : 'Copy failed. See the error banner.')
  }

  async function maintainNode(instanceId: string, repair: boolean) {
    const actionName = repair ? 'Client repair' : 'Client update'
    recordActivity(instanceId, instanceName(instanceId), actionName, 'info', repair ? 'Repair started.' : 'Update started.')
    const completed = await action(`${repair ? 'repair' : 'update'}-${instanceId}`, async () => {
      if (repair) await api().RepairEC2Node(instanceId)
      else await api().UpdateEC2Node(instanceId)
      setNodes(await api().ManagedNodes() ?? [])
    })
    recordActivity(instanceId, instanceName(instanceId), actionName, completed ? 'success' : 'error', completed ? `${repair ? 'Repair' : 'Update'} completed.` : `${repair ? 'Repair' : 'Update'} failed. See the error banner.`)
  }

  async function cancelPendingNode(instanceId: string) {
    if (!window.confirm(`Cancel the interrupted install reservation for ${instanceId}? Do this only when no installation is still running.`)) return
    const cancelled = await action(`cancel-${instanceId}`, async () => {
      await api().CancelEC2NodeReservation(instanceId, true)
      setPendingNodes(await api().PendingEC2NodeReservations() ?? [])
    })
    recordActivity(instanceId, instanceName(instanceId), 'Reservation', cancelled ? 'success' : 'error', cancelled ? 'Interrupted install reservation cancelled.' : 'Cancellation failed. See the error banner.')
  }

  async function copyVisibleActivityLogs(events: ActivityEvent[]) {
    await action('copy-activity', async () => { await navigator.clipboard.writeText(formatActivityEvents(events)) })
  }

  const readyInstanceCount = instances.filter(instance => instance.ssmOnline).length
  const setupInstanceCount = Math.max(instances.length - readyInstanceCount, 0)
  const visibleActivityEvents = filterActivityEvents(activityEvents, activityFilter)
  const activitySubjects = new Map<string, string>()
  for (const instance of instances) activitySubjects.set(instance.id, instance.name)
  for (const event of activityEvents) if (event.instanceId && !activitySubjects.has(event.instanceId)) activitySubjects.set(event.instanceId, event.instanceName)
  const activityInstanceOptions = [...activitySubjects].sort((left, right) => (left[1] || left[0]).localeCompare(right[1] || right[0]))

  return <main className="shell">
    <header><div><p className="eyebrow">Personal cellular bridge</p><h1>Mobile Egress</h1></div><div className={`health ${bridge.ready ? 'ready' : ''}`}><span />{bridge.ready ? 'Bridge ready' : bridge.tailscaleOnline ? 'Relay setup needed' : bridge.tailscaleInstalled ? 'Tailscale connection needed' : 'Setup needed'}</div></header>
    <nav><button className={tab === 'bridge' ? 'active' : ''} onClick={() => setTab('bridge')}>Bridge</button><button className={tab === 'phone' ? 'active' : ''} onClick={() => setTab('phone')}>Phone</button><button className={tab === 'nodes' ? 'active' : ''} onClick={() => setTab('nodes')}>EC2 Nodes</button><button className={tab === 'settings' ? 'active' : ''} onClick={() => setTab('settings')}>AWS Login</button></nav>
    {error && <div className="error" role="alert">{error}</div>}

    {tab === 'bridge' && <section className="stack">
      <article className="card hero-card"><p className="step-label">Step 1</p><h2>Install and connect Tailscale</h2><p>{bridge.tailscaleInstalled ? 'Tailscale is installed. Connect it here; browser approval may be required.' : 'The app downloads the official amd64 MSI, verifies its checksum and Tailscale Authenticode signature, then asks for UAC.'}</p><div className="row actions"><span className={`pill ${bridge.tailscaleOnline ? 'on' : ''}`}>{bridge.tailscaleOnline ? 'Online' : bridge.tailscaleInstalled ? 'Installed · not connected' : 'Not installed'}</span>{bridge.tailscaleOnline ? null : bridge.tailscaleInstalled ? <button onClick={() => void connectTailscale()} disabled={!!busy}>{busy === 'tailscale-connect' ? 'Opening Tailscale…' : 'Connect Tailscale'}</button> : <button onClick={() => void installTailscale()} disabled={!!busy}>{busy === 'tailscale-install' ? 'Waiting for UAC…' : 'Install Tailscale'}</button>}</div></article>
      <article className="card"><p className="step-label">Step 2</p><h2>Create this PC’s relay</h2><p>This installs <code>MobileEgressRelay</code> as LocalSystem, binds it to <code>127.0.0.1:8443</code>, and publishes raw TLS through Funnel. On first use, the app opens Tailscale’s Funnel approval page automatically. No router or firewall port is opened.</p>{bridge.publicUrl && <div className="serialline"><span>Public Funnel origin</span><code>{bridge.publicUrl}</code></div>}<div className="row actions"><span className={`pill ${bridge.funnelReady ? 'on' : ''}`}>{bridge.funnelReady ? 'Funnel active' : 'Funnel not ready'}</span><span className={`pill ${bridge.relayReady ? 'on' : ''}`}>{bridge.relayReady ? 'Relay healthy' : 'Relay not ready'}</span></div>{bridge.needsRotation ? <><p className="note">Tailscale now reports a different Funnel name. Connect AWS first if nodes are managed, then rotate and scan the migration QR on Android.</p><button className="primary" onClick={() => void rotateBridge()} disabled={!!busy}>{busy === 'rotate' ? 'Updating relay and nodes…' : 'Rotate endpoint safely'}</button></> : bridge.ownerReady && (!bridge.relayReady || !bridge.funnelReady) ? <button className="primary" onClick={() => void repairBridge()} disabled={!!busy}>{busy === 'relay-repair' ? 'Repairing through UAC…' : 'Repair Funnel and local relay'}</button> : <button className="primary" onClick={() => void setupBridge()} disabled={!!busy || bridge.ownerReady}>{bridge.ready ? 'Local bridge ready' : busy === 'bridge' ? 'Waiting for Funnel approval / UAC…' : 'Set up local bridge'}</button>}</article>
      <article className="note"><strong>Availability</strong><p>Your PC and phone must stay powered on and connected. Funnel is intended here for light, interruption-tolerant personal traffic.</p></article>
    </section>}

    {tab === 'phone' && <section className="stack">{!bridge.ownerReady ? <article className="card"><h2>Set up the bridge first</h2><p>The local Owner identity is required before pairing Android.</p></article> : <>{migrationQr && <article className="card"><p className="step-label">Endpoint migration</p><h2>Move the existing Android Agent</h2><p>Stop the Agent, choose Scan QR, and scan this one-use migration code. Its Android Keystore key and certificate stay unchanged.</p><div className="qr-card"><img src={migrationQr.imageDataUrl} alt="Android Agent endpoint migration QR" /><p>Expires {new Date(migrationQr.expiresAt).toLocaleTimeString()}.</p>{migrationQr.updatedNodes.length > 0 && <small>Updated EC2 nodes: {migrationQr.updatedNodes.join(', ')}</small>}{migrationQr.failedNodes.length > 0 && <p className="error">Repair these nodes after reconnecting AWS: {migrationQr.failedNodes.join(', ')}</p>}</div></article>}<article className="card"><p className="step-label">Step 3</p><h2>Pair the Android Agent</h2><p>Scan the short-lived QR in the Android app. The phone continues to bind outbound sockets to cellular with no Wi-Fi fallback.</p>{phoneQr ? <div className="qr-card"><img src={phoneQr.imageDataUrl} alt="Android Agent pairing QR" /><p>Expires {new Date(phoneQr.expiresAt).toLocaleTimeString()}.</p><button onClick={() => void issueAgentQr()} disabled={!!busy}>Replace QR</button></div> : <button className="primary" onClick={() => void issueAgentQr()} disabled={!!busy || !!migrationQr}>Generate Android QR</button>}</article></>}</section>}

    {tab === 'settings' && <section className="stack aws-wizard">
      <article className="card aws-connect-card">
        <div className="aws-wizard-header">
          <div>
            <h2>Connect AWS</h2>
            <p>Connect the AWS account where you normally see your EC2 instances.</p>
          </div>
        </div>

        <button className="link-button manual-access-link" type="button" onClick={() => document.getElementById('manual-aws-key')?.scrollIntoView({ behavior: 'smooth' })}>Already have an access key? Enter it manually</button>

        <ol className="aws-progress" aria-label="AWS setup steps">
          {['Open AWS', 'Create user', 'Add permissions', 'Connect'].map((label, index) => <li key={label} className="active"><span>{index + 1}</span><strong>{label}</strong></li>)}
        </ol>

        <div className="wizard-panel">
          <div className="wizard-step-number">1</div>
          <div className="wizard-step-body">
            <h3>Open AWS</h3>
            <p>Sign in to the same AWS account where you normally view and manage your EC2 instances.</p>
            <button className="primary wide" type="button" onClick={() => void openIAMUserCreateConsole()} disabled={!!busy}>{busy === 'iam-user-console' ? 'Opening AWS…' : 'Open AWS IAM ↗'}</button>
          </div>
        </div>

        <div className="wizard-panel">
          <div className="wizard-step-number">2</div>
          <div className="wizard-step-body">
            <h3>Create a Mobile Egress user</h3>
            <ol className="micro-steps">
              <li><span>1</span><div className="micro-step-text">In the AWS sidebar, click <strong>Users</strong></div></li>
              <li><span>2</span><div className="micro-step-text">Click <strong>Create user</strong></div></li>
              <li><span>3</span><div className="micro-step-text">Enter <strong>mobile-egress</strong> as the user name</div></li>
              <li><span>4</span><div className="micro-step-text">Leave console access turned off, then click <strong>Next</strong></div></li>
            </ol>
            <div className="copy-name"><code>mobile-egress</code><button type="button" onClick={() => void navigator.clipboard.writeText('mobile-egress')} aria-label="Copy mobile-egress user name">⧉</button></div>
            <button className="primary wide" type="button" onClick={() => void openIAMUserCreateConsole()} disabled={!!busy}>Open IAM Users ↗</button>
          </div>
        </div>

        <div className="wizard-panel">
          <div className="wizard-step-number">3</div>
          <div className="wizard-step-body">
            <h3>Give it limited permissions</h3>
            <p>Open the mobile-egress user, then choose Permissions → Add permissions → Create inline policy.</p>
            <div className="permission-grid">
              <div><h4>This app can</h4><p>✓ View your EC2 instances</p><p>✓ Check instance status</p><p>✓ Connect through Systems Manager</p></div>
              <div><h4>This app cannot</h4><p>× Access AWS billing</p><p>× Create other AWS users</p><p>× Use services it does not need</p></div>
            </div>
            <div className="split-actions">
              <button type="button" onClick={() => setShowPolicy(value => !value)}>{showPolicy ? 'Hide technical policy' : 'View technical policy'}</button>
              <button className="primary" type="button" onClick={() => void copyRequiredPermissions()} disabled={!!busy}>{busy === 'copy-policy' ? 'Copied' : 'Copy required permissions ⧉'}</button>
            </div>
            {showPolicy && <pre className="policy-preview">{requiredPermissionsPolicy}</pre>}
            <small>The technical policy is available if you want to review exactly what will be added.</small>
          </div>
        </div>

        <div className="wizard-panel">
          <div className="wizard-step-number">4</div>
          <div className="wizard-step-body">
            <h3>Create your key and connect</h3>
            <div className="breadcrumb"><span>mobile-egress</span><span>›</span><span>Security credentials</span><span>›</span><span>Access keys</span><span>›</span><span>Create access key</span><span>›</span><span>Local code</span></div>
            <p>AWS shows the secret only once. Paste both values below before closing the AWS page.</p>
            <form onSubmit={saveAccessKeys}>
              <label>Access key ID<input name="accessKeyId" required autoComplete="off" placeholder="AKIA..." /></label>
              <label>Secret access key<div className="secret-field"><input name="secretAccessKey" type={showSecret ? 'text' : 'password'} required autoComplete="off" /><button type="button" onClick={() => setShowSecret(value => !value)} aria-label={showSecret ? 'Hide secret access key' : 'Show secret access key'}>{showSecret ? 'Hide' : '👁'}</button></div></label>
              <button className="link-button" type="button" onClick={() => setShowSessionToken(value => !value)}>{showSessionToken ? '⌃' : '⌄'} Using temporary credentials?</button>
              {showSessionToken && <label>Session token<textarea name="sessionToken" rows={3} autoComplete="off" /></label>}
              <button className="primary wide" disabled={!!busy}>{busy === 'access-key' ? 'Testing AWS connection…' : 'Test AWS connection'}</button>
            </form>
            {awsReady && <div className="success-panel"><div><strong>✓ Connected successfully</strong><p>Found {instances.length} EC2 instances in us-east-1</p></div><span>{readyInstanceCount} ready</span><span className="warn">{setupInstanceCount} need setup</span></div>}
            {awsReady && <button className="primary wide" type="button" onClick={() => setTab('nodes')}>Finish setup</button>}
          </div>
        </div>

      </article>

      <details id="manual-aws-key" className="card"><summary>Manual access-key entry</summary><form onSubmit={saveAccessKeys}><label>Access key ID<input name="accessKeyId" required autoComplete="off" /></label><label>Secret access key<input name="secretAccessKey" type="password" required autoComplete="off" /></label><label>Session token (optional)<textarea name="sessionToken" rows={3} autoComplete="off" /></label><button className="primary" disabled={!!busy}>Save AWS access key</button></form></details>
      <details className="card"><summary>Advanced: IAM Identity Center</summary><p>Use this if your AWS account already has IAM Identity Center. If you only have the AWS root login, root can enable Identity Center in the browser, but Mobile Egress signs in as the Identity Center user you create.</p><div className="setup-callout"><div><strong>Need a Start URL?</strong><p>Open IAM Identity Center, choose Enable, then choose Single-Region instance in US East (N. Virginia). Create a user for yourself, assign it access to this AWS account, and copy the AWS access portal URL into the Start URL field.</p></div><button onClick={() => void openIdentityCenterConsole()} disabled={!!busy}>{busy === 'sso-console' ? 'Opening AWS…' : 'Open setup page'}</button></div><form onSubmit={beginIdentityCenter} className="form-grid"><label>Start URL<input name="startUrl" required placeholder="https://d-xxxxxxxxxx.awsapps.com/start" /></label><label>SSO region<input name="region" required defaultValue="us-east-1" /></label><button className="primary" disabled={!!busy}>Open AWS login</button></form>{authorization && <div className="issued"><code>{authorization.userCode}</code><button onClick={() => window.open(authorization.verificationUrl, '_blank')}>Open browser again</button><small>Approve in the browser, then continue.</small><button className="primary" onClick={() => void completeIdentityCenter()} disabled={!!busy}>I approved the login</button></div>}{accounts.length > 0 && <form onSubmit={selectRole} className="form-grid"><label>AWS account<select value={selectedAccount} onChange={event => void chooseAccount(event.target.value)} required><option value="">Choose account</option>{accounts.map(account => <option key={account.id} value={account.id}>{account.name || account.id}</option>)}</select></label><label>Role<select name="role" required><option value="">Choose role</option>{roles.map(role => <option key={role} value={role}>{role}</option>)}</select></label><button className="primary" disabled={!!busy}>Use this role</button></form>}</details>
    </section>}

    {tab === 'nodes' && <section className="stack">
      <article className="card"><div className="row"><div><p className="step-label">Step 4</p><h2>Windows Server 2019 nodes</h2></div><button onClick={() => void refreshInstances()} disabled={!!busy}>Refresh us-east-1</button></div><p>Only running x86-64 Windows Server 2019 instances appear. They need outbound HTTPS and SSM; public IPs and inbound security-group rules are not used.</p>{!awsReady && <p className="note">Connect AWS on the AWS Login tab, then refresh.</p>}</article>
      <div className="node-grid">{instances.map(instance => {
        const managed = nodes.some(node => node.instanceId === instance.id)
        const progress = ssmProgress[instance.id]
        const status = managed ? 'Managed' : instance.ssmOnline ? 'SSM online' : progress?.phase === 'preparing' ? 'Preparing SSM…' : progress?.phase === 'waiting' ? ssmWaitingStatusText(progress) : progress?.phase === 'timeout' ? 'SSM not online' : 'SSM setup'
        const statusClass = instance.ssmOnline ? 'on' : progress?.phase === 'timeout' ? 'error-state' : progress ? 'waiting' : ''
        const prepareLabel = progress?.phase === 'preparing' ? 'Preparing…' : progress?.phase === 'waiting' ? 'Waiting for SSM…' : progress?.phase === 'timeout' ? 'Retry SSM check' : instance.ssmOnline ? 'SSM ready' : 'Prepare SSM'
        return <article className="card node" key={instance.id}><div className="row"><div><h2>{instance.name || instance.id}</h2><code>{instance.id}</code></div><span className={`pill ${statusClass}`}>{status}</span></div><p>{instance.imageDescription}</p>{instance.roleName && <div className="serialline"><span>Existing IAM role</span><code>{instance.roleName}</code></div>}{progress?.phase === 'waiting' && <div className="ssm-live" role="status"><span className="ssm-spinner" aria-hidden="true" /><span>{ssmWaitingLiveText(progress, clock)}</span></div>}<div className="actions"><button onClick={() => void prepareSSM(instance)} disabled={!!busy || instance.ssmOnline || progress?.phase === 'preparing' || progress?.phase === 'waiting'}>{prepareLabel}</button><button className="primary" onClick={() => void installNode(instance)} disabled={!!busy || !instance.ssmOnline || managed}>{managed ? 'Client installed' : busy === `install-${instance.id}` ? 'Installing…' : 'Install Client'}</button></div></article>
      })}</div>
      <details className="card activity-panel" open>
        <summary><span>Activity logs</span><small>{activityEvents.length} / 200 session events</small></summary>
        <p className="note">Session only. Closing Mobile Egress clears these sanitized events.</p>
        <div className="activity-controls">
          <label>EC2 instance<select value={activityFilter} onChange={event => setActivityFilter(event.target.value)}><option value="all">All instances</option>{activityInstanceOptions.map(([instanceId, name]) => <option value={instanceId} key={instanceId}>{name ? `${name} · ${instanceId}` : instanceId}</option>)}</select></label>
          <div className="actions"><button onClick={() => void copyVisibleActivityLogs(visibleActivityEvents)} disabled={!!busy || visibleActivityEvents.length === 0}>Copy visible logs</button><button onClick={() => setActivityEvents([])} disabled={activityEvents.length === 0}>Clear logs</button></div>
        </div>
        {visibleActivityEvents.length === 0 ? <p className="activity-empty">No activity for this filter yet.</p> : <div className="activity-list">{visibleActivityEvents.map(event => <div className="activity-entry" key={event.id}><time dateTime={event.timestamp}>{new Date(event.timestamp).toLocaleTimeString()}</time><span className={`activity-level ${event.severity}`}>{event.severity}</span><div><strong>{event.instanceId ? `${event.instanceName || event.instanceId} · ${event.instanceId}` : 'Mobile Egress'}</strong><small>{event.action}</small><p>{event.message}</p></div></div>)}</div>}
      </details>
      {pendingNodes.length > 0 && <article className="card"><h2>Interrupted install reservations</h2><p>Retry Install Client for the same available instance. If that instance was terminated or cannot be recovered, explicitly cancel its reservation to release the slot.</p><div className="managed-list">{pendingNodes.map(instanceId => <div className="managed" key={instanceId}><div><strong>{instanceId}</strong><small>Reserved before remote provisioning</small></div><div className="actions"><button onClick={() => void cancelPendingNode(instanceId)} disabled={!!busy}>{busy === `cancel-${instanceId}` ? 'Cancelling…' : 'Cancel reservation'}</button></div></div>)}</div></article>}
      {nodes.length > 0 && <article className="card"><h2>Managed nodes ({nodes.length} / 10)</h2><div className="managed-list">{nodes.map(node => <div className="managed" key={node.instanceId}><div><strong>{node.instanceId}</strong><small>Client {node.clientSerial} · v{node.serviceVersion} · {node.health}</small></div><code>{node.proxy}</code><div className="actions"><button onClick={() => void copyNodeProxy(node.instanceId)} disabled={!!busy}>Copy credentials</button><button onClick={() => void maintainNode(node.instanceId, false)} disabled={!!busy}>{busy === `update-${node.instanceId}` ? 'Updating…' : 'Update'}</button><button onClick={() => void maintainNode(node.instanceId, true)} disabled={!!busy}>{busy === `repair-${node.instanceId}` ? 'Repairing…' : 'Repair'}</button></div></div>)}</div></article>}
    </section>}
    <footer>Closing the window keeps the controller available in the tray. The relay and EC2 Clients run as Windows services.</footer>
  </main>
}
