# Delivery plan

> Historical planning record. Its unchecked boxes are not current work status; see [current status](status.md) and the current source instead.

1. Establish repository documentation and build configuration before application code.
2. Implement and test the relay policy, enrollment state, certificate authority, and protocol primitives.
3. Implement the loopback Windows relay service and Tailscale Funnel ingress.
4. Implement the Windows Owner controller, SSM-managed headless Clients, secure local configuration, and tray application.
5. Implement the Android pairing and cellular-bound foreground agent.
6. Add packaging, operational checks, and end-to-end validation instructions.

Each phase is independently testable and committed separately. The detailed task plan is maintained under `docs/superpowers/plans/`.
