import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { EventEmitter } from "node:events";
import { mkdtemp, mkdir, readFile, readdir, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { PassThrough } from "node:stream";
import { test } from "node:test";
import { inspectNativeImports, stageNativeLibraries } from "./stage-native-libraries.mjs";

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
  await stageNativeLibraries(path.dirname(f.bin), f.destination, f.toolchain, { inspectImports: (file) => deps.get(file) });
  const names = (await readdir(f.destination)).sort();
  assert.deepEqual(names, ["Codec.dll", "Image.dll", "libvips-42.dll", "native-sources"]);
  assert.equal(await readFile(path.join(f.destination, "native-sources/NOTICE"), "utf8"), "source notice");
  assert.equal(await readFile(path.join(f.destination, "native-sources/toolchain-licenses/LICENSE"), "utf8"), "toolchain license");
});

test("streams objdump output and retains only import names", async () => {
  const child = new EventEmitter();
  child.stdout = new PassThrough();
  child.stderr = new PassThrough();
  const source = "C:\\build\\libvips-42.dll";
  let invocation;
  const result = inspectNativeImports(source, {
    spawnProcess: (command, args, options) => {
      invocation = { command, args, options };
      setImmediate(() => {
        child.stdout.write("PE header noise\n");
        child.stdout.write("DLL Name: CODEC.");
        child.stdout.write("dll\r\nmore noise\n\tDLL Name: KERNEL32.dll\n");
        child.stdout.end();
        child.stderr.end();
        child.emit("close", 0, null);
      });
      return child;
    },
  });
  assert.deepEqual(await result, ["CODEC.dll", "KERNEL32.dll"]);
  assert.deepEqual(invocation, { command: "objdump", args: ["-p", source], options: { stdio: ["ignore", "pipe", "pipe"] } });
});

test("rejects with bounded objdump stderr and no hang on failure", async () => {
  const child = new EventEmitter();
  child.stdout = new PassThrough();
  child.stderr = new PassThrough();
  const result = inspectNativeImports("libvips-42.dll", {
    spawnProcess: () => {
      setImmediate(() => {
        child.stderr.write("fatal: not a PE file\n");
        child.stdout.end();
        child.stderr.end();
        child.emit("close", 1, null);
      });
      return child;
    },
  });
  await assert.rejects(result, /objdump failed to inspect libvips-42\.dll: fatal: not a PE file/);
});

test("rejects spawn failures without waiting for a close event", async () => {
  const child = new EventEmitter();
  child.stdout = new PassThrough();
  child.stderr = new PassThrough();
  const result = inspectNativeImports("libvips-42.dll", {
    spawnProcess: () => {
      setImmediate(() => child.emit("error", new Error("spawn objdump ENOENT")));
      return child;
    },
  });
  await assert.rejects(result, /spawn objdump ENOENT/);
});

test("handles objdump-compatible output larger than the default exec buffer", async (t) => {
  const root = await mkdtemp(path.join(os.tmpdir(), "inspect-native-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const script = path.join(root, "fake-objdump.mjs");
  await writeFile(script, `if (process.argv[2] !== "-p" || !process.argv[3]) process.exit(9);
const noise = "irrelevant PE dump line ".repeat(64) + "\\n";
let remaining = 2 * 1024 * 1024;
while (remaining > 0) {
  process.stdout.write(noise);
  remaining -= noise.length;
}
process.stdout.write("\\tDLL Name: Image.dll\\r\\n");
process.stdout.write("DLL Name: KERNEL32.dll\\n");
`);
  const source = path.join(root, "libvips-42.dll");
  const invocations = [];
  const imports = await inspectNativeImports(source, {
    spawnProcess: (command, args, options) => {
      invocations.push({ command, args });
      return spawn(process.execPath, [script, ...args], options);
    },
  });
  assert.deepEqual(invocations, [{ command: "objdump", args: ["-p", source] }]);
  assert.deepEqual(imports, ["Image.dll", "KERNEL32.dll"]);
  const f = await fixture(t);
  await f.dll(f.bin, "libvips-42.dll");
  await f.dll(f.bin, "Image.dll");
  await stageNativeLibraries(path.dirname(f.bin), f.destination, f.toolchain, {
    inspectImports: (file) => inspectNativeImports(file, {
      spawnProcess: (_command, args, options) => spawn(process.execPath, [script, ...args], options),
    }),
  });
  assert.deepEqual((await readdir(f.destination)).sort(), ["Image.dll", "libvips-42.dll", "native-sources"]);
});

test("rejects unresolved non-system imports", async (t) => {
  const f = await fixture(t);
  const root = await f.dll(f.bin, "libvips-42.dll");
  await assert.rejects(stageNativeLibraries(path.dirname(f.bin), f.destination, f.toolchain, { inspectImports: () => "DLL Name: missing-runtime.dll\n" }), /Unresolved native import missing-runtime\.dll/);
  assert.equal(await readFile(path.join(f.destination, "libvips-42.dll"), "utf8"), "libvips-42.dll");
  assert.ok(root);
});
