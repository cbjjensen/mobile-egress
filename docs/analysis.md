# Implementation analysis

## Current state

This is a new, isolated Git repository. No existing mobile, relay, or desktop implementation is present. The parent Inevitable Proxies repository contains related forwarding guidance, but this project deliberately has no dependency on its production infrastructure.

## Risks

- Android background execution and cellular binding require a physical-device test; the local machine does not currently have Android SDK tooling.
- A relay listener on the Internet must authenticate every session and never become a raw proxy endpoint.
- TCP stream multiplexing needs strict size, timeout, and ownership checks to prevent a paired client from exhausting the phone.
- Certificate authority state is durable operational data; image replacement must not create a new CA unintentionally.

## Validation strategy

Start with Go unit and integration tests for policy, enrollment, protocol, and SOCKS behavior. Build Android and Windows projects once their prerequisites are installed. Complete physical end-to-end validation only with the owner's relay, Android phone, and Windows device.
