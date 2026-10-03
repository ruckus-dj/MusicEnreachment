# Managed source-I/O transport evidence — pending

Status: provisioning and CI wiring are being added; no native-run evidence has
been collected or reviewed yet. This document intentionally records no test
outcomes, packet hashes, or release identities before CI executes.

## Evidence lane

`task fixtures:managed-transport` uses the managed-tools catalog and lifecycle
to resolve an approved numbered FFmpeg release for the native CI platform,
download artifacts, require a non-empty published SHA-256 for each artifact,
verify each downloaded archive, and materialize and verify the managed
`ffmpeg`/`ffprobe` executables. macOS uses the approved release catalog's
separate `ffmpeg.zip` and `ffprobe.zip` archives. It generates
`seek-dependent.mp4` with ALAC audio and verifies that the MP4 `mdat` atom
precedes a trailing `moov` atom, so testing requires seeking rather than relying
on a fast-start index.

The command accepts explicit `-release`, `-output`, and `-manifest` flags; it
does not use environment variables to select its source or release. The
default `-release latest` selects the newest numbered release returned by the
approved platform catalog and records its actual identity in the manifest. A
run that cannot obtain and verify published checksum evidence fails rather
than silently treating the archive as verified.

The manifest is written to `build/tool-fixtures/manifest.json` and records the
native platform, approved source, resolved release, artifact URLs/SHA-256/byte
counts, executable version output and paths, and fixture path. The native CI
matrix uploads this manifest and its lane log even when the lane fails. The
managed transport tests consume this manifest using the explicit test-binary
flag `-managed-manifest`.

## Pending review

After the native CI matrix runs, review and add the per-platform manifest and
lane results here (including any failures or unavailable checksum evidence).
Do not claim protocol-fd coverage, stream/tag equivalence, raw-filename
handling, backward packet-hash agreement, or negative-case behavior based on
fixture provisioning alone; those claims require the corresponding managed
transport tests and their collected output.
