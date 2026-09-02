export type ProxyBackend = {
  NodeProxyLine(instanceId: string): Promise<string>
  NodeSOCKSProxyURL(instanceId: string): Promise<string>
}

export type ProxyClipboard = {
  writeText(value: string): Promise<void>
}

export type ProxyNode = {
  proxyReady: boolean
}

export function copyProxyLine(backend: ProxyBackend, clipboard: ProxyClipboard, instanceId: string): Promise<void>
export function copySOCKS5URL(backend: ProxyBackend, clipboard: ProxyClipboard, instanceId: string): Promise<void>
export function nodeProxyActions(node: ProxyNode): {
  primaryLabel: string
  primaryDisabled: boolean
  secondaryDisabled: boolean
  guidance: string
}
