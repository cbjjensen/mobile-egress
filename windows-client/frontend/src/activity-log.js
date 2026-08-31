const maximumActivityEvents = 200

export function appendActivityEvent(events, event) {
  return [event, ...events].slice(0, maximumActivityEvents)
}

export function filterActivityEvents(events, instanceId) {
  if (instanceId === 'all') return events
  return events.filter(event => event.instanceId === instanceId)
}

export function formatActivityEvents(events) {
  return events.map(event => {
    const subject = event.instanceId
      ? `${event.instanceName || event.instanceId} (${event.instanceId})`
      : 'Mobile Egress'
    return `${event.timestamp} [${event.severity.toUpperCase()}] ${subject} · ${event.action} · ${event.message}`
  }).join('\n')
}
