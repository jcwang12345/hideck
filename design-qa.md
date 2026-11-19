# HiDeck Signal Workspace — Design QA

**Comparison target**

- Source visual truth: `/Volumes/Data/esim/hideck-ui-signal/design-reference.png`
- Browser implementation evidence: `/Volumes/Data/esim/hideck-ui-signal/qa-artifacts/desktop-dashboard.png`, `/Volumes/Data/esim/hideck-ui-signal/qa-artifacts/mobile-devices.png`, and `/Volumes/Data/esim/hideck-ui-signal/qa-artifacts/mobile-phone.png`
- Combined side-by-side evidence: `/Volumes/Data/esim/hideck-ui-signal/qa-artifacts/design-comparison.png`
- Browser metrics and interaction evidence: `/Volumes/Data/esim/hideck-ui-signal/qa-artifacts/browser-report-isolated.json`
- Source pixels: 1717 × 916. The source is a composite desktop/mobile concept rather than a single runtime viewport.
- Implementation pixels and CSS viewports: desktop 1920 × 1080 at device scale factor 1; mobile 390 × 844 at device scale factor 1.
- Density normalization: every implementation capture is 1 CSS pixel to 1 image pixel. The combined 1800 × 1900 comparison board scales complete views proportionally and does not use pixel density as a fidelity signal.
- State: authenticated light theme, one online EC20 development device, AT&T VoWiFi connected, E911 address required, deterministic local mock API data.

**Findings**

- No actionable P0, P1, or P2 differences remain.
- Fonts and typography: the implementation preserves the reference's compact technical hierarchy with Fira Sans/Fira Code, clear Chinese fallbacks, readable 12 px minimum UI text, stable wrapping, and bounded number/device truncation.
- Spacing and layout rhythm: desktop uses the intended compact sidebar, split workspaces, shallow radii, thin borders, and dense vertical rhythm. Mobile keeps 44 px or larger primary touch targets, a fixed 68 px bottom navigation area, and safe content clearance above it.
- Colors and visual tokens: navy, signal blue, soft blue selected states, amber E911 alerts, green connected states, and red error states map consistently to the reference. Text and state contrast remain legible on the warm neutral canvas.
- Image quality and asset fidelity: this product surface is data and icon driven; the reference contains no required photographic or illustrative asset. Library icons remain sharp at both tested densities and no placeholder art is used.
- Copy and content: production labels and states are retained. The mobile navigation reads `仪表盘 / 设备 / 拨号 / 短信 / 更多`; E911 copy states both the requirement and the action, and number-attribution space remains reserved when lookup has no result.

**Full-view comparison evidence**

- The combined comparison board shows the source concept beside the rendered 1920 × 1080 dashboard, phone, SMS, and 390 × 844 mobile screens.
- Desktop composition keeps the reference's left connection summary and right device fleet while fitting the production traffic panel and route-level navigation.
- Mobile composition keeps the reference's bottom navigation and prominent center dial action while fitting the production device, phone, and settings content.
- Chrome captured 14 routes/states. There were no capture failures, browser console/runtime errors, missing authenticated mobile nav bars, or horizontal overflow cases.

**Focused region comparison evidence**

- Mobile E911 entry: `mobile-devices.png` shows the amber `本卡需要设置 911 紧急地址` card and `现在设置` action in the first 330 CSS pixels, before the device rail.
- Mobile dial access: `mobile-phone.png` shows the dial pad immediately in the phone workspace and the selected center `拨号` tab. Across every authenticated mobile route, clicking the center tab navigated to `#/phone`.
- Desktop connection workspace: `desktop-dashboard.png` keeps E911, SIM/Access/Tunnel/IMS/SMS stages, device health, and fleet status in one 1080p view.
- These focused captures were needed because the E911 action text and bottom-tab interaction are too small to judge reliably from the full comparison board alone.

**Comparison history**

1. Earlier P1: on the first mobile Devices capture, the full device rail pushed the existing E911 action below the fold, so a first-time user still could not find where to enter the 911 address.
   Fix: added a mobile-only first-screen E911 shortcut above the device rail, wired directly to the existing carrier websheet flow, while preserving the automatic first-card dialog and desktop banner.
   Post-fix evidence: `/Volumes/Data/esim/hideck-ui-signal/qa-artifacts/mobile-devices.png`; Chrome reports eight visible E911/address text mentions for this state and no horizontal overflow.
2. Earlier P2: the initial mock data omitted production phone and MCP fields, which triggered error boundaries during visual capture.
   Fix: completed the deterministic QA fixture to match the existing API shapes; no production fallback or UI behavior was changed.
   Post-fix evidence: `/Volumes/Data/esim/hideck-ui-signal/qa-artifacts/browser-report-isolated.json` reports an empty errors array for all 14 final captures.

**Primary interactions tested**

- Authenticated route loading for Dashboard, Devices, Phone, SMS, Proxy, Commands, Tasks, Logs, and Settings.
- Fixed mobile navigation presence and labels on every authenticated H5 route.
- Center `拨号` tab navigation from every authenticated H5 route to `#/phone`.
- E911 first-screen action visibility and direct carrier-websheet handler wiring.
- Desktop and mobile viewport overflow and browser console/runtime errors.

**Implementation Checklist**

- [x] Keep the H5 dialer in the fixed center bottom tab.
- [x] Put the required 911 address action on the mobile Devices first screen.
- [x] Preserve the existing automatic E911 websheet and desktop E911 banner.
- [x] Reserve phone attribution layout space when lookup returns no result.
- [x] Verify all production routes at 1920 × 1080 and the key H5 routes at 390 × 844.
- [x] Pass lint, TypeScript, 402 tests, production build, visual comparison, interaction checks, overflow checks, and console checks.

**Follow-up Polish**

- P3: the production desktop shell carries more routes and status detail than the concept image, so its left rail is denser. This is an intentional product constraint and can be revisited only if route grouping is redesigned later.

final result: passed
