# Normalized metadata reader

`Reader.Read(ctx, preparedPath, ReadRequest)` accepts the path prepared by its caller. It
does not reconstruct a path from source identity, retain a descriptor, or start
a background parsing goroutine. TagLib is path-based and does not accept context
cancellation, so the adapter checks context immediately before and after
library calls; cancellation cannot interrupt a call in flight.

## Audio metadata

`github.com/deluan/go-taglib` at commit
`32d7614f30ecf47d451cd801e0cfb93bf9739db4` (Go pseudo-version
`v0.0.0-20260926163408-32d7614f30ec`) provides normalized text tag maps and
optional audio properties using its embedded TagLib v2.3.2. This same reader is
used for MKA/Matroska; it returns a flat normalized map and does not preserve
native Matroska hierarchy, targets, language context, or binary tags. Successful
tag results are copied into an owned, non-nil
`map[string][]string`; values and order are exactly those returned by TagLib.
The adapter does not deduplicate values and makes no promise that all native
tags, frames, atoms, or binary fields are represented. In the exercised
synthetic corpus, repeated FLAC and ID3 values passed through; for MP4, multiple
same-key atoms normalize to the first value. That known loss is accepted by
this normalized-text contract. The reported format is detected by TagLib, not
inferred from the prepared path's extension. It is empty when properties are
not requested. Format detection failures leave optional properties unavailable.
TagLib may prefer a recognized filename extension when selecting a parser;
the adapter does not promise to correct a misleading known extension. Existing
technical stream/container analysis remains the ffprobe result, not this value.

TagLib properties are explicitly requested through `ReadRequest.Properties`; an
unrequested read never calls `ReadProperties`. A requested property-read failure
does not fail tag capture. Properties use the fork's `Bitrate`, `BitsPerSample`,
and `Codec` fields; bitrate is converted with checked multiplication from kbit/s
to bit/s. Unknown bit depth remains null. This capability does not replace
ffprobe.

The fork embeds the TagLib v2.3.2 WebAssembly build. Its LGPL-2.1
licensing and distribution/relinking implications are not assessed as resolved
for product distribution. Source and build provenance are linked in
`backend/THIRD_PARTY_NOTICES.md`; distribution readiness remains a separate
review item.

The wrapper uses wazero and does not itself require cgo. The project separately
requires cgo for its vendored RapidFuzz C++ integration. Full corresponding
source/rebuild coverage, relink materials, and any distribution-policy
assessment remain open; no distribution-readiness conclusion is made here.

For MPEG/ADTS AAC, TagLib uses the MPEG-family reader and returns its normalized
text tags (which may be empty for untagged audio). This is not a lossless raw
tag or frame inventory; AAC metadata classification is therefore described as
normalized MPEG-reader output, not an all-tags guarantee.

The returned map and audio properties are detached data. Go strings may contain
invalid UTF-8 from upstream tag decoding; JSON encoding follows Go's standard
replacement behavior for invalid UTF-8. TagLib's result strings are likewise
returned unchanged in memory.
