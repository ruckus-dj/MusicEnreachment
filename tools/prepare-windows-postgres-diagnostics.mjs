import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { gunzipSync } from "node:zlib";
import {
	appendFileSync,
	mkdirSync,
	mkdtempSync,
	readFileSync,
	writeFileSync,
} from "node:fs";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const source = {
	commit: "a7f8d1f8bb15a6ebf01f1bc2db8524e26cf89092",
	url: "https://codeload.github.com/fergusstrange/embedded-postgres/tar.gz/a7f8d1f8bb15a6ebf01f1bc2db8524e26cf89092",
	archiveSha256:
		"9258814a82f9692754d4f8a0bc5559dd7138fb89d43008aa0862a723e55ca217",
	root: "embedded-postgres-a7f8d1f8bb15a6ebf01f1bc2db8524e26cf89092",
	version: "v2.0.0-alpha.3",
};

export const diagnosticsSource = String.raw`package diagnostics

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// LogDirectory is set only in the manually requested diagnostic binary.
var LogDirectory string

// Record writes sparse process lifecycle data without affecting process behavior.
func Record(event string, result int) {
	if LogDirectory == "" || event == "" {
		return
	}
	filePath := filepath.Join(LogDirectory, strconv.Itoa(os.Getpid())+".jsonl")
	file, err := os.OpenFile(
		filePath,
		os.O_CREATE|os.O_APPEND|os.O_WRONLY,
		0600,
	)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(
		file,
		"{\"time_unix_nano\":%d,\"event\":%q,\"result\":%d}\n",
		time.Now().UnixNano(),
		event,
		result,
	)
	_ = file.Close()
}
`;

