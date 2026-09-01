import assert from 'node:assert/strict'
import test from 'node:test'

const bridgePlatform = await import('../src/bridge-platform.js').catch(() => ({}))

test('Windows bridge copy preserves MSI, UAC, tray, and service behavior', () => {
  const copy = bridgePlatform.bridgePlatformCopy?.({
    platform: 'windows',
    tailscaleInstalled: false,
    relayServiceState: 'not-required',
  })

  assert.deepEqual(copy, {
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
})

test('macOS bridge copy explains PKG, system-extension, Service Management, and availability', () => {
  const copy = bridgePlatform.bridgePlatformCopy?.({
    platform: 'macos',
    tailscaleInstalled: false,
    relayServiceState: 'not-registered',
  })

  assert.deepEqual(copy, {
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
})

test('macOS relay service states provide exact labels and recovery guidance', () => {
  const expected = {
    'not-registered': ['Relay service not registered', 'Set up the local bridge to register its signed background service.'],
    'approval-required': ['Login Items approval required', 'Open System Settings → General → Login Items and approve ZFNF Mobile Egress, then return here.'],
    enabled: ['Relay service enabled', ''],
    'version-mismatch': ['Relay service update required', 'Repair the local bridge to update and restart the bundled relay helper.'],
    unavailable: ['Relay service unavailable', 'Verify Service Management approval and that the signed relay is running, then try again.'],
  }

  for (const [state, [label, guidance]] of Object.entries(expected)) {
    assert.deepEqual(bridgePlatform.relayServicePresentation?.('macos', state), { label, guidance, ready: state === 'enabled' })
  }
  assert.deepEqual(bridgePlatform.relayServicePresentation?.('windows', 'not-required'), { label: '', guidance: '', ready: true })
})
