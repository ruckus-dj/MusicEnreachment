import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { gzipSync } from "node:zlib";
import { test } from "node:test";
import {
	applyPinnedPatch,
	diagnosticsSource,
	validateTarArchive,
} from "./prepare-windows-postgres-diagnostics.mjs";

const archiveRoot =
	"embedded-postgres-a7f8d1f8bb15a6ebf01f1bc2db8524e26cf89092";

function tarEntry(name, type = "0", body = "") {
	const header = Buffer.alloc(512);
	const putText = (value, start, length) => {
		Buffer.from(value).copy(header, start, 0, length);
	};
	const putOctal = (value, start, length) => {
		putText(`${value.toString(8).padStart(length - 1, "0")}\0`, start, length);
	};
	putText(name, 0, 100);
	putOctal(0o644, 100, 8);
	putOctal(0, 108, 8);
	putOctal(0, 116, 8);
	putOctal(Buffer.byteLength(body), 124, 12);
	putOctal(0, 136, 12);
	header.fill(0x20, 148, 156);
	header[156] = type.charCodeAt(0);
	putText("ustar\0", 257, 6);
	putText("00", 263, 2);
	const checksum = header.reduce((sum, byte) => sum + byte, 0);
	putText(`${checksum.toString(8).padStart(6, "0")}\0 `, 148, 8);
	const payload = Buffer.from(body);
	const padding = Buffer.alloc((512 - (payload.length % 512)) % 512);
	return Buffer.concat([header, payload, padding]);
}

function gzipTar(...entries) {
	return gzipSync(Buffer.concat([...entries, Buffer.alloc(1024)]));
}

test("archive validation permits regular files beneath the pinned source root", () => {
	const archive = gzipTar(
		tarEntry(`${archiveRoot}/LICENSE`, "0", "license"),
		tarEntry(`${archiveRoot}/internal`, "5"),
	);
	assert.deepEqual(validateTarArchive(archive), [
		{ path: `${archiveRoot}/LICENSE`, type: "0" },
		{ path: `${archiveRoot}/internal`, type: "5" },
	]);
});

test("archive validation rejects traversal, foreign roots, and links", () => {
	for (const [name, type] of [
		[`${archiveRoot}/../outside`, "0"],
		["elsewhere/file", "0"],
		[`${archiveRoot}/link`, "2"],
	]) {
		assert.throws(
			() => validateTarArchive(gzipTar(tarEntry(name, type))),
			/unsafe archive path|unsupported tar entry type/,
		);
	}
});

test("pinned patch application verifies the complete preimage and every anchor", () => {
	const original = "const value = 1;\n";
	const patch = {
		sha256: createHash("sha256").update(original).digest("hex"),
		edits: [["const value = 1;", "const value = 2;"]],
	};
	assert.equal(
		applyPinnedPatch(
			"fixture.go",
			Buffer.from(original),
			patch,
		).contents.toString(),
		"const value = 2;\n",
	);
	assert.throws(
		() => applyPinnedPatch("fixture.go", Buffer.from("unexpected"), patch),
		/preimage hash mismatch/,
	);
	assert.throws(
		() =>
			applyPinnedPatch("fixture.go", Buffer.from(original), {
				...patch,
				edits: [["missing", "change"]],
			}),
		/anchor mismatch/,
	);
	assert.throws(
		() =>
			applyPinnedPatch("fixture.go", Buffer.from(original + original), {
				...patch,
				sha256: createHash("sha256")
					.update(original + original)
					.digest("hex"),
			}),
		/anchor mismatch/,
	);
});

test("generated diagnostics source keeps the record format as escaped Go text", () => {
	const lines = diagnosticsSource
		.split("\n")
		.filter((line) => line.includes("time_unix_nano"));
	assert.equal(lines.length, 1);
	const literal = lines[0].trim().replace(/,$/, "");
	// Go and JSON share these escapes, so the generated literal must parse as
	// JSON; a plain template literal would leave unescaped quotes and newlines.
	const format = JSON.parse(literal);
	assert.ok(format.endsWith("\n"));
	assert.deepEqual(Object.keys(JSON.parse(format.replace(/%[dq]/g, "0"))), [
		"time_unix_nano",
		"event",
		"result",
	]);
});