const patches = [
	{
		file: "cmd/embedded-postgres/main.go",
		sha256: "e4e726d5190c478f4a8713bc283740be965bc6f8bb2eabb6cd3c6b57721719df",
		edits: [
			[
				'\t"github.com/fergusstrange/embedded-postgres/v2/internal/cli"',
				'\t"github.com/fergusstrange/embedded-postgres/v2/internal/cli"\n\t"github.com/fergusstrange/embedded-postgres/v2/internal/diagnostics"',
			],
			[
				"func main() { os.Exit(run()) }",
				`func main() {
	diagnostics.Record("main-entry", 0)
	code := run()
	diagnostics.Record("main-exit", code)
	os.Exit(code)
}`,
			],
		],
	},
	{
		file: "internal/supervisor/supervisor.go",
		sha256: "abec263a302191387723018d42d548ee4afedec68756fe3888fc2358c947c7ee",
		edits: [
			[
				"func Run(input io.Reader, output io.Writer) error {",
				`func Run(input io.Reader, output io.Writer) (returnedErr error) {
	defer func() {
		if returnedErr == nil {
			diagnostics.Record("supervisor-return", 0)
		} else {
			diagnostics.Record("supervisor-return-error", 1)
		}
	}()`,
			],
			[
				'\t"github.com/fergusstrange/embedded-postgres/v2/internal/platform"',
				'\t"github.com/fergusstrange/embedded-postgres/v2/internal/diagnostics"\n\t"github.com/fergusstrange/embedded-postgres/v2/internal/platform"',
			],
			[
				`err = cmd.Start()
	release()
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()`,
				`err = cmd.Start()
	release()
	if err != nil {
		diagnostics.Record("postgres-start-error", 1)
		return err
	}
	diagnostics.Record("postgres-started", cmd.Process.Pid)
	done := make(chan error, 1)
	go func() {
		waitErr := cmd.Wait()
		exitCode := -1
		if cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		}
		diagnostics.Record("postgres-child-wait", exitCode)
		done <- waitErr
	}()`,
			],
			[
				`shutdown.Stdout = log
			shutdown.Stderr = log
			e = shutdown.Run()
			release()`,
				`shutdown.Stdout = log
			shutdown.Stderr = log
			e = shutdown.Start()
			started := e == nil
			if started {
				diagnostics.Record("pgctl-started", shutdown.Process.Pid)
				e = shutdown.Wait()
			}
			release()
			if !started {
				diagnostics.Record("pgctl-start-error", 1)
			} else {
				exitCode := -1
				if shutdown.ProcessState != nil {
					exitCode = shutdown.ProcessState.ExitCode()
				}
				diagnostics.Record("pgctl-return", exitCode)
			}`,
			],
			[
				`if e != nil || ctx.Err() != nil {
			_ = platform.Kill(cmd.Process)
		}`,
				`if e != nil || ctx.Err() != nil {
			if ctx.Err() != nil {
				diagnostics.Record("pgctl-context-ended", 1)
			}
			killErr := platform.Kill(cmd.Process)
			if killErr == nil {
				diagnostics.Record("postgres-kill", 0)
			} else {
				diagnostics.Record("postgres-kill-error", 1)
			}
		}`,
			],
			[
				`case <-ctx.Done():
			_ = platform.Kill(cmd.Process)
			<-done`,
				`case <-ctx.Done():
			diagnostics.Record("pgctl-context-done", 1)
			killErr := platform.Kill(cmd.Process)
			if killErr == nil {
				diagnostics.Record("postgres-timeout-kill", 0)
			} else {
				diagnostics.Record("postgres-timeout-kill-error", 1)
			}
			<-done`,
			],
			[
				`case <-ownerGone:
		return stop()
	case err := <-done:`,
				`case <-ownerGone:
			diagnostics.Record("owner-eof", 0)
			stopErr := stop()
			if stopErr == nil {
				diagnostics.Record("owner-eof-stop-complete", 0)
			} else {
				diagnostics.Record("owner-eof-stop-error", 1)
			}
			return stopErr
		case err := <-done:`,
			],
		],
	},
	{
		file: "internal/supervisor/command.go",
		sha256: "60184359cb3470e4f3bc2453eba1e46dac8dbeb7f99f227fe7354a27875c34d5",
		edits: [
			[
				"func RunCommand(input io.Reader, output io.Writer) error {",
				`func RunCommand(input io.Reader, output io.Writer) (returnedErr error) {
	defer func() {
		if returnedErr == nil {
			diagnostics.Record("command-supervisor-return", 0)
		} else {
			diagnostics.Record("command-supervisor-return-error", 1)
		}
	}()`,
			],
			[
				'\t"github.com/fergusstrange/embedded-postgres/v2/internal/platform"',
				'\t"github.com/fergusstrange/embedded-postgres/v2/internal/diagnostics"\n\t"github.com/fergusstrange/embedded-postgres/v2/internal/platform"',
			],
			[
				`err = cmd.Start()
	release()
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()`,
				`err = cmd.Start()
	release()
	if err != nil {
		diagnostics.Record("command-start-error", 1)
		return err
	}
	diagnostics.Record("command-started", cmd.Process.Pid)
	done := make(chan error, 1)
	go func() {
		waitErr := cmd.Wait()
		exitCode := -1
		if cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		}
		diagnostics.Record("command-child-wait", exitCode)
		done <- waitErr
	}()`,
			],
			[
				`case err := <-done:
		return err
	case <-gone:
		_ = platform.Kill(cmd.Process)
		return <-done`,
				`case err := <-done:
		return err
	case <-gone:
		diagnostics.Record("command-owner-eof", 0)
		killErr := platform.Kill(cmd.Process)
		if killErr == nil {
			diagnostics.Record("command-kill", 0)
		} else {
			diagnostics.Record("command-kill-error", 1)
		}
		return <-done`,
			],
		],
	},
];

const modulePath = "github.com/fergusstrange/embedded-postgres/v2";
const archiveLimit = 64 * 1024 * 1024;
const inertPaxKeys = new Set([
	"atime",
	"comment",
	"ctime",
	"gid",
	"gname",
	"mtime",
	"uid",
	"uname",
]);

function sha256(value) {
	return createHash("sha256").update(value).digest("hex");
}

