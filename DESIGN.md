# Music Ingest library design contract

## 1. Direction

An evidence-led local library: warm archival paper, ink-blue navigation, terracotta decision accent, and restrained depth. The shell should feel like a calm record store back office: fast to scan, safe to edit, and explicit about what is original, analyzed, or final.

The product is source-agnostic. Lidarr, another service, and a manually populated incoming folder are all intake sources, not the library identity. The library has two deliberately separate sides:

- **Sources** are immutable files found in configured incoming folders. Music Ingest may inspect, fingerprint, read tags, preserve observations, and record their disappearance, but it must never edit or delete them.
- **Publications** are files created by Music Ingest in the managed media folder from a source and a metadata revision. Their folder structure, filename, and format may differ from the source.

The signature interaction is a stable record page: one persistent Music Ingest ID connects the canonical MusicBrainz recording/release identity, all source-file observations, the current publication file, and the complete metadata/history timeline. Replacing an input, losing an input, adding a better FLAC after an MP3, or republishing must change the linked file/version state, never the record ID.

## 2. Domain model and invariants

### 2.1 Stable library record

The primary UI and persistence identity is a `LibraryRecord` (working name), not a path, filename, webhook, job, or current release row. It has an immutable `record_id` and an explicit MusicBrainz identity when known:

```text
LibraryRecord
  record_id                 immutable Music Ingest ID
  musicbrainz_recording_id  required once a composition is identified
  musicbrainz_release_id    optional release-level identity
  musicbrainz_artist_id     optional artist identity
  sources[]                 historical and current source-file versions
  publication               current or historical managed output
  metadata_revisions[]      original, analyzed, final, and edit history
  processing_events[]       inspections, matches, failures, retries, decisions
```

The MusicBrainz ID is an identity link, not a replacement for `record_id`: identity matching can be corrected or deferred, while the service record remains stable. Records without a confirmed MusicBrainz match remain visible with an explicit `unmatched` state and a manual matching action.

### 2.2 Source file history

Each source version is append-only evidence:

```text
SourceVersion
  source_id       immutable file-version ID
  record_id       LibraryRecord foreign key
  path            last observed incoming path
  format          flac, mp3, ...
  size_bytes
  device/inode    when available
  sha256          content identity
  observed_at
  disappeared_at  nullable; absence is a state, not deletion from history
  origin          service, manual, or another adapter
```

The same `LibraryRecord` may have multiple source versions. An MP3 source can be followed by a FLAC source; both remain linked to the same record. A changed file is a new source version, not an overwrite of the old observation. The service can mark a publication stale and offer reprocessing from the selected/newest source, but cannot mutate the incoming file.

### 2.3 Publication history

Publications are managed outputs and may be replaced safely:

```text
PublicationVersion
  publication_id  immutable output-version ID
  record_id       LibraryRecord foreign key
  source_id       source used to create this output
  path            managed media path
  format
  content_sha256
  metadata_revision_id
  state           current, superseded, missing, or failed
  created_at
```

At most one publication version is `current` for a record. A missing current publication is retained as history and exposes a recovery action; it must not make the `LibraryRecord` disappear. A publication may be regenerated from a newer source or metadata revision while retaining the same `record_id`.

### 2.4 Metadata and processing history

Metadata revisions are attached to the stable record and may also be associated with a source/publication version. Preserve immutable `original` observations, derived `analyzed` proposals, and editable `final` revisions. Every edit, match, source replacement, publication, rollback, and failure is an append-only event with timestamp, actor, reason, and affected IDs.

The UI must expose separate dimensions instead of one overloaded status:

```text
source_state:       present | disappeared | replaced | never_seen
processing_state:   queued | analyzing | needs_review | retrying | quarantined | complete
match_state:        unmatched | candidate | matched | conflicted
publication_state:  absent | current | stale | superseded | missing | failed
metadata_state:     original | analyzed | final | edited
```

### 2.5 Non-negotiable safety rules

- Source files are read-only from every Music Ingest action. No source delete, rename, tag rewrite, or in-place replacement.
- A path is a location observation, never an identity. Paths and filenames may change without changing `record_id`.
- A source disappearing is recorded as `disappeared`, with its last path, hash, size, format, and observation time preserved.
- A newly discovered source is linked to an existing record only by explicit identity/evidence, never by filename alone.
- A publication is always traceable to the exact `source_id` and metadata revision that produced it.
- A source replacement can mark the publication `stale` and schedule regeneration; it must not silently overwrite history.
- A record may have many source versions and publication versions, but one stable `record_id` and one clearly marked current publication.

## 3. Tokens

