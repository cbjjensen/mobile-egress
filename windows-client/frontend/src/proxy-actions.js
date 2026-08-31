export async function copyProxyLine(backend, clipboard, instanceId) {
  await clipboard.writeText(await backend.NodeProxyLine(instanceId))
}

export async function copySOCKS5URL(backend, clipboard, instanceId) {
  await clipboard.writeText(await backend.NodeSOCKSProxyURL(instanceId))
}

export function nodeProxyActions(node) {
  return {
    primaryLabel: 'Copy proxy line',
    primaryDisabled: !node.httpProxyReady,
    guidance: node.httpProxyReady ? '' : 'Update Client to enable HTTP proxying',
  }
}
