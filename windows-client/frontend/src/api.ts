export type Status = {
  paired: boolean
  role?: 'owner' | 'client'
  ownerReady: boolean
  clientReady: boolean
  clientSerial?: string
  running: boolean
  relay: 'connected' | 'offline'
  agentAvailable: boolean
  activeStreams: number
  bytesUp: number
  bytesDown: number
  port?: number
  proxy?: string
}

export type AgentQr = { imageDataUrl: string; expiresAt: string }
export type EndpointMigration = AgentQr & { updatedNodes: string[]; failedNodes: string[] }

export type BridgeStatus = {
  tailscaleOnline: boolean
  funnelReady: boolean
  relayReady: boolean
  fqdn?: string
  publicUrl?: string
  ownerReady: boolean
  ready: boolean
  needsRotation: boolean
}

export type DeviceAuthorization = { verificationUrl: string; userCode: string; expiresAt: string }
export type AWSAccount = { id: string; name: string; email: string }
export type EC2Instance = {
  id: string
  name: string
  state: string
  platform: string
  architecture: string
  imageDescription: string
  profileArn?: string
  roleName?: string
  ssmOnline: boolean
}
export type ManagedNode = {
  instanceId: string
  clientSerial: string
  serviceVersion: string
  health: string
  proxy: string
}
export type SSMProfileResult = { changed: boolean; roleName: string }

type DesktopAPI = {
  GetStatus(): Promise<Status>
  GetBridgeStatus(): Promise<BridgeStatus>
  InstallTailscale(): Promise<void>
  SetupLocalBridge(): Promise<BridgeStatus>
  RepairLocalBridge(): Promise<BridgeStatus>
  RotateLocalBridge(): Promise<EndpointMigration>
  SaveAWSAccessKeys(accessKeyId: string, secretAccessKey: string, sessionToken: string): Promise<void>
  BeginAWSIdentityCenter(startUrl: string, ssoRegion: string): Promise<DeviceAuthorization>
  CompleteAWSIdentityCenter(): Promise<AWSAccount[]>
  AWSIdentityCenterRoles(accountId: string): Promise<string[]>
  SelectAWSIdentityCenterRole(accountId: string, roleName: string): Promise<void>
  ListEC2Instances(): Promise<EC2Instance[]>
  EnsureInstanceSSM(instanceId: string, confirmExistingRoleChange: boolean): Promise<SSMProfileResult>
  InstallEC2Node(instanceId: string): Promise<ManagedNode>
  UpdateEC2Node(instanceId: string): Promise<ManagedNode>
  RepairEC2Node(instanceId: string): Promise<ManagedNode>
  ManagedNodes(): Promise<ManagedNode[]>
  PendingEC2NodeReservations(): Promise<string[]>
  CancelEC2NodeReservation(instanceId: string, confirmed: boolean): Promise<void>
  NodeProxyLine(instanceId: string): Promise<string>
  BootstrapOwner(encodedBundle: string): Promise<void>
  RetryClientSetup(): Promise<void>
  ReplaceClient(): Promise<void>
  StartProxy(port: number): Promise<void>
  StopProxy(): Promise<void>
  ProxyLine(): Promise<string>
  IssueAgentQr(): Promise<AgentQr>
  Revoke(serial: string): Promise<void>
  Quit(): Promise<void>
}

declare global {
  interface Window {
    go?: { desktop?: { DesktopApp?: DesktopAPI } }
  }
}

export function api(): DesktopAPI {
  const backend = window.go?.desktop?.DesktopApp
  if (!backend) throw new Error('Wails backend unavailable. Start this UI with the Windows client.')
  return backend
}
