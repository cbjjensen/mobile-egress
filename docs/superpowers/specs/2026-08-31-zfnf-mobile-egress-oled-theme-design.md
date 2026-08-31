# ZFNF Mobile Egress OLED Theme Design

## Goal

Give the Windows Wails controller the fixed Premium OLED visual language used by the Inevitable Proxies dashboard while preserving the existing Mobile Egress workflows, W app icon, and compatibility-sensitive identifiers.

## Source Analysis

Inevitable's OLED theme is built around semantic tokens rather than component-specific color overrides. Its hierarchy uses a true-black page, a small set of near-black elevated surfaces, high-contrast cool-white text, and quiet translucent borders. Mint is the primary action and success color, pale blue communicates information and focus, violet is secondary emphasis, amber is warning, and rose is danger. Shadows are dark and restrained so borders and surface elevation—not glow—carry most of the hierarchy.

The current Windows stylesheet is already partially tokenized, but it uses a charcoal canvas, a blue page wash, and electric-blue primary actions and progress glows. The transfer will retain the current component layout and replace that palette and state treatment with a compact Mobile Egress-specific OLED token set.

## Design

- OLED is the only theme. There is no switcher, persisted preference, or light/dark migration.
- The page and native Wails startup background are `#000000`. Component elevation uses `#05070b`, `#080a0f`, `#0b0e14`, and `#0c111a`.
- Primary, muted, and subtle text use `#f2f5fb`, `#aeb7c6`, and `#747d8c`.
- Mint `#7ef2c5` is used for primary actions and success, pale blue `#7db7ff` for information and focus, violet `#d6b3ff` for secondary emphasis, amber `#f4df74` for warning, and rose `#ff8d98` for danger.
- Primary buttons are solid mint with dark text. Secondary controls remain dark with subtle borders. Cards, forms, navigation, progress, status pills, alerts, activity rows, QR containers, hover/focus/disabled states, and reduced-motion behavior all consume semantic tokens.
- The current layout, responsive breakpoints, tab structure, onboarding flow, Wails bindings, and operational behavior do not change.

## Branding And Compatibility

The visible product name becomes **ZFNF Mobile Egress** in the React header, document title, user-facing activity and confirmation copy, Wails window title, tray tooltip/menu, and fatal-dialog caption. The existing W icon remains unchanged.

Executable and package names, Windows services, installer and shortcut identity, IAM identifiers, certificates, proxy authentication realm, URLs, file paths, protocol identifiers, and release artifacts remain unchanged.

## Verification

Dependency-free frontend tests protect the observable OLED styling contract and product-name behavior. Frontend tests, type checking, production build, focused desktop Go tests, the complete Windows component gate, and `git diff --check` must pass. Visual review covers Bridge, Phone, EC2 Nodes, and AWS Login at the Wails minimum window size and a larger desktop viewport using non-sensitive state.
