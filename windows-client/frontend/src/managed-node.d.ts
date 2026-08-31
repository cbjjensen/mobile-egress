import type { EC2Instance } from './api'

export type ManagedNodeIdentity = {
  title: string
  instanceId: string
}

export function managedNodeIdentity(instanceId: string, instances: EC2Instance[]): ManagedNodeIdentity