function textField(header, start, length) {
	return header
		.subarray(start, start + length)
		.toString("utf8")
		.split("\0", 1)[0];
}

function tarNumber(header, start, length) {
	const raw = header.subarray(start, start + length);
	if (raw[0] & 0x80) throw new Error("base-256 tar numbers are not accepted");
	const value = raw.toString("ascii").replace(/\0.*$/s, "").trim();
	if (!value) return 0;
	if (!/^[0-7]+$/.test(value)) throw new Error("invalid tar numeric field");
	return Number.parseInt(value, 8);
}

function parsePax(payload) {
	const values = new Map();
	let offset = 0;
	while (offset < payload.length) {
		const space = payload.indexOf(0x20, offset);
		if (space < 0) throw new Error("malformed PAX tar record");
		const lengthText = payload.subarray(offset, space).toString("ascii");
		if (!/^\d+$/.test(lengthText)) throw new Error("malformed PAX tar length");
		const length = Number.parseInt(lengthText, 10);
		const end = offset + length;
		if (
			length <= space - offset + 1 ||
			end > payload.length ||
			payload[end - 1] !== 0x0a
		) {
			throw new Error("malformed PAX tar record length");
		}
		const record = payload.subarray(space + 1, end - 1).toString("utf8");
		const equals = record.indexOf("=");
		if (equals < 1) throw new Error("malformed PAX tar key");
		values.set(record.slice(0, equals), record.slice(equals + 1));
		offset = end;
	}
	return values;
}

export function validateTarArchive(gzipBytes) {
	const bytes = gunzipSync(gzipBytes, { maxOutputLength: archiveLimit });
	const entries = [];
	const seen = new Set();
	let pendingPax;
	let offset = 0;
	let ended = false;
	while (offset + 512 <= bytes.length) {
		const header = bytes.subarray(offset, offset + 512);
		if (header.every((byte) => byte === 0)) {
			ended = true;
			break;
		}
		const storedChecksum = tarNumber(header, 148, 8);
		let checksum = 0;
		for (let i = 0; i < 512; i++)
			checksum += i >= 148 && i < 156 ? 32 : header[i];
		if (storedChecksum !== checksum)
			throw new Error("tar header checksum mismatch");
		const size = tarNumber(header, 124, 12);
		const type = String.fromCharCode(header[156] || 48);
		const name = textField(header, 0, 100);
		const prefix = textField(header, 345, 155);
		const headerName = prefix ? `${prefix}/${name}` : name;
		const dataStart = offset + 512;
		const dataEnd = dataStart + size;
		if (dataEnd > bytes.length) throw new Error("truncated tar entry");
		if (type === "g") {
			const globalPax = parsePax(bytes.subarray(dataStart, dataEnd));
			if ([...globalPax.keys()].some((key) => !inertPaxKeys.has(key))) {
				throw new Error("unsafe global PAX tar metadata");
			}
		} else if (type === "x") {
			if (pendingPax)
				throw new Error("nested PAX tar headers are not accepted");
			pendingPax = parsePax(bytes.subarray(dataStart, dataEnd));
			if (
				[...pendingPax.keys()].some(
					(key) => key !== "path" && !inertPaxKeys.has(key),
				)
			) {
				throw new Error("unsafe PAX tar metadata");
			}
		} else {
			if (type !== "0" && type !== "5")
				throw new Error(`unsupported tar entry type ${JSON.stringify(type)}`);
			const entryName = pendingPax?.get("path") ?? headerName;
			pendingPax = undefined;
			const normalized = entryName.endsWith("/")
				? entryName.slice(0, -1)
				: entryName;
			const components = normalized.split("/");
			if (
				!normalized ||
				normalized.startsWith("/") ||
				normalized.includes("\\") ||
				normalized.includes(":") ||
				components.some(
					(component) => !component || component === "." || component === "..",
				) ||
				components[0] !== source.root
			) {
				throw new Error(`unsafe archive path ${JSON.stringify(entryName)}`);
			}
			if (seen.has(normalized))
				throw new Error(`duplicate archive path ${JSON.stringify(entryName)}`);
			seen.add(normalized);
			entries.push({ path: normalized, type });
		}
		offset = dataStart + Math.ceil(size / 512) * 512;
	}
	if (!ended || pendingPax || entries.length === 0)
		throw new Error("incomplete or empty tar archive");
	return entries;
}

