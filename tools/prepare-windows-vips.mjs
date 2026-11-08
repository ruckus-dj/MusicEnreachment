import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import {
	appendFileSync,
	readdirSync,
	rmSync,
	readFileSync,
	lstatSync,
	statSync,
} from "node:fs";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const sourceArchives = new Map([
	[
		"vips-8.18.7.tar.xz",
		"5baaead3b0bb20ffdb9e9ff09aa9fda08620923df77b63b436654cb5e0b3bf94",
	],
	[
		"ImageMagick-7.1.2-31.tar.xz",
		"168a9710b3a8d69dbd20eb0a35f145b0c0ed35f076b2fc98d3eb57daa919c376",
	],
	[
		"tiff-4.7.1.tar.xz",
		"b92017489bdc1db3a4c97191aa4b75366673cb746de0dce5d7a749d5954681ba",
	],
]);

const requiredNotices = [
	"LICENSE-libtiff.md",
	"LICENSE-ImageMagick",
	"LICENSE-libvips",
];

const recipe = String.raw`set -euo pipefail

prefix="$(cygpath -u "$RUNNER_TEMP")/native-vips-ucrt64"
work="$(cygpath -u "$RUNNER_TEMP")/native-vips-ucrt64-build"
export PKG_CONFIG_PATH="$prefix/lib/pkgconfig:$(printenv PKG_CONFIG_PATH || true)"
export PATH="$prefix/bin:$PATH"
mkdir -p "$prefix" "$work" "$prefix/share/native-sources"
cp tools/prepare-windows-vips.mjs "$prefix/share/native-sources/"
pacman -Q | sort > "$prefix/share/native-sources/MSYS2-PACKAGE-VERSIONS.txt"
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
cp vips-8.18.7/LICENSE "$prefix/share/native-sources/LICENSE-libvips"

pkgconfig="$prefix/lib/pkgconfig"
windows_prefix="$(cygpath -m "$prefix")"
for pc in "$prefix"/lib/pkgconfig/*.pc; do
  sed -i "s|$prefix|$windows_prefix|g" "$pc"
done
PKG_CONFIG_PATH="$pkgconfig" pkg-config --modversion vips
`;

export function createCacheKey({
	platform,
	architecture,
	prefix,
	packages,
	toolchain,
	recipeText = recipe,
	implementation = readFileSync(fileURLToPath(import.meta.url), "utf8"),
}) {
	const digest = createHash("sha256");
	for (const value of [
		platform,
		architecture,
		prefix,
		packages,
		toolchain,
		recipeText,
		implementation,
	]) {
		digest.update(String(value));
		digest.update("\0");
	}
	return `native-vips-ucrt64-${platform}-${architecture}-${digest.digest("hex")}`;
}

function isFile(file) {
	try {
		return lstatSync(file).isFile();
	} catch {
		return false;
	}
}

function findFile(directory, expectedName) {
	let entries;
	try {
		entries = readdirSync(directory, { withFileTypes: true });
	} catch {
		return false;
	}
	for (const entry of entries) {
		const current = path.join(directory, entry.name);
		if (entry.isFile() && entry.name === expectedName) return true;
		if (entry.isDirectory() && findFile(current, expectedName)) return true;
	}
	return false;
}

function findPackageImportLibrary(prefix, packageFile) {
	const metadata = path.join(prefix, "lib", "pkgconfig", packageFile);
	if (!isFile(metadata)) return null;
	const contents = readFileSync(metadata, "utf8");
	const variables = new Map(
		[...contents.matchAll(/^([A-Za-z_][A-Za-z0-9_]*)=(.*)$/gm)].map(
			(match) => [match[1], match[2]],
		),
	);
	const libs = /^Libs:\s*(.*)$/m.exec(contents)?.[1] ?? "";
	const expandedLibs = libs.replace(
		/\$\{([^}]+)\}/g,
		(reference, name) => variables.get(name) ?? reference,
	);
	const libraryName = /(?:^|\s)-l([^\s]+)/.exec(expandedLibs)?.[1];
	if (!libraryName) return null;
	for (const filename of [`lib${libraryName}.dll.a`, `${libraryName}.dll.a`]) {
		if (isFile(path.join(prefix, "lib", filename))) return filename;
	}
	return null;
}

