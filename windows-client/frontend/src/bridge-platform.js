const windowsCopy = Object.freeze({
  tailscaleDescription: 'The app downloads the official amd64 MSI, verifies its checksum and Tailscale Authenticode signature, then asks for UAC.',
  tailscaleInstallBusyLabel: 'Waiting for UAC…',
  systemExtensionGuidance: '',
  relayHeading: 'Create this PC’s relay',
  relayDescription: 'This installs MobileEgressRelay as LocalSystem, binds it to 127.0.0.1:8443, and publishes raw TLS through Funnel. On first use, the app opens Tailscale’s Funnel approval page automatically. No router or firewall port is opened.',
  relaySetupBusyLabel: 'Waiting for Funnel approval / UAC…',
  relayRepairBusyLabel: 'Repairing through UAC…',
  availability: 'Your PC and Agent device must stay powered on and connected. Funnel is intended here for light, interruption-tolerant personal traffic.',
  footer: 'Closing the window keeps the controller available in the tray. The relay and EC2 Clients run as Windows services.',
})

const macOSCopy = Object.freeze({
  tailscaleDescription: 'The app downloads the official Tailscale PKG, verifies its checksum and Developer ID signature, then opens Apple Installer.',
  tailscaleInstallBusyLabel: 'Waiting for Apple Installer…',
  systemExtensionGuidance: 'If macOS prompts, approve Tailscale’s system extension and VPN configuration in System Settings, then connect again.',
  relayHeading: 'Create this Mac’s relay',
  relayDescription: 'This registers the signed relay with macOS Service Management, binds it to 127.0.0.1:8443, and publishes raw TLS through Funnel. No router or firewall port is opened.',
  relaySetupBusyLabel: 'Waiting for Service Management approval…',
  relayRepairBusyLabel: 'Repairing background service…',
  availability: 'Your Mac must stay powered on with its controller user logged in, and your Agent device must stay connected. Funnel is intended here for light, interruption-tolerant personal traffic.',
  footer: 'Closing the window keeps the controller available in the menu bar. The relay runs as a macOS background service; EC2 Clients remain Windows services.',
})

const relayServiceStates = Object.freeze({
  'not-registered': {
    label: 'Relay service not registered',
    guidance: 'Set up the local bridge to register its signed background service.',
    ready: false,
  },
  'approval-required': {
    label: 'Login Items approval required',
    guidance: 'Open System Settings → General → Login Items and approve ZFNF Mobile Egress, then return here.',
    ready: false,
  },
  enabled: { label: 'Relay service enabled', guidance: '', ready: true },
  'version-mismatch': {
    label: 'Relay service update required',
    guidance: 'Repair the local bridge to update and restart the bundled relay helper.',
    ready: false,
  },
  unavailable: {
    label: 'Relay service unavailable',
    guidance: 'Verify Service Management approval and that the signed relay is running, then try again.',
    ready: false,
  },
})

export function bridgePlatformCopy(status) {
  return status?.platform === 'macos' ? macOSCopy : windowsCopy
}

export function relayServicePresentation(platform, state) {
  if (platform !== 'macos') return { label: '', guidance: '', ready: true }
  return relayServiceStates[state] ?? relayServiceStates.unavailable
}
