import type { BridgeStatus, DesktopPlatform, RelayServiceState } from './api'

export type BridgePlatformCopy = {
  tailscaleDescription: string
  tailscaleInstallBusyLabel: string
  systemExtensionGuidance: string
  relayHeading: string
  relayDescription: string
  relaySetupBusyLabel: string
  relayRepairBusyLabel: string
  availability: string
  footer: string
}

export type RelayServicePresentation = { label: string; guidance: string; ready: boolean }

export function bridgePlatformCopy(status: Pick<BridgeStatus, 'platform' | 'tailscaleInstalled' | 'relayServiceState'>): BridgePlatformCopy
export function relayServicePresentation(platform: DesktopPlatform, state: RelayServiceState): RelayServicePresentation
