export async function copyProxyLine(backend, clipboard, instanceId) {
  await clipboard.writeText(await backend.NodeProxyLine(instanceId))
}

export async function copySOCKS5URL(backend, clipboard, instanceId) {
  await clipboard.writeText(await backend.NodeSOCKSProxyURL(instanceId))
}

export function nodeProxyActions(node) {
  return {
    primaryLabel: 'Copy proxy line',
    primaryDisabled: !node.proxyReady,
    secondaryDisabled: !node.proxyReady,
    guidance: node.proxyReady ? '' : node.management === 'paired' ? 'Finish pairing in the Client app before copying the proxy' : 'Update Client to activate the 127.0.0.2 proxy endpoint',
  }
}
