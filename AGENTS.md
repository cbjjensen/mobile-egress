# Project requirements

- Mobile Egress traffic must pass through the owner's personal Windows PC or Mac running the local relay. This is a permanent product requirement, not a temporary deployment limitation.
- Cloud-hosted, VPS-hosted, EC2-hosted, managed, or other externally hosted Mobile Egress relays are prohibited. Do not propose, benchmark, provision, or implement them as performance improvements or deployment alternatives unless the owner explicitly reverses this requirement.
- Keep the existing Tailscale Funnel ingress and personal-computer relay topology. EC2 machines are workload Clients, never relay hosts. The existing Mac build server is development infrastructure, not a traffic relay.
- Optimize the protocol, clients, mobile Agents, and local relay within that topology. Measure contention before adding transport connections or replacing transport protocols.
- Preserve Android/iOS user-visible parity and mixed-version interoperability when changing transport behavior. Follow the repository mobile-parity skill and maintain `docs/mobile-feature-manifest.json`.
