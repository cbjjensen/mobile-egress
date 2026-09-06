import type { BridgeStatus, EC2Instance, ManagedNode } from './api'
export function canInstallNode(bridge: BridgeStatus, instance: EC2Instance, managed: boolean, busy: string): boolean
export function nextSetupStep(bridge: BridgeStatus, awsReady: boolean, nodes: ManagedNode[], options?: { awsChecking?: boolean; verified?: boolean }): { tab: 'bridge' | 'settings' | 'nodes' | 'phone'; label: string; detail: string; button?: string; action?: 'setup' | 'pair' | 'inventory' | 'verify' | 'refresh' | 'details'; complete?: boolean }
