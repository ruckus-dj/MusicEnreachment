# Music Ingest library design contract

## 1. Direction

An evidence-led local library: warm archival paper, ink-blue navigation, terracotta decision accent, and restrained depth. The shell should feel like a calm record store back office: fast to scan, safe to edit, and explicit about what is original, analyzed, or final.

The product is source-agnostic. Lidarr, another service, and a manually populated incoming folder are all intake sources, not the library identity:

- **Sources** are immutable files found in configured incoming folders. Music Ingest may inspect, fingerprint, read tags, preserve observations, and record their disappearance, but it must never edit or delete them.
- **Publications** are files created by Music Ingest in the managed media folder from a source and a metadata revision.

The signature interaction is a stable record page: one persistent Music Ingest ID connects the canonical MusicBrainz recording/release identity, all source-file observations, the current publication file, and the complete metadata/history timeline.

## 2. Domain model and invariants

### 2.1 Stable library record

The primary UI and persistence identity is a `LibraryRecord`, not a path, filename, webhook, job, or current release row. It has an immutable `record_id` and an explicit MusicBrainz identity when known. Records without a confirmed match remain visible with an explicit `unmatched` state and a manual matching action.

### 2.2 Source file history

Each source version is append-only evidence: `source_id`, record ID, path, format, size, device/inode, SHA-256, observation state, disappearance timestamp, and origin. A changed file is a new source version, not an overwrite of the old observation. Source files are read-only from every Music Ingest action.

### 2.3 Publication history

Publications are managed outputs tied to the exact source and metadata revision that produced them. At most one publication version is `current`; missing or superseded versions remain visible as history.

### 2.4 Metadata and processing history

Metadata revisions are attached to the stable record and source. Preserve immutable `original` observations, derived `analyzed` proposals, and editable `final` revisions. Every edit, match, source replacement, publication, rollback, and failure is an append-only event with timestamp, actor, reason, and affected IDs.

The UI exposes separate dimensions instead of one overloaded status:

```text
source_state:       present | disappeared | replaced | never_seen
processing_state:   queued | analyzing | needs_review | retrying | quarantined | complete
match_state:        unmatched | candidate | matched | conflicted
publication_state:  absent | current | stale | superseded | missing | failed
metadata_state:     original | analyzed | final | edited
```

## 3. Tokens

- Surface: `#f5f2ec` page, `#fffdf9` panels, `#fbf5ed` selected rows, `#202c35` navigation.
- Ink: `#1c2730` primary, `#5d666e` muted, `#e4ded4` divider.
- Semantic: `#b64d32` action/diff accent, `#3c715d` ready state, `#f2dfd6` attention surface.
- Space: 4px base; 8px control gap, 16px field rhythm, 24px panel inset, 40px content gutter.
- Type: Newsreader for display/section headings; DM Sans for body; DM Mono for tags, paths, and revisions.
- Depth: low warm shadow on panels; tonal selected rows; motion only on interactive transform/background state changes.
- Rail: 252px desktop navigation rail; horizontal navigation below 700px.

## 4. Layout

The app shell uses a fixed dark navigation rail and a scrolling content workspace. The primary navigation is the library hierarchy: **Медиатека → Исполнители → Альбомы → Треки → Инспектор трека**. Source, publication, match, and metadata evidence belongs to the selected track inspector, not to duplicate catalog screens. The workspace uses a two-column inspector grid above 1100px, one column below it, and stacked navigation below 700px.

## 5. Screen responsibilities

### Медиатека / Исполнители

Answers “what do I have?” with a list of artists. The first screen is not a processing dashboard and never shows editable metadata fields.

### Альбомы

Answers “what releases belong to this artist?” with a separate album list and a clear back path.

### Треки

Answers “which files are in this album?” with one row per source file, processing/match state, and a direct route to the track inspector.

### Инспектор трека

Answers “what will be published?” in one focused screen: source path/hash/size, immutable source tags, fingerprint, AcousticID/provider evidence, history, and separate Original/Analyzed/Final metadata tabs. Only Final is editable; saving creates a new metadata revision and does not mutate the source file.

## 6. Accessibility

Use semantic headings, native buttons and labeled inputs, visible focus rings, readable contrast, responsive reflow, inline save/scan feedback, and never convey review state by color alone. Respect `prefers-reduced-motion`.

## 7. Primitives

### Navigation rail
- Structure: brand, media-library destination, refresh action, storage status.
- States: default, active, hover, focus, disabled.

### Entity and track row
- Structure: stable title, count/path, state, and explicit chevron/back destination.
- States: default, hover, selected, focus, empty catalog.

### Metadata inspector
- Structure: compact Original/Analyzed/Final comparison, labeled Final inputs only in edit mode, revision badge, save action, history.
- States: read-only comparison, editing Final, saving, success, stale revision/error.
- Accessibility: every input has a visible field label and save result is inline.

### Candidate review panel
- Structure: selected provider identity in a disclosure header, review reason, selectable MusicBrainz candidates, score, identity summary, and explicit confirmation action.
- States: unresolved and expanded, selected and collapsed, candidates available, no candidates, confirming, confirmed and publishing.
- Accessibility: each candidate is a native button with a text score and readable identity, never color alone.

### Scan action
- Structure: primary action with explicit counts and queued-analysis feedback.
- States: idle, scanning, complete, error.

## 8. Motion & interaction

- Interactive rows use 180ms background/transform transitions.
- Save and scan feedback is inline and does not interrupt the operator with a modal.
- Reduced motion disables non-essential transitions.

## 9. Accepted Debt

The React bundle is served as a production asset from FastAPI rather than through a separate deployment pipeline. Artwork thumbnails remain icon-based until a media-art endpoint exists; this does not hide metadata or alter navigation. The backend exposes the canonical FLAC tag allowlist; truly arbitrary vendor-specific tags remain intentionally rejected by the existing safety contract.

## 10. Recovery primitives

- `recovery-library-action`: sidebar action for one-click bulk recovery; disabled while a recovery request is active.
- `destination-replace-action`: inline scoped action inside the error banner; only shown for service-owned destination conflicts and returns to the track state after completion.
- Both use existing `.secondary`, focus-visible, disabled, and reduced-motion behavior.
