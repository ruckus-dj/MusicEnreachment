# Music Ingest Design System

## 1. Atmosphere & Identity

Music Ingest is a calm operator console for a self-hosted media pipeline. It combines a warm paper workspace with a dark fixed navigation rail and restrained brick and green semantic accents. The signature is editorial headings inside a practical, provenance-first control surface: dense enough for operations, but never terminal-like or visually noisy.

## 2. Color

### Palette

| Role | Token | Value | Usage |
|---|---|---:|---|
| Workspace | `--paper` | `#f5f2ec` | Page background and quiet input surfaces |
| Panel | `--panel` | `#fffdf9` | Cards, dialogs, primary controls |
| Selected | `--selected` | `#fbf5ed` | Selected rows and grouped settings surfaces |
| Text | `--ink` | `#1c2730` | Primary text |
| Navigation | `--nav` | `#202c35` | Sidebar and dark structural surfaces |
| Muted text | `--muted` | `#5d666e` | Secondary copy and metadata |
| Divider | `--line` | `#e4ded4` | Borders and separators |
| Control boundary | `--control-line` | 55% `--ink` mixed with `--panel` | Default interactive boundaries with at least 3:1 contrast |
| Action / caution | `--accent` | `#b64d32` | Primary actions, focus, destructive attention |
| Action soft | `--accent-soft` | `#f2dfd6` | Warning and error backgrounds |
| Success | `--green` | `#3c715d` | Healthy and completed states |
| Success soft | `--green-soft` | `#dfece3` | Success backgrounds |

### Rules

- The interface is light-mode only until a complete dark palette is designed and tested.
- `--accent` marks interaction or operational attention, never decoration.
- Destructive actions require explanatory text and may not rely on color alone.
- New colors must be added here before use. Existing `color-mix()` expressions derive from these tokens.

## 3. Typography

### Scale

| Level | Size | Weight | Line height | Usage |
|---|---:|---:|---:|---|
| Page title | `clamp(42px, 5vw, 68px)` | 600 | 0.96 | Main screen title |
| Section title | `clamp(25px, 3vw, 34px)` | 600 | 1 | Major section title |
| Card legend | 18px | 600 | normal | Settings cards |
| Body lead | 15px | 400 | 1.65 | Screen introduction |
| Body | 14px | 400 | 1.5 | Default application text |
| Compact | 12px | 400-600 | 1.5 | Controls and contextual text |
| Metadata | 10-11px | 400-600 | 1.4 | IDs, statuses, helper text |

### Font Stack

- Display: `Newsreader, Georgia, serif`
- Primary: `DM Sans, sans-serif`
- Mono: `DM Mono, monospace`

### Rules

- Newsreader is reserved for hierarchy, not body copy.
- IDs, counts, filesystem paths, states, and timestamps use DM Mono.
- Body and control text never falls below 11px; essential instructional copy stays at 12px or above.

## 4. Spacing & Layout

### Base Unit

Spacing uses a 4px base through `--space-1`, `--space-2`, `--space-3`, `--space-4`, `--space-5`, `--space-6`, `--space-8`, and `--space-12`.

### Grid

- Desktop shell: fixed 252px sidebar plus `minmax(0, 1fr)` workspace.
- Workspace content limit: 1280px.
- Settings use an intrinsic two-column grid and collapse to one column below 700px.
- Reusable intrinsic grids use `minmax(min(<floor>, 100%), 1fr)` to avoid narrow-screen overflow.
- The `.content` region is the vertical scroll owner; sidebar and top bar remain structural shell regions.

### Rules

- Primary content must reflow to one readable column at 375px without horizontal scrolling.
- Long paths and identifiers use `overflow-wrap: anywhere`.
- Touch targets in Settings are at least 44px high on narrow screens.

## 5. Components

### Application Shell

- **Structure**: sidebar navigation, top bar, scrollable content region.
- **States**: active navigation, hover, focus, compact mobile navigation.
- **Accessibility**: landmark elements, `aria-current`, visible focus.
- **Motion**: 180ms transform/color feedback; removed under reduced motion.
- **Layout**: fixed-sidenav shell on desktop, document flow on mobile; `.content` owns desktop vertical scrolling.

### Action Button

- **Variants**: `.primary`, `.secondary`, quiet navigation action.
- **States**: default, hover, active press, focus-visible, disabled, loading through disabled label.
- **Spacing**: `--space-2` to `--space-3` internal rhythm.
- **Accessibility**: native button semantics, visible focus, descriptive loading labels.
- **Motion**: 180ms color/transform feedback only.

### Settings Card

- **Structure**: fieldset with legend, labels above controls, helper text below groups.
- **Variants**: regular and `.settings-card-wide`.
- **States**: loading, empty, error, read-only, saving.
- **Accessibility**: native fieldset/legend grouping; labels are never placeholders.
- **Layout**: grid child; wide variant spans the settings grid and collapses naturally on mobile.

### Operational Status Message

- **Structure**: concise heading or sentence plus optional action.
- **Variants**: neutral, warning/error with accent-soft, success with green-soft.
- **States**: preview ready, no work, applying, applied, failed.
- **Accessibility**: `role="status"` for updates and `role="alert"` for failures; status is communicated in text, not color alone.
- **Motion**: none beyond existing color transition.

### Destructive Maintenance Panel

- **Structure**: explanation, preview summary, affected-count details, preview action, explicit apply action.
- **Variants**: unpreviewed, preview-empty, preview-has-work, applying, applied, error.
- **States**: apply remains disabled until a successful preview reports work; any failed or completed apply invalidates the previous preview.
- **Accessibility**: native buttons, confirmation through a second explicit action, live status text, no surprise mutation on page load.
- **Layout**: wide Settings card, action cluster wraps on narrow containers.

### Modal / Picker

- **Structure**: backdrop and bounded scrolling dialog surface.
- **States**: open, focus-visible controls, mobile full-height-safe layout.
- **Accessibility**: the existing folder picker must retain keyboard reachability; new maintenance work does not introduce a modal.
- **Layout**: viewport-bounded overlay with its own named scroll owner.

## 6. Motion & Interaction

| Type | Duration | Easing | Usage |
|---|---:|---|---|
| Micro | 180ms | ease | Hover, active, selection, color feedback |
| Reduced motion | 0.01ms | linear | Global reduced-motion override |

- Motion communicates affordance or state change only.
- Animate transform and color/opacity, never layout dimensions.
- `prefers-reduced-motion: reduce` disables non-essential transitions and smooth scrolling.

## 7. Depth & Surface

The strategy is mixed but restrained: 1px semantic borders define controls and groups; `--shadow` is reserved for top-level panels and dialogs. Nested Settings cards do not add shadows. Radii use `--radius-tight` (6px) for controls and nested cards, and `--radius-panel` (12px) for screen-level surfaces.

## 8. Accessibility Constraints & Accepted Debt

### Constraints

- WCAG 2.2 AA target: 4.5:1 body contrast, 3:1 large text and control boundaries.
- Every interactive element is keyboard reachable and has a visible focus ring.
- Status changes use semantic live regions where asynchronous work is involved.
- Controls remain labeled and usable at 375px, 768px, and 1280px widths.
- Reduced-motion preference is honored.

### Accepted Debt

| Item | Location | Why accepted | Owner / Exit |
|---|---|---|---|
| Google Fonts are loaded through CSS `@import` | `frontend/src/styles.css` | Existing behavior retained during this focused maintenance feature | Replace with self-hosted font files during a dedicated asset/performance pass |
| Dark mode is not implemented | Whole application | Existing product is intentionally a single light operational theme | Add only with a complete tested palette and explicit product decision |
