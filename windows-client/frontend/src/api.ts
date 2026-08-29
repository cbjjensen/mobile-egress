export type Status = {
  paired: boolean
  role?: 'owner' | 'client'
  ownerReady: boolean
  clientReady: boolean
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

type DesktopAPI = {
  GetStatus(): Promise<Status>
  BootstrapOwner(encodedBundle: string): Promise<void>
  RetryClientSetup(): Promise<void>
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