export function applyPinnedPatch(relativePath, contents, patch) {
	const before = Buffer.from(contents);
	const beforeHash = sha256(before);
	if (beforeHash !== patch.sha256) {
		throw new Error(
			`upstream preimage hash mismatch for ${relativePath}: ${beforeHash}`,
		);
	}
	let output = before.toString("utf8");
	for (const [needle, replacement] of patch.edits) {
		const first = output.indexOf(needle);
		if (first < 0 || output.indexOf(needle, first + needle.length) >= 0) {
			throw new Error(`pinned patch anchor mismatch for ${relativePath}`);
		}
		output = `${output.slice(0, first)}${replacement}${output.slice(first + needle.length)}`;
	}
	return {
		contents: Buffer.from(output),
		preimageSha256: beforeHash,
		postimageSha256: sha256(output),
	};
}

function checkedRunnerTemp(value) {
	if (typeof value !== "string" || !value.trim() || !path.isAbsolute(value)) {
		throw new Error("RUNNER_TEMP must be a nonempty absolute directory");
	}
	const absolute = path.resolve(value);
	if (absolute === path.parse(absolute).root)
		throw new Error("RUNNER_TEMP cannot be a filesystem root");
	return absolute;
}

function run(command, args, options = {}) {
	const result = spawnSync(command, args, {
		stdio: "inherit",
		windowsHide: true,
		...options,
	});
	if (result.error) throw result.error;
	if (result.signal)
		throw new Error(`${command} was terminated by ${result.signal}`);
	if (result.status !== 0)
		throw new Error(`${command} failed with exit code ${result.status ?? 1}`);
}

async function downloadArchive() {
	const response = await fetch(source.url, { redirect: "follow" });
	if (!response.ok)
		throw new Error(
			`pinned upstream archive download failed with HTTP ${response.status}`,
		);
	const bytes = Buffer.from(await response.arrayBuffer());
	const actualHash = sha256(bytes);
	if (actualHash !== source.archiveSha256)
		throw new Error(`pinned upstream archive checksum mismatch: ${actualHash}`);
	validateTarArchive(bytes);
	return bytes;
}

