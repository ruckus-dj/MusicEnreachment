# React design prototype

This is a standalone design artifact, never part of the production frontend.
By owner decision on 2026-11-03, this React prototype is the sole current
graphical reference. Retired static-prototype runtime files are not retained or
copied here. The moved Markdown files are preserved design prose, not a separate
live prototype or independent authority; current product decisions remain in
`../design/decisions.md`.

Keep production `frontend/` unchanged. Preserve approved behavior, fields,
identities, data and product semantics. Do not add screenshot artifacts.

Use React 19, TypeScript, Vite, Tailwind 4 and react-aria-components. The fixture
at `src/original-fixtures.json` is canonical, self-contained demo data. Do not
regenerate it from a retired prototype or add invented source hashes/provenance.
`check-fixtures.mjs` validates structure, identity uniqueness, references, and
critical existing identities; update it only when approved fixture identities
change. Do not reformat the fixture JSON.

All prototype styling must use Tailwind utilities in components or reusable
static utility strings. Keep the minimal Tailwind CSS entry to directives and
configuration only; do not add handwritten CSS selectors, component rules,
`@apply`, CSS-in-JS, or runtime class translators.

Artist and release details are always-visible, text-like readonly fields, not
track-tag comparison tables. Edit enables the same fields in place; the fixed-size
Edit/Save action and reserved Cancel space must not shift the layout. Saving
updates prototype library state; Cancel/Escape discard the draft. Provider
identities remain readonly. Source files and existing publications are not changed
by editing object metadata; file/publication comparisons belong to a track.

Sort names are edited only on the artist card, with no album/track overrides;
ordered credit sort tags derive from those values without display-name fallback.
Explicit fixture sort names are demo data. Keep default and catalog-backed metadata
projection consistent with that fixture. Editing unrelated artist fields must
preserve genres when the picker is untouched or its catalog is unavailable.

## Metadata regression prevention

- Keep one draft and one Save/Cancel boundary in Local forms. Controlled grouped
  edits must merge all paired names/IDs within an event, not overwrite one
  another with stale values.
- Keep the Edit/Save button `type="button"`; request form submission explicitly
  on Save. Changing its native type during a press can submit immediately when
  entering edit mode. Reserve readonly action space for credits, roles and works.
- React Aria pickers render in a portal: scope their tests to the dialog and
  close it before interacting with the underlying form. Escape closes only the
  picker; it must not discard the parent draft.
- Preserve position title overrides when editing a shared recording, including
  legacy fixtures without explicit override slots. Test projection as well as
  state: recording metadata must not overwrite the position's publication title.
- Display credits include join phrases; plural name/ID lists remain aligned.
  Navidrome advisory values are `1`/`4` for explicit and `2` for clean, not
  booleans. Work MBIDs require UUID validation despite their grouped editor kind.
