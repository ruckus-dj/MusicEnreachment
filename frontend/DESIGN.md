

## Settings and managed tools

The ordinary Settings screen follows the compact system-typography, green-accent, bordered-panel language and responsive spacing used by Setup (`setup.css` and the shared `AppButton`). Settings and configuration health are always read from the generated API; health problems remain visible after Setup and do not redirect to Setup. Output/publication, MusicBrainz, LRCLIB, and log-level forms save independently. Tools-root changes are explicit moves, never ordinary settings saves.

Managed FFmpeg package and fpcalc panels separate active, installed, and catalog-available releases. Installation and activation are separate actions. Install and move preflights expose exact conflict paths before confirmation; operations render REST snapshots, use SSE only to trigger snapshot re-reads, and retain retry/dismiss controls on failure. Catalog payloads stay in memory; only the last successful check timestamp is persisted in browser storage. All async outcomes have visible text, errors receive focus, sections have accessible names, and unavailable tool/platform actions are disabled without disabling unrelated settings.
