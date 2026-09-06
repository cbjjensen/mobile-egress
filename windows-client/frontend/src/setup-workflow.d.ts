import type { BridgeStatus } from './api'
type SetupAPI = {
  GetBridgeStatus(): Promise<BridgeStatus>
  InstallTailscale(): Promise<void>
  ConnectTailscale(): Promise<BridgeStatus>
  SetupLocalBridge(): Promise<BridgeStatus>
  RepairLocalBridge(): Promise<BridgeStatus>
}
export function runSetupWorkflow(api: SetupAPI, options?: { signal?: AbortSignal; onStage?: (message: string) => void; onBridge?: (bridge: BridgeStatus) => void; wait?: () => Promise<void>; now?: () => number }): Promise<BridgeStatus>
