# Music Ingest library design contract

## 1. Direction

An evidence-led local library: warm archival paper, ink-blue navigation, terracotta decision accent, and restrained depth. The shell should feel like a calm record store back office: fast to scan, safe to edit, and explicit about what is original, analyzed, or final. The signature is the split catalog/editor view where a selected release stays visible while one track's final tags are revised.

## 2. Tokens

- Surface: `#f5f2ec` page, `#fffdf9` panels, `#fbf5ed` selected rows, `#202c35` navigation.
- Ink: `#1c2730` primary, `#5d666e` muted, `#e4ded4` divider.
- Semantic: `#b64d32` action/diff accent, `#3c715d` ready state, `#f2dfd6` attention surface.
- Space: 4px base; 8px control gap, 16px field rhythm, 24px panel inset, 40px content gutter.
- Type: Newsreader for display/section headings; DM Sans for body; DM Mono for tags, paths, and revisions.
- Depth: low warm shadow on panels; tonal selected rows; motion only on interactive transform/background state changes.

## 3. Layout

The app shell uses a fixed dark navigation rail and a scrolling content workspace. The workspace uses a two-column catalog/detail grid above `1100px`, one column below it, and stacked navigation below `700px`. Track lists scroll within the document; the tag editor stays adjacent to the selected track on desktop.

## 4. Accessibility

Use semantic headings, native buttons and labeled inputs, visible focus rings, readable contrast, responsive reflow, `role=status` save feedback, and never convey review state by color alone. Respect `prefers-reduced-motion`.

## 5. Primitives

### Navigation rail
- Structure: brand, primary nav buttons, storage status.
- States: default, active, hover, focus, attention count.
- Layout: fixed sidebar on desktop, horizontal overflow nav on small screens.

### Release row and track row
- Structure: button with cover/position, title, path, state, revision.
- States: default, hover, selected, focus, empty catalog.

### Tag editor
- Structure: layer tabs, labeled canonical tag inputs, revision badge, save action.
- States: editable final layer, read-only original/analyzed labels, saving, success, stale revision/error.
- Accessibility: every input has a visible field label and save result uses `role=status`.

### Group toolbar
- Structure: search input and folder/artist/album segmented controls.
- States: query, active grouping, empty results.

## 6. Motion & interaction

- Interactive rows use 180ms background/transform transitions.
- Save feedback is inline and does not interrupt the operator with a modal.
- Reduced motion disables non-essential transitions.

## 7. Accepted Debt

The React bundle is served as a production asset from FastAPI rather than through a separate deployment pipeline. The backend currently exposes the canonical FLAC tag allowlist; truly arbitrary vendor-specific tags remain intentionally rejected by the existing safety contract.
