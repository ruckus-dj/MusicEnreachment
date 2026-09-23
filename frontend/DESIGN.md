# Music Ingest Design System

## 1. Atmosphere & Identity

Music Ingest is a dense, calm operator console with an archival editorial character. Warm paper surfaces and restrained terracotta actions sit beside a dark navigation rail; status is communicated with text and shape as well as color.

## 2. Color

| Role | Token | Value | Usage |
| --- | --- | --- | --- |
| Page surface | `--paper` | `#f5f2ec` | Workspace background and inputs |
| Panel surface | `--panel` | `#fffdf9` | Cards, buttons, dialogs |
| Selected surface | `--selected` | `#fbf5ed` | Hover, active and informational surfaces |
| Primary text | `--ink` | `#1c2730` | Body and headings |
| Navigation | `--nav` | `#202c35` | Sidebar and dark contrast |
| Secondary text | `--muted` | `#5d666e` | Supporting copy |
| Border | `--line` | `#e4ded4` | Structural separators |
| Action / error | `--accent` | `#b64d32` | Primary actions, focus and errors |
| Action soft | `--accent-soft` | `#f2dfd6` | Pending and error backgrounds |
| Success | `--green` | `#3c715d` | Complete and available states |
| Success soft | `--green-soft` | `#dfece3` | Success backgrounds |

Use `color-mix()` with these tokens for state variants. Never rely on color alone for status.

## 3. Typography

- Body: `DM Sans`, 14px default, 1.5 line height.
- Display: `Newsreader`, weight 600, for page and card headings.
- Technical: `DM Mono`, 9–24px depending on hierarchy, for identifiers, counts and statuses.
- Headings use the existing responsive `clamp()` scale; controls remain at least 11px with explicit labels.

## 4. Spacing & Layout

- Base unit: 4px.
- Tokens: `--space-1` (4px), `--space-2` (8px), `--space-3` (12px), `--space-4` (16px), `--space-5` (20px), `--space-6` (24px), `--space-8` (32px), `--space-12` (48px).
- Desktop shell: 252px sidebar plus a fluid workspace; content width is capped at 1280px.
- The workspace content owns vertical scrolling. Grids use `minmax(0, 1fr)` and collapse at 700px; primary content must remain usable from 320px.

## 5. Components

### Action button

- Structure: native `<button type="button">` with `.primary` or `.secondary`.
- States: default, hover, active, visible focus, disabled and loading label.
- Accessibility: native disabled semantics; loading and outcome text is announced by the shared status notice.
- Motion: 180ms background/transform transition; reduced-motion media query makes it effectively instant.

### Status notice

- Structure: `.notice-bar[role="status"]` with plain-language outcome and dismiss action.
- States: queued, success and error copy; never color-only.

### Panel

- Structure: bordered `.settings-screen`, `.inspector`, `.track-screen` or `.worker-queue-screen` surface.
- Radius: `--radius-panel`; depth: `--shadow`; internal groups use border separators.

### Action cluster

- Structure: `.provider-actions` wraps related operational buttons.
- Layout: wraps on desktop and stretches controls on mobile; long labels must not clip.

### Library navigation group

- Structure: the main `Медиатека` action is followed by an always-visible `.nav-submenu` with artist, album and track grouping destinations.
- States: the current grouping uses text plus the accent marker; the parent stays selected throughout catalog and track-detail routes.
- Responsive behavior: nested destinations remain grouped vertically in the desktop rail and become an always-visible three-column row in the mobile navigation strip.

## 6. Motion & Interaction

- `--ease`: 180ms ease for hover, press and state feedback.
- Animate only transform and color/background changes already present in the system.
- All controls need hover, active, focus-visible and disabled states.
- `prefers-reduced-motion: reduce` collapses transitions.

## 7. Depth & Surface

Use a mixed strategy: one-pixel structural borders plus the single `--shadow` elevation token for major panels. Nested cards use borders and tonal shifts rather than additional shadows. Dialogs may use the existing stronger tinted shadow.

## 8. Accessibility Constraints & Accepted Debt

- Target WCAG 2.2 AA, visible 3px focus outlines, semantic native controls, keyboard reachability, 44px mobile targets in settings, and non-color-only statuses.
- Operational mutations expose a changing text label while busy and a shared `role="status"` result.

| Item | Location | Why accepted | Exit |
| --- | --- | --- | --- |
| `--attention` is referenced but not declared | `src/styles.css` storage migration notice | Pre-existing inconsistency unrelated to metadata refresh | Define or replace during the next storage-settings styling change |
| Remote Google Fonts import | `src/styles.css` | Pre-existing runtime dependency | Self-host when the font asset pipeline is revisited |
