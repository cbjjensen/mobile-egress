import type { BridgeStatus, EC2Instance, ManagedNode } from './api'
export function canInstallNode(bridge: BridgeStatus, instance: EC2Instance, managed: boolean, busy: string): boolean
export function nextSetupStep(bridge: BridgeStatus, awsReady: boolean, nodes: ManagedNode[]): { tab: 'bridge' | 'settings' | 'nodes'; label: string; detail: string }