- Surface: `#f5f2ec` page, `#fffdf9` panels, `#fbf5ed` selected rows, `#202c35` navigation.
- Ink: `#1c2730` primary, `#5d666e` muted, `#e4ded4` divider.
- Semantic: `#b64d32` action/diff accent, `#3c715d` ready state, `#f2dfd6` attention surface.
- Space: 4px base; 8px control gap, 16px field rhythm, 24px panel inset, 40px content gutter.
- Type: Newsreader for display/section headings; DM Sans for body; DM Mono for tags, paths, and revisions.
- Depth: low warm shadow on panels; tonal selected rows; motion only on interactive transform/background state changes.

## 4. Layout

The app shell uses a fixed dark navigation rail and a scrolling content workspace. The top-level navigation is source-agnostic:

- **Состояние** — pipeline counters, stale/missing records, recent failures, and current work.
- **Медиатека** — searchable stable records with source/publication view modes.
- **Исходники** — immutable incoming files, source versions, disappearance/replacement status, and safe rescan actions.
- **Публикации** — managed outputs, current/superseded versions, stale outputs, and regeneration actions.
- **Требуют внимания** — actionable queue grouped by reason.
- **Метаданные** — separate metadata and revision editor.

The workspace uses a two-column catalog/detail grid above `1100px`, one column below it, and stacked navigation below `700px`. Track lists scroll within the document; metadata editing is a route of its own and never occupies the library detail pane.

## 5. Screen responsibilities

### Состояние

Answers “what needs action now?” without becoming a second catalog. Show counts by the independent status dimensions, active jobs, records with missing sources/publications, and recent processing events. Every warning links to the stable record and its reason.

### Медиатека

Answers “find this composition.” Search artist/title/MusicBrainz ID/record ID/path/hash. Views include records, album/release grouping, artist grouping, and a source/publication comparison. Each row shows the stable ID, identity confidence, current source summary, publication summary, metadata revision, and the most important reason. Do not place editable metadata fields here.

### Исходники

Answers “what did the service receive?” Show incoming path, format, hash, last observation, source origin, immutable tags, analysis state, and linked record/publication. Actions are limited to rescan, analyze, match, choose a source version, and reprocess; never delete or edit the file.

### Публикации

Answers “what did the service create?” Show managed path, format, content hash, source used, metadata revision, publication state, and version history. Actions are republish, regenerate from another source version, restore a prior metadata revision, and inspect the resulting file. Missing or stale outputs remain visible.

### Требуют внимания

Answers “which decision can I make?” Group by unmatched identity, ambiguous match, missing source, stale publication, processing failure, and metadata conflict. Each item has one primary next action and a concise reason; deeper evidence opens the stable record page.

### Метаданные

Answers “what metadata will be published?” Show one record or track at a time with `Original`, `Analyzed`, and `Final` layers, diff, revision history, MusicBrainz link, save/republish state, and rollback. Source observations are read-only. Publication regeneration is explicit after a final metadata change.

## 6. Accessibility

Use semantic headings, native buttons and labeled inputs, visible focus rings, readable contrast, responsive reflow, `role=status` save feedback, and never convey review state by color alone. Respect `prefers-reduced-motion`.

## 7. Primitives

### Navigation rail
- Structure: brand, primary nav buttons, storage status.
- States: default, active, hover, focus, attention count.
- Layout: fixed sidebar on desktop, horizontal overflow nav on small screens.

### Release row and track row
- Structure: stable record ID, identity, source summary, publication summary, state, reason, revision.
- States: default, hover, selected, focus, source missing, publication stale, unmatched, empty catalog.

### Source/publication relationship row
- Structure: source version, publication version, hashes, paths, metadata revision, link to stable record.
- States: linked, source disappeared, source replaced, publication current, publication stale, publication missing.

### Tag editor
- Structure: layer tabs, labeled canonical tag inputs, revision badge, save action.
- States: editable final layer, read-only original/analyzed labels, saving, success, stale revision/error.
- Accessibility: every input has a visible field label and save result uses `role=status`.

### Group toolbar
- Structure: search input, source/publication scope, status filters, and folder/artist/album/record grouping controls.
- States: query, active grouping, empty results.

### Reason panel
- Structure: current status, human explanation, evidence timestamp, affected source/publication IDs, next action.
- States: informational, attention, retryable failure, quarantined, resolved.

## 8. Motion & interaction

- Interactive rows use 180ms background/transform transitions.
- Save feedback is inline and does not interrupt the operator with a modal.
- Reduced motion disables non-essential transitions.

## 9. Accepted Debt

The React bundle is served as a production asset from FastAPI rather than through a separate deployment pipeline. The backend currently exposes the canonical FLAC tag allowlist; truly arbitrary vendor-specific tags remain intentionally rejected by the existing safety contract.

The current persistence model still has legacy source-to-release one-to-one paths and synthetic release identifiers. Before implementing the new screens, the domain model must gain the stable `LibraryRecord`, append-only source/publication versions, explicit source replacement links, and MusicBrainz identity fields described above. The UI must not paper over that gap by treating `ReleaseRecord` or a filesystem path as the permanent identity.
