# Native text similarity

This package provides a narrow C ABI wrapper around the official RapidFuzz C++
header-only library. It accepts Unicode code points, copies them into C-owned
buffers before crossing cgo, and exposes the official token-set ratio as a
checked Go value in `[0, 1]`. C++ exceptions are caught at the ABI boundary.

Vendored upstream headers and license are from the official
[`rapidfuzz/rapidfuzz-cpp`](https://github.com/rapidfuzz/rapidfuzz-cpp) `v3.3.4`
tag, commit `82662f3623b3ca3645e543f677fc32fb8bd1fb95`. The official GitHub
source archive SHA-256 is
`a0dd2ef361cac165e12076696e7c7e8d069a2908abd9599ad4bd190de33f9881`.
Only the `rapidfuzz/` public headers are vendored, unchanged, under
`third_party/rapidfuzz/`; the complete upstream MIT license is
`third_party/LICENSE`.

The native boundary requires a C++17 compiler and the platform's C++ runtime:
`libstdc++` on Linux, `libc++` on macOS, and MinGW `libstdc++` and `libwinpthread`
on Windows using explicit static archive selection; `libgcc` is statically
linked there as well. CI inspects the resulting Windows executable for MinGW
runtime DLL dependencies. These are build prerequisites, not runtime
configuration. cgo is required; no pure-Go fallback is provided.
