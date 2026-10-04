# Managed source-I/O transport evidence

Status: **COMPLETE** for the approved scope as of 2026-10-04. Final code
revision: `a42f09fded69fbd6b90f22122392aeb27c69e7f1`. The plan's step 12,
independent review, real manual scenario, and current full native CI completed.
At completion, Windows UNC/SMB was excluded from the then-approved slice; the
owner decision dated 2026-10-04 clarifies this is an implementation limitation,
not a permanent product prohibition. See the
[independent review](plan06-independent-review-2026-10-04.md) and
[manual acceptance record](plan06-acceptance-2026-10-03.md).

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
does not use environment variables to select its source or release. The default
`-release latest` selects the newest numbered release returned by the approved
platform catalog and records its actual identity in the manifest. A run that
cannot obtain and verify published checksum evidence fails rather than silently
treating the archive as verified.

The CI matrix uploads each manifest and lane log. The manifests for CI run
[37153819171](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/37153819171)
are retained under `plan06-ci-a42f09f-managed-artifacts`; the complete job log
is `plan06-ci-a42f09f-full.log`. The table records manifest values, including
archive hashes; archive bytes were not independently re-downloaded or rehashed
for this report. CI provisioning verified the published checksums. BtbN's
catalog identity is the mutable `9.0` latest selector; the actual executable
version recorded by CI is `n9.0.2-22-g46d8f462ee-20261003`. macOS catalog release
is `9.0.2`.

| Native CI platform | Catalog release; executable version | Manifest artifact SHA-256 |
| --- | --- | --- |
| Linux amd64 | `9.0`; `n9.0.2-22-g46d8f462ee-20261003` | `ffmpeg-n9.0-latest-linux64-gpl-9.0.tar.xz`: `b28d9af79527302004b380a69b3d78edffc9ccabbf23f70d963945c744bc7ec2` |
| Linux arm64 | `9.0`; `n9.0.2-22-g46d8f462ee-20261003` | `ffmpeg-n9.0-latest-linuxarm64-gpl-9.0.tar.xz`: `b4b133b8923dffc6bd34d53b49c89865ba0259e661b87d5ed96f256457e3f4d6` |
| macOS x64 | `9.0.2`; `9.0.2` | `ffmpeg.zip`: `7c6b4125b191cbf773832dc51f424cf2b6bb7da43007d1e066f95909e47cacd4`; `ffprobe.zip`: `2322438ed2f6319a691291b247d09c69dcaa3a982460d1f269a7e1af335cfdfd` |
| macOS arm64 | `9.0.2`; `9.0.2` | `ffmpeg.zip`: `c8ed4c4e6978a03c485edbfe4e0a5dc2380f8a30bba5150531b31b094492d924`; `ffprobe.zip`: `fcbe839537485eaee7a7a8bc5cbc0f90d53617e80943e8a5b2e31cb851197ea6` |
| Windows amd64 | `9.0`; `n9.0.2-22-g46d8f462ee-20261003` | `ffmpeg-n9.0-latest-win64-gpl-9.0.zip`: `2e2b8f168bec7ca279f20b22b78c626093c8757ab70c152aea24eb71d82ca31d` |

The test binaries consume the corresponding manifest with `-managed-manifest`.
Actual CI output (not fixture provisioning alone) records the real managed
transport and source-filesystem test results. Per-platform evidence and the
Windows-specific caveat are in the next section and the independent review.

## Native CI evidence

Earlier runs are retained as historical evidence and are not rewritten as
successful final gates:

- CI `37138601965` (head `2784703`): managed ffprobe transport and source-fs
  checks passed, but overall CI failed on unrelated Windows settings fixtures.
- CI `37139210470` (head `84a0801`): transport checks and the Windows mandatory
  junction case passed, but overall CI failed on settings. Step 8 was accepted
  independently within its scope at that revision.
- Fix `2fb0cb1` addressed synthetic Normalize host handling on Windows.
- CI `37137104810` was provisioning only: manifest load failed on the `pkg`
  working directory, so it supplied no actual transport test evidence.
- CI runs `7d31194` / `12963b8` failed; those historical failures are not
  presented as passing or as the final gate.

Final full CI [37153819171](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/37153819171)
at `a42f09fded69fbd6b90f22122392aeb27c69e7f1` completed **all jobs successfully**,
including verification, Compose, the native Linux/macOS/Windows matrix, and
publish. Managed transport tests passed on all five platform targets with the
approved real managed tools and seek-dependent fixture. The Windows run passed
the non-skipped `TestWindowsAdapterUsesPinnedDirectoryAcrossJunctionSwap` and
`TestWindowsAdapterRejectsJunctionsAndClosesFailedOpens` checks. The native analysis test also attempted ancestor replacement;
the attempted namespace operation was denied with `ERROR_ACCESS_DENIED`, the
namespace remained unchanged, and the test passed. This is a pass of the actual
Windows adapter test, but **not** evidence that Windows permits a successful
ancestor swap. Windows legacy-relative-path handling passed; UNC rejection
passed. The report does not claim actual Windows UNC/SMB access or manual
acceptance.

On Unix, the real native tests include pinned-directory/source swap behavior;
the five-target matrix verifies the seekable managed ffprobe path, fd transport,
tags, raw filename behavior and negative case through the tests' assertions.
The platform/test specifics and limits are summarized in the independent review.

## Implementation behavior, product criterion, and remaining limitations

- At completion, Windows UNC/SMB source roots were excluded from the then-approved
  slice: create/edit/revalidation reject before filesystem access; scan and
  analysis return an actionable unsupported-path error; existing stored roots
  remain readable. The owner
  decision dated 2026-10-04 clarifies this is an implementation limitation, not
  a permanent product prohibition. If NAS access is required, ordinary pathname
  and `stat` checks may satisfy the product criterion subject to functional
  requirements; this report does not claim UNC currently works.
- Root-scoped source I/O pins directory/file handles and rejects symlink/reparse
  escapes. These stricter mechanisms remain in the current implementation, but
  are not product acceptance requirements. Product freshness is established by
  matching size and `mtime`; immutable bytes/digests and concurrent path/link
  replacement outside ordinary trusted use are not required. This does not
  promise filesystem transactions or safety against arbitrary bind-mount/remount
  topology; deployment mount topology remains trusted.
- Windows ancestor replacement in the native run was denied by the OS, so do
  not describe it as a successful ancestor-swap test. The real junction swap
  test passed and is the direct positive Windows namespace-race evidence.
- The artifact hashes above are copied from retained manifests; no independent
  rehash of archived/downloaded bytes was performed during documentation.

These are evidence and implementation boundaries, not blockers to the completed
slice or future feature work. No protocol-fd coverage, stream/tag equivalence,
raw filename handling, backward packet-hash agreement, or negative-case behavior
is inferred from provisioning alone; those claims rest on the corresponding
passing managed transport tests in the final CI.
