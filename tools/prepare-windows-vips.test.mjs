import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { lstatSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import {
	assertValidPrefix,
	createCacheKey,
	prepareNativeSdk,
	validateNativePrefix,
} from "./prepare-windows-vips.mjs";

const baseIdentity = {
	platform: "Windows",
	architecture: "X64",
	prefix: "C:/runner/_temp/native-vips-ucrt64",
	packages: "mingw-w64-ucrt-x86_64-gcc 14.2.0-1",
	toolchain: "gcc (Rev10, Built by MSYS2 project) 14.2.0",
	recipeText: "native library recipe v1",
	implementation: "helper v1",
};

test("native SDK cache key changes with each build identity input", () => {
	const original = createCacheKey(baseIdentity);
	for (const [name, update] of [
		["platform", { platform: "OtherOS" }],
		["architecture", { architecture: "ARM64" }],
		["prefix", { prefix: "D:/runner/_temp/native-vips-ucrt64" }],
		["installed MSYS2 package versions", { packages: "mingw-w64-ucrt-x86_64-gcc 14.2.0-2" }],
		["MSYS2 toolchain family", { packages: "mingw-w64-x86_64-gcc 14.2.0-1" }],
		["compiler identity", { toolchain: "gcc 14.2.1" }],
		["recipe", { recipeText: "native library recipe v2" }],
		["helper implementation", { implementation: "helper v2" }],
	]) {
		assert.notEqual(createCacheKey({ ...baseIdentity, ...update }), original, name);
	}
});

function createValidPrefix(root = mkdtempSync(path.join(os.tmpdir(), "native-vips-cache-test-"))) {
	mkdirSync(root, { recursive: true });
	const put = (relative, content = "present") => {
		const file = path.join(root, relative);
		mkdirSync(path.dirname(file), { recursive: true });
		writeFileSync(file, content);
	};
	const archives = new Map([
		["vips-8.18.7.tar.xz", "vips source"],
		["ImageMagick-7.1.2-31.tar.xz", "ImageMagick source"],
		["tiff-4.7.1.tar.xz", "TIFF source"],
	]);
	const sourceHashes = new Map();
	for (const [filename, content] of archives) {
		put(`share/native-sources/${filename}`, content);
		sourceHashes.set(filename, createHash("sha256").update(content).digest("hex"));
	}
	for (const notice of ["LICENSE-libtiff.md", "LICENSE-ImageMagick", "LICENSE-libvips"]) {
		put(`share/native-sources/${notice}`);
	}
	put("share/native-sources/MSYS2-PACKAGE-VERSIONS.txt");
	put("share/native-sources/MSYS2-PACKAGE-PROVENANCE.txt");
	put("include/vips/vips.h");
	put("include/tiffio.h");
	put("include/ImageMagick-7/MagickCore/MagickCore.h");
	put("lib/pkgconfig/vips.pc", "Name: vips\nVersion: 8.18.7\nLibs: -L${libdir} -lvips-42\n");
	put(
		"lib/pkgconfig/MagickCore.pc",
		"libname=MagickCore-fixture\nName: MagickCore\nLibs: -L${libdir} -l${libname}\n",
	);
	put("lib/pkgconfig/libtiff-4.pc", "Name: libtiff\nLibs: -L${libdir} -ltiff-fixture\n");
	put("lib/libvips-42.dll.a");
	put("lib/libMagickCore-fixture.dll.a");
	put("lib/libtiff-fixture.dll.a");
	put("bin/libvips-42.dll");
	return { root, put, sourceHashes };
}

test("native SDK cache validation accepts pinned archives and the complete SDK", (t) => {
	const fixture = createValidPrefix();
	t.after(() => rmSync(fixture.root, { recursive: true, force: true }));
	assert.deepEqual(validateNativePrefix(fixture.root, {
		sourceHashes: fixture.sourceHashes,
		nativeSmoke: () => [],
	}), []);
});

test("native SDK cache validation rejects missing package import libraries", (t) => {
	const fixture = createValidPrefix();
	t.after(() => rmSync(fixture.root, { recursive: true, force: true }));
	rmSync(path.join(fixture.root, "lib/libMagickCore-fixture.dll.a"));
	const failures = validateNativePrefix(fixture.root, {
		sourceHashes: fixture.sourceHashes,
		nativeSmoke: () => [],
	});
	assert.ok(failures.includes("missing MinGW import library for MagickCore.pc"));
});

test("native SDK cache validation rejects archive corruption, missing notices and SDK files", (t) => {
	const fixture = createValidPrefix();
	t.after(() => rmSync(fixture.root, { recursive: true, force: true }));
	fixture.put("share/native-sources/vips-8.18.7.tar.xz", "corrupt source");
	rmSync(path.join(fixture.root, "share/native-sources/LICENSE-libvips"));
	rmSync(path.join(fixture.root, "lib/pkgconfig/MagickCore.pc"));

	const failures = validateNativePrefix(fixture.root, { sourceHashes: fixture.sourceHashes });
	assert.ok(failures.includes("checksum mismatch for vips-8.18.7.tar.xz"));
	assert.ok(failures.includes("missing required source notice LICENSE-libvips"));
	assert.ok(failures.includes("missing native SDK file lib/pkgconfig/MagickCore.pc"));
	assert.throws(() => assertValidPrefix(fixture.root), /Windows native SDK prefix is invalid/);
});

test("native SDK cache validation rejects a wrong libvips pkg-config version", (t) => {
	const fixture = createValidPrefix();
	t.after(() => rmSync(fixture.root, { recursive: true, force: true }));
	fixture.put("lib/pkgconfig/vips.pc", "Name: vips\nVersion: 8.18.6\n");
	assert.ok(validateNativePrefix(fixture.root, { sourceHashes: fixture.sourceHashes })
		.includes("libvips pkg-config metadata is not version 8.18.7"));
});

test("native SDK cache validation treats missing directories and directory-shaped files as invalid", (t) => {
	const root = mkdtempSync(path.join(os.tmpdir(), "native-vips-missing-cache-"));
	t.after(() => rmSync(root, { recursive: true, force: true }));
	assert.doesNotThrow(() => validateNativePrefix(path.join(root, "missing")));
	mkdirSync(path.join(root, "share/native-sources/vips-8.18.7.tar.xz"), { recursive: true });
	const failures = validateNativePrefix(root);
	assert.ok(failures.includes("missing pinned source archive vips-8.18.7.tar.xz"));
});

test("valid native SDK cache hit configures the prefix without rebuilding", (t) => {
	const runnerTemp = mkdtempSync(path.join(os.tmpdir(), "native-vips-cache-hit-"));
	t.after(() => rmSync(runnerTemp, { recursive: true, force: true }));
	let configured = 0;
	const result = prepareNativeSdk({
		runnerTemp,
		cacheHit: true,
		validate: () => [],
		build: () => assert.fail("valid cache hit must not rebuild"),
		configure: () => configured++,
		remove: () => assert.fail("valid cache hit must not clean the prefix"),
		warn: () => assert.fail("valid cache hit must not warn"),
	});
	assert.deepEqual(result, { cacheHit: true, rebuilt: false });
	assert.equal(configured, 1);
});

test("invalid native SDK cache is warned about, cleaned, rebuilt, and revalidated", (t) => {
	const runnerTemp = mkdtempSync(path.join(os.tmpdir(), "native-vips-cache-rebuild-"));
	t.after(() => rmSync(runnerTemp, { recursive: true, force: true }));
	const removed = [];
	const warnings = [];
	let validations = 0;
	let builds = 0;
	let configured = 0;
	const result = prepareNativeSdk({
		runnerTemp,
		cacheHit: true,
		validate: () => ++validations === 1 ? ["corrupt cached binary"] : [],
		build: ({ prefix, workDirectory }) => {
			builds++;
			assert.equal(prefix, path.join(runnerTemp, "native-vips-ucrt64"));
			assert.equal(workDirectory, path.join(runnerTemp, "native-vips-ucrt64-build"));
		},
		configure: () => configured++,
		remove: (directory) => removed.push(directory),
		warn: (message) => warnings.push(message),
	});

	assert.deepEqual(result, { cacheHit: false, rebuilt: true });
	assert.equal(validations, 2);
	assert.equal(builds, 1);
	assert.equal(configured, 1);
	assert.deepEqual(removed, [
		path.join(runnerTemp, "native-vips-ucrt64"),
		path.join(runnerTemp, "native-vips-ucrt64-build"),
	]);
	assert.match(warnings[0], /Invalid Windows native SDK cache/);
	assert.match(warnings[0], /rebuilding from pinned sources/);
});

test("missing imported library or dependent DLL rejects the cache and rebuilds it", (t) => {
	for (const missingProduct of ["lib/libvips-42.dll.a", "bin/native-dependent.dll"]) {
		const runnerTemp = mkdtempSync(path.join(os.tmpdir(), "native-vips-missing-library-"));
		t.after(() => rmSync(runnerTemp, { recursive: true, force: true }));
		const prefix = path.join(runnerTemp, "native-vips-ucrt64");
		const fixture = createValidPrefix(prefix);
		const dependentDLL = path.join(prefix, "bin/native-dependent.dll");
		writeFileSync(dependentDLL, "fixture dependent DLL");
		rmSync(path.join(prefix, missingProduct));
		const validate = (nativePrefix) => validateNativePrefix(nativePrefix, {
			sourceHashes: fixture.sourceHashes,
			nativeSmoke: () => {
				try {
					return lstatSync(dependentDLL).isFile() ? [] : ["native smoke could not load a dependent DLL"];
				} catch {
					return ["native smoke could not load a dependent DLL"];
				}
			},
		});
		let builds = 0;
		const result = prepareNativeSdk({
			runnerTemp,
			cacheHit: true,
			validate,
			build: () => {
				builds++;
				createValidPrefix(prefix);
				writeFileSync(dependentDLL, "rebuilt dependent DLL");
			},
			configure: () => {},
			warn: () => {},
		});

		assert.deepEqual(result, { cacheHit: false, rebuilt: true }, missingProduct);
		assert.equal(builds, 1, missingProduct);
		assert.deepEqual(validate(prefix), [], missingProduct);
	}
});

test("cold native SDK build failure propagates and does not configure", (t) => {
	const runnerTemp = mkdtempSync(path.join(os.tmpdir(), "native-vips-cold-failure-"));
	t.after(() => rmSync(runnerTemp, { recursive: true, force: true }));
	let configured = false;
	const removed = [];
	assert.throws(() => prepareNativeSdk({
		runnerTemp,
		cacheHit: false,
		validate: () => [],
		build: () => {
			throw new Error("compile failed");
		},
		configure: () => configured = true,
		remove: (directory) => removed.push(directory),
	}), /compile failed/);
	assert.equal(configured, false);
	assert.deepEqual(removed, [
		path.join(runnerTemp, "native-vips-ucrt64"),
		path.join(runnerTemp, "native-vips-ucrt64-build"),
	]);
});

test("missing RUNNER_TEMP fails before any helper-owned path can be removed", () => {
	let removed = false;
	for (const [runnerTemp, expectedError] of [
		["", /RUNNER_TEMP must be a nonempty absolute directory/],
		[path.parse(path.resolve(path.sep)).root, /RUNNER_TEMP cannot be a filesystem root/],
	]) {
		assert.throws(() => prepareNativeSdk({
			runnerTemp,
			cacheHit: false,
			remove: () => removed = true,
		}), expectedError);
	}
	assert.equal(removed, false);
});
