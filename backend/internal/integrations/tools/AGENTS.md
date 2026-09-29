# backend/internal/integrations/tools

**Score: 13** (distinct domain: 6 files, module boundary, code-heavy)

## OVERVIEW
Managed external tool integrations: ffmpeg, ffprobe, fpcalc. Handles GitHub release discovery, download, verification, and installation. Never bundles tools in Docker image; never resolves from system PATH.

## WHERE TO LOOK
- catalog.go (233 lines) - GitHub release adapters: Catalog, GitHubAdapter, MacOSAdapter for approved sources
- lifecycle.go (272 lines) - Lifecycle.Materialize: download, verify, install flow
- tools.go - Tool interface, Registry
- catalog_test.go, lifecycle_test.go, tools_test.go - unit tests for adapter logic

## STRUCTURE
Three-layer design:
- Tool interface: defines what a tool provides (name, version, binary paths)
- Catalog: discovers available versions from GitHub releases (platform-specific adapters)
- Lifecycle: materializes a tool (download, verify checksum, extract, install to managed path)

## CONVENTIONS
**Approved sources only**: 
- Chromaprint releases (fpcalc)
- BtbN FFmpeg-Builds for Win/Linux
- martin-riedl release builds ONLY for macOS (snapshot links explicitly forbidden)

**Never bundled**: Tools are downloaded at runtime, never shipped in Docker image, never resolved from system PATH.

**Quality limits forbidden**: No artificial upper limits on quality values; no auto-downgrades (design doc requirement).

## ANTI-PATTERNS
- Snapshot ffmpeg links for macOS (MacOSAdapter enforces release archives only, see catalog.go comment)
- Bundling tools in Docker image
- Resolving tools from system PATH
- Auto-selecting same-or-worse-quality source over existing publication
- Skipping checksum verification (all downloads must verify)
- Mixing managed and system tools (always use managed path)

## NOTES
The approved release catalog, HTTPS downloads, checksum verification, extraction,
and managed-path materialization are wired into the backend install operation
flow. Setup and the post-Setup Settings UI call the backend catalog and install
APIs; River workers perform the download/verify/materialize operation. Do not
reintroduce the obsolete claim that automatic tool installation is unimplemented.
