# Test fixtures

`eg.flac`, `eg.mp3`, `eg.m4a`, and `eg.mka` are copied byte-for-byte from the
`testdata/` directory of
[`github.com/deluan/go-taglib`](https://github.com/deluan/go-taglib/tree/32d7614f30ecf47d451cd801e0cfb93bf9739db4/testdata)
at commit `32d7614f30ecf47d451cd801e0cfb93bf9739db4`. The package's
`LICENSE` is LGPL-2.1; see `backend/THIRD_PARTY_NOTICES.md` for the license text.

The tests use this file as a Matroska container, then write synthetic tags
before checking TagLib's normalized tag results. The fixtures are not product
assets.
