import { spawnSync } from "node:child_process";

const recipe = String.raw`set -euo pipefail

prefix="$(cygpath -u "$RUNNER_TEMP")/native-vips"
work="$(cygpath -u "$RUNNER_TEMP")/native-vips-build"
export PKG_CONFIG_PATH="$prefix/lib/pkgconfig:$(printenv PKG_CONFIG_PATH || true)"
export PATH="$prefix/bin:$PATH"
mkdir -p "$prefix" "$work" "$prefix/share/native-sources"
cp tools/prepare-windows-vips.mjs "$prefix/share/native-sources/"
pacman -Q > "$prefix/share/native-sources/MSYS2-PACKAGE-VERSIONS.txt"
pacman -Qi > "$prefix/share/native-sources/MSYS2-PACKAGE-PROVENANCE.txt"
cd "$work"

fetch_source() {
  local url="$1" sha="$2" archive="$3"
  curl --fail --location --retry 3 --output "$archive" "$url"
  echo "$sha  $archive" | sha256sum --check --status
  tar -xf "$archive"
  cp "$archive" "$prefix/share/native-sources/"
}

fetch_source \
  https://github.com/libvips/libvips/releases/download/v8.18.7/vips-8.18.7.tar.xz \
  5baaead3b0bb20ffdb9e9ff09aa9fda08620923df77b63b436654cb5e0b3bf94 \
  vips-8.18.7.tar.xz
fetch_source \
  https://github.com/ImageMagick/ImageMagick/releases/download/7.1.2-31/ImageMagick-7.1.2-31.tar.xz \
  168a9710b3a8d69dbd20eb0a35f145b0c0ed35f076b2fc98d3eb57daa919c376 \
  ImageMagick-7.1.2-31.tar.xz
fetch_source \
  https://download.osgeo.org/libtiff/tiff-4.7.1.tar.xz \
  b92017489bdc1db3a4c97191aa4b75366673cb746de0dce5d7a749d5954681ba \
  tiff-4.7.1.tar.xz

cmake -S tiff-4.7.1 -B tiff-build -G Ninja \
  -DCMAKE_INSTALL_PREFIX="$prefix" -DCMAKE_BUILD_TYPE=Release \
  -DBUILD_SHARED_LIBS=ON -Dtiff-tools=OFF -Dtiff-tests=OFF \
  -Dtiff-contrib=OFF -Dtiff-docs=OFF -Djbig=OFF -Dlzma=OFF \
  -Dzstd=OFF -Djpeg=ON -Dwebp=OFF -Dlibdeflate=OFF
cmake --build tiff-build --parallel
cmake --install tiff-build
cp tiff-4.7.1/LICENSE.md "$prefix/share/native-sources/LICENSE-libtiff.md"

cd ImageMagick-7.1.2-31
./configure --prefix="$prefix" --enable-shared --disable-static \
  --disable-openmp --disable-hdri --with-quantum-depth=16 --without-modules \
  --without-magick-plus-plus --without-perl --without-utilities --without-x \
  --disable-docs \
  --without-autotrace --without-bzlib --without-zip --without-zlib --without-zstd \
  --without-dps --without-fftw --without-flif --without-fpx --without-djvu \
  --without-fontconfig --without-freetype --without-raqm --without-gdi32 \
  --without-gslib --without-gvc --without-dmr --without-heic --without-jbig \
  --without-jpeg --without-jxl --without-lcms --without-openjp2 --without-lqr \
  --without-lzma --without-openexr --without-pango --without-png --without-raw \
  --without-rsvg --without-tiff --without-uhdr --without-webp --without-wmf \
  --without-xml --without-jemalloc --without-mtmalloc --without-tcmalloc \
  --without-umem
make -j"$(nproc)"
make install
cp LICENSE "$prefix/share/native-sources/LICENSE-ImageMagick"

cd "$work"
meson setup vips-build vips-8.18.7 --prefix="$prefix" \
  --auto-features=disabled --default-library=shared \
  -Dmodules=disabled -Ddeprecated=false -Dexamples=false -Dcplusplus=false \
  -Dintrospection=disabled -Dvapi=false -Djpeg=enabled -Dpng=enabled \
  -Dtiff=enabled -Dwebp=enabled -Dzlib=enabled -Dlcms=enabled -Dexif=enabled \
  -Dnsgif=true -Dmagick=enabled -Dmagick-package=MagickCore \
  -Dmagick-features=load -Dfftw=disabled -Dimagequant=disabled \
  -Dpoppler=disabled -Dppm=false -Danalyze=false -Dradiance=false
meson compile -C vips-build
meson install -C vips-build
cp vips-8.18.7/COPYING "$prefix/share/native-sources/COPYING-libvips"

pkgconfig="$prefix/lib/pkgconfig"
windows_prefix="$(cygpath -m "$prefix")"
windows_pkgconfig="$(cygpath -w "$pkgconfig")"
windows_bin="$(cygpath -w "$prefix/bin")"
for pc in "$prefix"/lib/pkgconfig/*.pc; do
  sed -i "s|$prefix|$windows_prefix|g" "$pc"
done
printf '%s\n' "$windows_bin" >> "$GITHUB_PATH"
printf 'PKG_CONFIG_PATH=%s\n' "$windows_pkgconfig" >> "$GITHUB_ENV"
printf 'VIPSHOME=%s\n' "$windows_prefix" >> "$GITHUB_ENV"
PKG_CONFIG_PATH="$pkgconfig" pkg-config --modversion vips
`;

if (!process.env.MSYS2_BASH) throw new Error("The MSYS2 bash build path is required");
const result = spawnSync(process.env.MSYS2_BASH, ["-lc", recipe], {
	stdio: "inherit",
	windowsHide: true,
	env: { ...process.env, MSYSTEM: "MINGW64", CHERE_INVOKING: "1" },
});

if (result.error) {
	throw result.error;
}
if (result.signal) {
	throw new Error(`Windows native library build was terminated by ${result.signal}`);
}
if (result.status !== 0) {
	process.exit(result.status ?? 1);
}
