import { FormEvent, useCallback, useEffect, useState } from 'react'
import { AgentQr, api, AWSAccount, BridgeStatus, DeviceAuthorization, EC2Instance, EndpointMigration, ManagedNode } from './api'

const emptyBridge: BridgeStatus = { tailscaleInstalled: false, tailscaleOnline: false, funnelReady: false, relayReady: false, ownerReady: false, ready: false, needsRotation: false }
const requiredPermissionsPolicy = JSON.stringify({
  Version: '2012-10-17',
  Statement: [{
    Sid: 'MobileEgressEC2AndSSMSetup',
    Effect: 'Allow',
    Action: [
      'ec2:DescribeImages',
      'ec2:DescribeInstances',
      'ec2:AssociateIamInstanceProfile',
      'iam:AddRoleToInstanceProfile',
      'iam:AttachRolePolicy',
      'iam:CreateInstanceProfile',
      'iam:CreateRole',
      'iam:GetInstanceProfile',
      'iam:GetRole',
      'iam:ListAttachedRolePolicies',
      'iam:ListRolePolicies',
      'ssm:DescribeInstanceInformation',
      'ssm:GetCommandInvocation',
      'ssm:SendCommand',
    ],
    Resource: '*',
  }],
}, null, 2)

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
    catch (reason) { setError(reason instanceof Error ? reason.message : 'Unable to complete that action.'); return false }
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
    await action('inventory', async () => { setInstances(await api().ListEC2Instances() ?? []); setAWSReady(true) })
  }

  async function prepareSSM(instance: EC2Instance) {
    await action(`ssm-${instance.id}`, async () => {
      try { await api().EnsureInstanceSSM(instance.id, false) }
      catch {
        if (!instance.profileArn || !instance.roleName) throw new Error('Unable to create the dedicated SSM profile.')
        if (!window.confirm(`Add AmazonSSMManagedInstanceCore to existing role ${instance.roleName}? The app will not replace its instance profile.`)) return
        await api().EnsureInstanceSSM(instance.id, true)
      }
      setInstances(await api().ListEC2Instances() ?? [])
    })
  }

  async function installNode(instanceId: string) {
    await action(`install-${instanceId}`, async () => {
      await api().InstallEC2Node(instanceId)
      setNodes(await api().ManagedNodes() ?? [])
    })
  }

  async function copyNodeProxy(instanceId: string) {
    await action(`copy-${instanceId}`, async () => { await navigator.clipboard.writeText(await api().NodeProxyLine(instanceId)) })
  }

  async function maintainNode(instanceId: string, repair: boolean) {
    await action(`${repair ? 'repair' : 'update'}-${instanceId}`, async () => {
      if (repair) await api().RepairEC2Node(instanceId)
      else await api().UpdateEC2Node(instanceId)
      setNodes(await api().ManagedNodes() ?? [])
    })
  }

  async function cancelPendingNode(instanceId: string) {
    if (!window.confirm(`Cancel the interrupted install reservation for ${instanceId}? Do this only when no installation is still running.`)) return
    await action(`cancel-${instanceId}`, async () => {
      await api().CancelEC2NodeReservation(instanceId, true)
      setPendingNodes(await api().PendingEC2NodeReservations() ?? [])
    })
  }

  const readyInstanceCount = instances.filter(instance => instance.ssmOnline).length
  const setupInstanceCount = Math.max(instances.length - readyInstanceCount, 0)

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
          <div className="aws-badges"><span>◷ About 3 minutes</span><span>✓ No AWS CLI needed</span></div>
        </div>

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

        <button className="link-button centered" type="button" onClick={() => document.getElementById('manual-aws-key')?.scrollIntoView({ behavior: 'smooth' })}>Already have an access key? Enter it manually</button>
      </article>

      <details id="manual-aws-key" className="card"><summary>Manual access-key entry</summary><form onSubmit={saveAccessKeys}><label>Access key ID<input name="accessKeyId" required autoComplete="off" /></label><label>Secret access key<input name="secretAccessKey" type="password" required autoComplete="off" /></label><label>Session token (optional)<textarea name="sessionToken" rows={3} autoComplete="off" /></label><button className="primary" disabled={!!busy}>Save AWS access key</button></form></details>
      <details className="card"><summary>Advanced: IAM Identity Center</summary><p>Use this if your AWS account already has IAM Identity Center. If you only have the AWS root login, root can enable Identity Center in the browser, but Mobile Egress signs in as the Identity Center user you create.</p><div className="setup-callout"><div><strong>Need a Start URL?</strong><p>Open IAM Identity Center, choose Enable, then choose Single-Region instance in US East (N. Virginia). Create a user for yourself, assign it access to this AWS account, and copy the AWS access portal URL into the Start URL field.</p></div><button onClick={() => void openIdentityCenterConsole()} disabled={!!busy}>{busy === 'sso-console' ? 'Opening AWS…' : 'Open setup page'}</button></div><form onSubmit={beginIdentityCenter} className="form-grid"><label>Start URL<input name="startUrl" required placeholder="https://d-xxxxxxxxxx.awsapps.com/start" /></label><label>SSO region<input name="region" required defaultValue="us-east-1" /></label><button className="primary" disabled={!!busy}>Open AWS login</button></form>{authorization && <div className="issued"><code>{authorization.userCode}</code><button onClick={() => window.open(authorization.verificationUrl, '_blank')}>Open browser again</button><small>Approve in the browser, then continue.</small><button className="primary" onClick={() => void completeIdentityCenter()} disabled={!!busy}>I approved the login</button></div>}{accounts.length > 0 && <form onSubmit={selectRole} className="form-grid"><label>AWS account<select value={selectedAccount} onChange={event => void chooseAccount(event.target.value)} required><option value="">Choose account</option>{accounts.map(account => <option key={account.id} value={account.id}>{account.name || account.id}</option>)}</select></label><label>Role<select name="role" required><option value="">Choose role</option>{roles.map(role => <option key={role} value={role}>{role}</option>)}</select></label><button className="primary" disabled={!!busy}>Use this role</button></form>}</details>
    </section>}

    {tab === 'nodes' && <section className="stack">
      <article className="card"><div className="row"><div><p className="step-label">Step 4</p><h2>Windows Server 2019 nodes</h2></div><button onClick={() => void refreshInstances()} disabled={!!busy}>Refresh us-east-1</button></div><p>Only running x86-64 Windows Server 2019 instances appear. They need outbound HTTPS and SSM; public IPs and inbound security-group rules are not used.</p>{!awsReady && <p className="note">Connect AWS on the AWS Login tab, then refresh.</p>}</article>
      <div className="node-grid">{instances.map(instance => { const managed = nodes.some(node => node.instanceId === instance.id); return <article className="card node" key={instance.id}><div className="row"><div><h2>{instance.name || instance.id}</h2><code>{instance.id}</code></div><span className={`pill ${instance.ssmOnline ? 'on' : ''}`}>{managed ? 'Managed' : instance.ssmOnline ? 'SSM online' : 'SSM setup'}</span></div><p>{instance.imageDescription}</p>{instance.roleName && <div className="serialline"><span>Existing IAM role</span><code>{instance.roleName}</code></div>}<div className="actions"><button onClick={() => void prepareSSM(instance)} disabled={!!busy}>{instance.ssmOnline ? 'Check SSM role' : 'Prepare SSM'}</button><button className="primary" onClick={() => void installNode(instance.id)} disabled={!!busy || !instance.ssmOnline || managed}>{managed ? 'Client installed' : busy === `install-${instance.id}` ? 'Installing…' : 'Install Client'}</button></div></article> })}</div>
      {pendingNodes.length > 0 && <article className="card"><h2>Interrupted install reservations</h2><p>Retry Install Client for the same available instance. If that instance was terminated or cannot be recovered, explicitly cancel its reservation to release the slot.</p><div className="managed-list">{pendingNodes.map(instanceId => <div className="managed" key={instanceId}><div><strong>{instanceId}</strong><small>Reserved before remote provisioning</small></div><div className="actions"><button onClick={() => void cancelPendingNode(instanceId)} disabled={!!busy}>{busy === `cancel-${instanceId}` ? 'Cancelling…' : 'Cancel reservation'}</button></div></div>)}</div></article>}
      {nodes.length > 0 && <article className="card"><h2>Managed nodes ({nodes.length} / 10)</h2><div className="managed-list">{nodes.map(node => <div className="managed" key={node.instanceId}><div><strong>{node.instanceId}</strong><small>Client {node.clientSerial} · v{node.serviceVersion} · {node.health}</small></div><code>{node.proxy}</code><div className="actions"><button onClick={() => void copyNodeProxy(node.instanceId)} disabled={!!busy}>Copy credentials</button><button onClick={() => void maintainNode(node.instanceId, false)} disabled={!!busy}>{busy === `update-${node.instanceId}` ? 'Updating…' : 'Update'}</button><button onClick={() => void maintainNode(node.instanceId, true)} disabled={!!busy}>{busy === `repair-${node.instanceId}` ? 'Repairing…' : 'Repair'}</button></div></div>)}</div></article>}
    </section>}
    <footer>Closing the window keeps the controller available in the tray. The relay and EC2 Clients run as Windows services.</footer>
  </main>
}
