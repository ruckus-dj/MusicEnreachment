import assert from "node:assert/strict";
import { mkdtemp, mkdir, readFile, readdir, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import { stageNativeLibraries } from "./stage-native-libraries.mjs";

async function fixture(t) {
  const root = await mkdtemp(path.join(os.tmpdir(), "stage-native-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const prefix = path.join(root, "prefix");
  const bin = path.join(prefix, "bin");
  const toolchain = path.join(root, "toolchain", "bin");
  const destination = path.join(root, "out");
  await Promise.all([mkdir(bin, { recursive: true }), mkdir(toolchain, { recursive: true }), mkdir(path.join(prefix, "share/native-sources"), { recursive: true }), mkdir(path.join(root, "toolchain/share/licenses"), { recursive: true })]);
  const dll = async (directory, name) => { const file = path.join(directory, name); await writeFile(file, name); return file; };
  await writeFile(path.join(prefix, "share/native-sources/NOTICE"), "source notice");
  await writeFile(path.join(root, "toolchain/share/licenses/LICENSE"), "toolchain license");
  return { bin, toolchain, destination, dll };
}

test("stages only reachable DLL closure once, case-insensitively, plus notices/licenses", async (t) => {
  const f = await fixture(t);
  const root = await f.dll(f.bin, "libvips-42.dll");
  const image = await f.dll(f.bin, "Image.dll");
  await f.dll(f.bin, "unused-gpl-plugin.dll");
  const codec = await f.dll(f.toolchain, "Codec.dll");
  const deps = new Map([[root, "DLL Name: image.DLL\nDLL Name: KERNEL32.dll\n"], [image, "DLL Name: CODEC.dll\nDLL Name: codec.DLL\n"], [codec, "DLL Name: api-ms-win-core-file-l1-1-0.dll\n"]]);
  await stageNativeLibraries(f.bin.replace(/\/bin$/, ""), f.destination, f.toolchain, { inspectImports: (file) => deps.get(file) });
  const names = (await readdir(f.destination)).sort();
  assert.deepEqual(names, ["Codec.dll", "Image.dll", "libvips-42.dll", "native-sources"]);
  assert.equal(await readFile(path.join(f.destination, "native-sources/NOTICE"), "utf8"), "source notice");
  assert.equal(await readFile(path.join(f.destination, "native-sources/toolchain-licenses/LICENSE"), "utf8"), "toolchain license");
});

test("rejects unresolved non-system imports", async (t) => {
  const f = await fixture(t);
  const root = await f.dll(f.bin, "libvips-42.dll");
  await assert.rejects(stageNativeLibraries(f.bin.replace(/\/bin$/, ""), f.destination, f.toolchain, { inspectImports: () => "DLL Name: missing-runtime.dll\n" }), /Unresolved native import missing-runtime\.dll/);
  assert.equal(await readFile(path.join(f.destination, "libvips-42.dll"), "utf8"), "libvips-42.dll");
  assert.ok(root);
});
