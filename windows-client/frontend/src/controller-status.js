// Retain the last component information; freshness is a separate display state.
export function componentPresentation(status) {
  if (!status || status.checking) return 'Checking'
  if (status.error) return status.stale ? 'Unavailable / stale' : 'Unavailable'
  if (status.stale) return 'Stale'
  return 'Current'
}