async function prepare() {
	const runnerTemp = checkedRunnerTemp(process.env.RUNNER_TEMP);
	if (
		!process.env.GITHUB_OUTPUT ||
		!path.isAbsolute(process.env.GITHUB_OUTPUT)
	) {
		throw new Error("GITHUB_OUTPUT must be an absolute path");
	}
	const artifactDirectory = path.join(
		runnerTemp,
		"embedded-postgres-diagnostics",
	);
	mkdirSync(artifactDirectory, { recursive: true });
	appendFileSync(process.env.GITHUB_OUTPUT, `artifact=${artifactDirectory}\n`);
	const sourceDirectory = mkdtempSync(
		path.join(runnerTemp, "embedded-postgres-source-"),
	);
	const buildDirectory = mkdtempSync(
		path.join(runnerTemp, "embedded-postgres-helper-"),
	);
	const archivePath = path.join(runnerTemp, "embedded-postgres-pinned.tar.gz");
	const archiveBytes = await downloadArchive();
	writeFileSync(archivePath, archiveBytes, { flag: "wx" });
	validateTarArchive(readFileSync(archivePath));
	run("tar", ["-xzf", archivePath, "-C", sourceDirectory]);
	const root = path.join(sourceDirectory, source.root);
	const patchRecords = [];
	const changedGoFiles = [];
	for (const patch of patches) {
		const target = path.join(root, patch.file);
		const applied = applyPinnedPatch(patch.file, readFileSync(target), patch);
		writeFileSync(target, applied.contents);
		changedGoFiles.push(patch.file);
		patchRecords.push({
			path: patch.file,
			preimage_sha256: applied.preimageSha256,
		});
	}
	const diagnosticsPath = path.join(
		root,
		"internal",
		"diagnostics",
		"trace.go",
	);
	mkdirSync(path.dirname(diagnosticsPath), { recursive: true });
	writeFileSync(diagnosticsPath, diagnosticsSource, { flag: "wx" });
	changedGoFiles.push("internal/diagnostics/trace.go");
	patchRecords.push({
		path: "internal/diagnostics/trace.go",
		preimage_sha256: null,
	});
	run("gofmt", ["-w", ...changedGoFiles], { cwd: root });
	for (const patchRecord of patchRecords) {
		patchRecord.postimage_sha256 = sha256(
			readFileSync(path.join(root, patchRecord.path)),
		);
	}
	const licensePath = path.join(root, "LICENSE");
	const licenseBytes = readFileSync(licensePath);
	const diagnosticLicense = path.join(artifactDirectory, "UPSTREAM-LICENSE");
	writeFileSync(diagnosticLicense, licenseBytes);
	const supervisorPath = path.join(buildDirectory, "embedded-postgres.exe");
	const logDirectory = artifactDirectory.replaceAll("\\", "/");
	const ldflags = `-s -w -X ${modulePath}.ReleaseVersion=${source.version} -X ${modulePath}/internal/diagnostics.LogDirectory=${JSON.stringify(logDirectory)}`;
	const buildArgs = [
		"build",
		"-trimpath",
		"-ldflags",
		ldflags,
		"-o",
		supervisorPath,
		"./cmd/embedded-postgres",
	];
	const goVersion = spawnSync("go", ["version"], {
		encoding: "utf8",
		windowsHide: true,
	});
	if (goVersion.error) throw goVersion.error;
	if (goVersion.status !== 0)
		throw new Error("could not determine Go toolchain version");
	run("go", buildArgs, {
		cwd: root,
		env: { ...process.env, CGO_ENABLED: "0" },
	});
	const supervisorBytes = readFileSync(supervisorPath);
	const patchManifest = {
		upstream: {
			commit: source.commit,
			archive_url: source.url,
			archive_sha256: source.archiveSha256,
		},
		patches: patchRecords,
		patch_sha256: sha256(JSON.stringify(patches)),
		patch_manifest_sha256: sha256(JSON.stringify(patchRecords)),
		helper_sha256: sha256(readFileSync(fileURLToPath(import.meta.url))),
		go_version: goVersion.stdout.trim(),
		build_command: {
			executable: "go",
			args: buildArgs,
			environment: { CGO_ENABLED: "0" },
		},
		format_command: `gofmt -w ${changedGoFiles.join(" ")}`,
		binary_sha256: sha256(supervisorBytes),
		license_sha256: sha256(licenseBytes),
		instrumented_diagnostic_binary: true,
		not_for_release: true,
		diagnostic_not_a_fix: true,
		diagnostic_file_writes_may_perturb_timing: true,
	};
	writeFileSync(
		path.join(artifactDirectory, "provenance.json"),
		`${JSON.stringify(patchManifest, null, 2)}\n`,
	);
	appendFileSync(process.env.GITHUB_OUTPUT, `supervisor=${supervisorPath}\n`);
}

export async function main() {
	await prepare();
}

export function isMain(
	moduleUrl = import.meta.url,
	entryArgument = process.argv[1],
	platform = process.platform,
) {
	if (!entryArgument) return false;
	const entryUrl = entryArgument.startsWith("file:")
		? entryArgument
		: pathToFileURL(path.resolve(entryArgument)).href;
	return platform === "win32"
		? entryUrl.toLowerCase() === moduleUrl.toLowerCase()
		: entryUrl === moduleUrl;
}

if (isMain()) {
	main().catch((error) => {
		console.error(error instanceof Error ? error.message : error);
		process.exitCode = 1;
	});
}