export function validateNativePrefix(prefix, {
	sourceHashes = sourceArchives,
	nativeSmoke = probeNativeLoad,
} = {}) {
	const failures = [];
	const notices = path.join(prefix, "share", "native-sources");
	for (const [archive, expectedHash] of sourceHashes) {
		const file = path.join(notices, archive);
		if (!isFile(file)) {
			failures.push(`missing pinned source archive ${archive}`);
			continue;
		}
		const actualHash = createHash("sha256").update(readFileSync(file)).digest("hex");
		if (actualHash !== expectedHash) failures.push(`checksum mismatch for ${archive}`);
	}
	for (const notice of requiredNotices) {
		const file = path.join(notices, notice);
		if (!isFile(file)) failures.push(`missing required source notice ${notice}`);
		else if (lstatSync(file).size === 0) failures.push(`empty required source notice ${notice}`);
	}
	for (const file of [
		"share/native-sources/MSYS2-PACKAGE-VERSIONS.txt",
		"share/native-sources/MSYS2-PACKAGE-PROVENANCE.txt",
		"include/vips/vips.h",
		"include/tiffio.h",
		"lib/pkgconfig/vips.pc",
		"lib/pkgconfig/MagickCore.pc",
		"lib/pkgconfig/libtiff-4.pc",
		"bin/libvips-42.dll",
	]) {
		if (!isFile(path.join(prefix, file))) failures.push(`missing native SDK file ${file}`);
	}
	for (const packageFile of ["vips.pc", "MagickCore.pc", "libtiff-4.pc"]) {
		if (isFile(path.join(prefix, "lib", "pkgconfig", packageFile))
			&& !findPackageImportLibrary(prefix, packageFile)) {
			failures.push(`missing MinGW import library for ${packageFile}`);
		}
	}
	if (!findFile(path.join(prefix, "include"), "MagickCore.h")) {
		failures.push("missing ImageMagick MagickCore.h header");
	}
	const vipsPC = path.join(prefix, "lib", "pkgconfig", "vips.pc");
	if (isFile(vipsPC) && !/^Version:\s*8\.18\.7\s*$/m.test(readFileSync(vipsPC, "utf8"))) {
		failures.push("libvips pkg-config metadata is not version 8.18.7");
	}
	if (failures.length === 0) {
		try {
			failures.push(...nativeSmoke());
		} catch (error) {
			failures.push(`native link/load smoke failed: ${error instanceof Error ? error.message : String(error)}`);
		}
	}
	return failures;
}

function probeNativeLoad() {
	runBash(String.raw`set -euo pipefail
prefix="$(cygpath -u "$RUNNER_TEMP")/native-vips-ucrt64"
work="$(cygpath -u "$RUNNER_TEMP")/native-vips-ucrt64-build/native-sdk-smoke"
mkdir -p "$work"
export PKG_CONFIG_PATH="$prefix/lib/pkgconfig:$(printenv PKG_CONFIG_PATH || true)"
export PATH="$prefix/bin:$PATH"
cat > "$work/native-sdk-smoke.c" <<'EOF'
#include <MagickCore/MagickCore.h>
#include <stddef.h>
#include <tiffio.h>
#include <vips/vips.h>

int main(void) {
  if (VIPS_INIT("native-sdk-smoke")) return 1;
  MagickCoreGenesis("native-sdk-smoke", MagickFalse);
  const char *version = TIFFGetVersion();
  MagickCoreTerminus();
  vips_shutdown();
  return version == NULL;
}
EOF
pkgflags="$(pkg-config --cflags --libs vips MagickCore libtiff-4)"
gcc "$work/native-sdk-smoke.c" -o "$work/native-sdk-smoke.exe" $pkgflags
"$work/native-sdk-smoke.exe"
`);
	return [];
}

function runBash(script, { capture = false } = {}) {
	if (!process.env.MSYS2_BASH) throw new Error("The MSYS2 bash build path is required");
	const result = spawnSync(process.env.MSYS2_BASH, ["-lc", script], {
		encoding: capture ? "utf8" : undefined,
		stdio: capture ? ["ignore", "pipe", "inherit"] : "inherit",
		windowsHide: true,
		env: { ...process.env, MSYSTEM: "UCRT64", CHERE_INVOKING: "1" },
	});
	if (result.error) throw result.error;
	if (result.signal) throw new Error(`Windows native library preparation was terminated by ${result.signal}`);
	if (result.status !== 0) throw new Error(`Windows native library preparation failed with exit code ${result.status ?? 1}`);
	return result.stdout ?? "";
}

export function assertValidPrefix(prefix) {
	const failures = validateNativePrefix(prefix);
	if (failures.length) throw new Error(`Windows native SDK prefix is invalid: ${failures.join("; ")}`);
}

function configureEnvironment() {
	runBash(String.raw`set -euo pipefail
prefix="$(cygpath -u "$RUNNER_TEMP")/native-vips-ucrt64"
pkgconfig="$prefix/lib/pkgconfig"
windows_prefix="$(cygpath -m "$prefix")"
windows_pkgconfig="$(cygpath -w "$pkgconfig")"
windows_bin="$(cygpath -w "$prefix/bin")"
version="$(PKG_CONFIG_PATH="$pkgconfig" pkg-config --modversion vips)"
test "$version" = "8.18.7"
packages="$(pacman -Q | sort)"
recorded_packages="$(< "$prefix/share/native-sources/MSYS2-PACKAGE-VERSIONS.txt")"
if [[ "$packages" != "$recorded_packages" ]]; then
  printf '%s\n' 'Windows native SDK package versions do not match the active toolchain' >&2
  exit 1
fi
printf '%s\n' "$windows_bin" >> "$GITHUB_PATH"
printf 'PKG_CONFIG_PATH=%s\n' "$windows_pkgconfig" >> "$GITHUB_ENV"
printf 'VIPSHOME=%s\n' "$windows_prefix" >> "$GITHUB_ENV"
`);
}

