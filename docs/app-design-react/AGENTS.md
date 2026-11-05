# React design prototype

This is a standalone design artifact, never part of the production frontend at
runtime.
By owner decision on 2026-11-03, this React prototype is the sole current
graphical reference. Retired static-prototype runtime files are not retained or
copied here. The moved Markdown files are preserved design prose, not a separate
live prototype or independent authority; current product decisions remain in
`../design/decisions.md`.

For prototype-only tasks, keep production `frontend/` unchanged. The owner has
explicitly approved porting prototype presentation modules into production with
production API/controllers, while keeping this prototype standalone. UI/UX
improvements are synchronized in both surfaces. Preserve approved behavior,
fields, identities, data and product semantics. Do not add screenshot artifacts.

## Approved prototype-to-production port

- Copy/adapt presentation modules into production; production code must not import
  from `docs/app-design-react/`, and must not use prototype demo fixtures or
  prototype execution/data models. Connect the port to production APIs and
  controllers.
- Prefer controlled view extraction with production-owned adapters. Port prototype
  `ui/index.tsx`, static style modules, tokens in `index.html`, i18n,
  metadata editor, `MatchingDnD`, and `LocalProviderScreen` where suitable.
- Preserve production concurrency/SSE behavior and directory controllers. Do not
  replace them with prototype workflows. No blanket shared-package prerequisite
  is required before porting.
- Development does not require backwards compatibility or theme-key compatibility.

For prototype/documentation-only work, the owner explicitly requested scoped
verification: `task design:check` and live prototype checks, not application
`task verify`, application CI, or waiting for the platform matrix. Do not
reintroduce application checks through the commit hook for such changes.

Use React 19, TypeScript, Vite, Tailwind 4 and react-aria-components. The fixture
at `src/original-fixtures.json` is canonical, self-contained demo data. Do not
regenerate it from a retired prototype or add invented source hashes/provenance.
`check-fixtures.mjs` validates structure, identity uniqueness, references, and
critical existing identities; update it only when approved fixture identities
change. Do not reformat the fixture JSON.

Format explicit TypeScript/TSX paths, not the entire `src` directory: a recursive
Biome write also reformats the canonical JSON and attempts to parse Tailwind's
CSS directives without the prototype parser configuration. Keep fixture bytes
unchanged. RTL assertions must scope duplicate controls to their package/section
and distinguish notices from independent health/status regions; optional empty
aligned provider IDs are not invalid metadata merely because projection emits a
nonblocking completeness notice.

All prototype styling must use Tailwind utilities in components or reusable
static utility strings. Keep the minimal Tailwind CSS entry to directives and
configuration only; do not add handwritten CSS selectors, component rules,
`@apply`, CSS-in-JS, or runtime class translators.

Artist details and release/recording basics are always-visible, text-like
readonly fields, not track-tag comparison tables. Additional metadata uses
thematic disclosures available in both modes; do not expand every group on Edit,
render an empty form for every role, or repeat all metadata in a default-open
comparison table. Keep each field in one editing group and preserve the shared
draft when groups close. Edit enables the same visible fields in place; the fixed-size
Edit/Save action and reserved Cancel space must not shift the layout. Saving
updates prototype library state; Cancel/Escape discard the draft. Provider
identities remain readonly. Source files and existing publications are not changed
by editing object metadata; file/publication comparisons belong to a track.

Disclosure groups form one vertical stack, with the same full width closed and
open. Never put their headers in multiple columns or change grid span on open:
expansion may move later content down, not sideways. Multi-column field grids
are allowed inside an already expanded group, without changing its outer width.

Sort names are edited only on the artist card, with no album/track overrides;
ordered credit sort tags derive from those values. By owner decision on
2026-11-05, a missing sort name falls back to the linked artist's display name
only during tag projection; never populate the stored artist field. If different
artists in a credit group have identical projected sorts, omit both sort tags of
that group and show a nonblocking localized preview notice, including removal of
existing publication sort tags in the diff. Keep the other group unchanged.
Explicit fixture sort names are demo data. Keep default and catalog-backed metadata
projection consistent with that fixture. Editing unrelated artist fields must
preserve genres when the picker is untouched or its catalog is unavailable.

## Metadata regression prevention

- Keep one draft and one Save/Cancel boundary in Local forms. Controlled grouped
  edits must merge all paired names/IDs within an event, not overwrite one
  another with stale values.
- Keep the Edit/Save button `type="button"`; request form submission explicitly
  on Save. Changing its native type during a press can submit immediately when
  entering edit mode. Keep compact action tracks without adding empty action rows
  or placeholder forms for unused roles to the default view.
- React Aria pickers render in a portal: scope their tests to the dialog and
  close it before interacting with the underlying form. Escape closes only the
  picker; it must not discard the parent draft.
- Preserve position title overrides when editing a shared recording, including
  legacy fixtures without explicit override slots. Test projection as well as
  state: recording metadata must not overwrite the position's publication title.
- Display credits include join phrases; plural name/ID lists remain aligned.
  Navidrome advisory values are `1`/`4` for explicit and `2` for clean, not
  booleans. Work MBIDs require UUID validation despite their grouped editor kind.

## Localization and interaction regression prevention

- RU/EN copy uses semantic catalogs; store persistent notices as descriptors and
  format at render time. Do not translate metadata values, paths, filenames or
  unknown tags. Locale changes must not remount editors, reset drafts, rerun
  actions or move focus to the page heading. Keep `document.lang`, titles and
  React Aria's locale synchronized.
- The MusicBrainz snapshot is separate from `original-fixtures.json`; preserve
  real MBIDs and source names. Exact genre-search matches must be reachable on
  the first result page. When paging/filtering a multiselect, retain selections
  outside the visible results. Do not mount thousands of picker options simply
  to make an unscoped test query succeed; test the ordinary search workflow.
- React Aria `useDrop` registers and focuses the supplied ref during keyboard
  dragging. With a separate drop button, point that ref at the focusable button,
  not its nonfocusable layout wrapper. Verify native mouse drag and keyboard
  Enter/Tab/Enter, not only direct callback invocation.
- Reserve matching feedback space before the first notice appears: fixed row
  heights alone do not prevent the entire table moving after assignment.
  Inspect both row dimensions and its position before add/remove.
- Modal keyboard tests dispatch to the focused dialog and await focus restoration;
  document-level synthetic key events do not model an event bubbling from the
  modal. Always restore media-query mocks even when an assertion fails.
- JSDOM does not apply responsive Tailwind visibility: mobile rows and the
  desktop React Aria grid coexist in unit tests. Scope mobile label queries to
  their container and use the desktop's actual `grid` role, not `table`. Keep
  browser checks for visibility, reflow and focus across breakpoints; a duplicate
  label in JSDOM is not evidence that both surfaces are visible in the browser.
