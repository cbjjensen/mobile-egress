export function managedNodeIdentity(instanceId, instances) {
  const name = instances.find(instance => instance.id === instanceId)?.name?.trim() ?? ''
  return {
    title: name || instanceId,
    instanceId: name ? instanceId : '',
  }
}