function computeCacheKey() {
	const details = runBash(String.raw`set -euo pipefail
prefix="$(cygpath -u "$RUNNER_TEMP")/native-vips-ucrt64"
packages="$(pacman -Q | sort)"
toolchain="$(
  printf '%s\n' '--- gcc ---'
  gcc -dumpmachine
  gcc -dumpfullversion -dumpversion
  gcc -v 2>&1
  printf '%s\n' '--- g++ ---'
  g++ -dumpmachine
  g++ -dumpfullversion -dumpversion
  g++ -v 2>&1
)"
printf '%s\0%s\0%s' "$prefix" "$packages" "$toolchain"
`, { capture: true });
	const [prefix, packages, toolchain] = details.split("\0");
	if (!prefix || !packages || !toolchain) throw new Error("Could not determine the Windows native SDK cache identity");
	const key = createCacheKey({
		platform: process.env.RUNNER_OS ?? "Windows",
		architecture: process.env.RUNNER_ARCH ?? "X64",
		prefix,
		packages,
		toolchain,
	});
	if (!process.env.GITHUB_OUTPUT) throw new Error("GITHUB_OUTPUT is required to publish the Windows native SDK cache key");
	appendFileSync(process.env.GITHUB_OUTPUT, `key=${key}\n`);
}

function checkedRunnerTemp(runnerTemp) {
	if (typeof runnerTemp !== "string" || !runnerTemp.trim() || !path.isAbsolute(runnerTemp)) {
		throw new Error("RUNNER_TEMP must be a nonempty absolute directory before preparing the Windows native SDK");
	}
	const absolute = path.resolve(runnerTemp);
	if (absolute === path.parse(absolute).root) {
		throw new Error("RUNNER_TEMP cannot be a filesystem root before preparing the Windows native SDK");
	}
	let details;
	try {
		details = statSync(absolute);
	} catch {
		throw new Error("RUNNER_TEMP must name an existing directory before preparing the Windows native SDK");
	}
	if (!details.isDirectory()) {
		throw new Error("RUNNER_TEMP must name an existing directory before preparing the Windows native SDK");
	}
	return absolute;
}

function validationFailures(prefix, validate) {
	try {
		const failures = validate(prefix);
		if (!Array.isArray(failures)) return ["validator did not return a failure list"];
		return failures;
	} catch (error) {
		return [`validator raised: ${error instanceof Error ? error.message : String(error)}`];
	}
}

export function prepareNativeSdk({
	runnerTemp,
	cacheHit = false,
	validate = validateNativePrefix,
	build = () => runBash(recipe),
	configure = configureEnvironment,
	remove = (directory) => rmSync(directory, { recursive: true, force: true }),
	warn = (message) => console.warn(message),
}) {
	const temp = checkedRunnerTemp(runnerTemp);
	const prefix = path.join(temp, "native-vips-ucrt64");
	const workDirectory = path.join(temp, "native-vips-ucrt64-build");
	let cacheFailures = cacheHit ? validationFailures(prefix, validate) : [];

	if (cacheHit && cacheFailures.length === 0) {
		try {
			configure(prefix);
			return { cacheHit: true, rebuilt: false };
		} catch (error) {
			cacheFailures = [`cache configuration failed: ${error instanceof Error ? error.message : String(error)}`];
		}
	}

	if (cacheHit) {
		warn(`::warning::Invalid Windows native SDK cache (${cacheFailures.join("; ")}); cleaning its prefix and build directory, then rebuilding from pinned sources.`);
	}
	remove(prefix);
	remove(workDirectory);
	build({ prefix, workDirectory });
	const buildFailures = validationFailures(prefix, validate);
	if (buildFailures.length) {
		throw new Error(`Windows native SDK build failed validation: ${buildFailures.join("; ")}`);
	}
	configure(prefix);
	return { cacheHit: false, rebuilt: cacheHit };
}

export function main(args = process.argv.slice(2)) {
	if (args.length === 1 && args[0] === "--cache-key") return computeCacheKey();
	if (args.length && !(args.length === 1 && args[0] === "--prepare")) {
		throw new Error(`Unknown Windows native library helper arguments: ${args.join(" ")}`);
	}
	return prepareNativeSdk({
		runnerTemp: process.env.RUNNER_TEMP,
		cacheHit: process.env.NATIVE_VIPS_CACHE_HIT === "true",
	});
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
	try {
		main();
	} catch (error) {
		console.error(error);
		process.exitCode = 1;
	}
}
