# Read-only metadata capability

`Reader.Read(ctx, preparedPath, ReadRequest)` accepts the path prepared by its caller. It
does not reconstruct a path from source identity, retain a descriptor, or start
a background parsing goroutine. Both upstream libraries are path-based and do
not accept context cancellation, so the adapter checks context immediately
before and after library calls; cancellation cannot interrupt a call in flight.

## Audio metadata

`go.senan.xyz/taglib` v0.14.0 provides normalized text tag maps and optional
audio properties. Successful tag results are copied into an owned, non-nil
`map[string][]string`; values and order are exactly those returned by TagLib.
The adapter does not deduplicate values and makes no promise that all native
tags, frames, atoms, or binary fields are represented. In the exercised
synthetic corpus, repeated FLAC and ID3 values passed through; for MP4, multiple
same-key atoms normalize to the first value. That known loss is accepted by
this normalized-text contract. Extensionless prepared paths are supported by
TagLib's content detection.

TagLib properties are explicitly requested through `ReadRequest.Properties`; an
unrequested read never calls `ReadProperties`. A requested property-read failure
does not fail tag capture. Properties are returned directly in application DTO units, including
bitrate converted with checked multiplication from kbit/s to bit/s. Unknown
bit depth remains null. This capability does not replace ffprobe.

TagLib v0.14.0 embeds the TagLib v2.1.1 WebAssembly build. Its LGPL-2.1
licensing and distribution/relinking implications are not assessed as resolved
for product distribution. Source and build provenance are linked in
`backend/THIRD_PARTY_NOTICES.md`; distribution readiness remains a separate
review item.

## Matroska / MKA

MKA uses the ready-made `github.com/remko/go-mkvparse` v0.14.0 MIT-licensed
event parser. The adapter captures track UID/type and ordered native Tags,
target scopes, SimpleTag nesting, language fields, text and base64-encoded
binary values. It skips Cluster contents while traversing to Tracks and Tags
rather than buffering audio payloads. The normalized projection includes global
tags followed by tags targeting the sole audio track (TrackType 2), preserving
repeated values in both scopes without deduplication. Video-track, unknown-UID,
and mixed-entity-target tags remain native-only. If multiple audio tracks make
the target ambiguous, only global values are projected and a diagnostic is
returned; no track-scoped values are mixed into the normalized map.

For MPEG/ADTS AAC, TagLib uses the MPEG-family reader and returns its normalized
text tags (which may be empty for untagged audio). This is not a lossless raw
tag or frame inventory; AAC metadata classification is therefore described as
normalized MPEG-reader output, not an all-tags guarantee.

The returned map and Matroska capture are detached data. Go strings may contain
invalid UTF-8 from upstream tag decoding; JSON encoding follows Go's standard
replacement behavior for invalid UTF-8. TagLib's result strings are likewise
returned unchanged in memory.
