# Prototype redesign brief

Mode: Operate. Scope: the complete standalone prototype, not `frontend/`.

## Direction contract

**THESIS:** Make managing a music library as clear as the React Aria CRUD
table selected by the owner. Content and actions lead; decorative dashboard
cards and microscopic controls do not. The library uses the owner's
additional GridList gallery reference rather than forcing artwork-led
browsing into a table.

**OWN-WORLD:** Neutral white and graphite surfaces, restrained blue selection,
system sans-serif, fine gray dividers, softly rounded inputs and menus,
semantic status pills. Both themes receive equivalent treatment.

**STORY:** Navigate the existing library and source workflows, find and select
records, inspect details, and perform the existing actions with keyboard or
pointer. Preserve product requirements, fixtures, and both locales.

**FIRST VIEWPORT:** A stable navigation rail and compact top bar frame a
clear page title, contextual actions, search/filter toolbar, and substantial
table. Selection and row actions stay close to the affected records.
In the library, a GridList gallery replaces the main table while preserving
search, captions, multiple selection, and keyboard navigation.
The signature interaction is consistent selection and contextual action
across artwork galleries and data tables.

**FORM:** seed: none (pinned). The owner requests “по стилю и UI ... вот эту
таблицу ... https://react-aria.adobe.com/examples/crud” across the prototype,
with light and dark themes and maximum appropriate use of React Aria examples.

**FINISH:** unreviewed and undocumented is unfinished; this build ends with
the finish review, the verdict, DESIGN.md, and every shipping raster carrying
its provenance.

## Approved UX follow-up

The owner approved all six follow-up refinements:

- Incoming links say “Match” / “Сопоставить”; the group name remains in the
  accessible label, not repeated in the visible action.
- Artist cards retain audio availability and publication counts; the artist
  page remains the place for the full statistics.
- Visible guidance explains that selecting an artist selects positions.
  Mixed selection and keyboard navigation remain available.
- Active search and filter conditions appear as removable tags with a reset
  action; changes keep canonical URL state and pagination behavior.
- Settings separate essential inline instructions from expandable detail.
  Dangerous consequences, validation, conflicts, and save state stay visible.
- Frequent actions remain explicit; rare secondary actions use an accessible
  ellipsis menu without bypassing existing confirmations.

These changes refine the established visual system, not product semantics.
