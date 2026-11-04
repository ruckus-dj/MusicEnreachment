import { createReadStream, createWriteStream } from "node:fs";
import { mkdir, writeFile } from "node:fs/promises";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { createInterface } from "node:readline";
import { pipeline } from "node:stream/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";

export function createAggregator() {
	const packages = {};
	const metrics = [];
	return {
		accept(line) {
			let event;
			try {
				event = JSON.parse(line);
			} catch {
				return;
			}
			const pkg = event.Package;
			if (!pkg) return;
			const info = (packages[pkg] ??= {
				tests: new Set(),
				status: null,
				elapsed: null,
			});
			if (event.Test && ["run", "pass", "fail", "skip"].includes(event.Action)) {
				info.tests.add(event.Test);
			}
			if (event.Action === "output" && /testpostgres/i.test(event.Output ?? "")) {
				metrics.push(event.Output.trimEnd());
			}
			if (["pass", "fail", "skip"].includes(event.Action) && !event.Test) {
				info.status = event.Action;
				info.elapsed = event.Elapsed ?? null;
			}
		},
		summary(wallSeconds) {
			const result = Object.fromEntries(
				Object.entries(packages).map(([pkg, info]) => {
					const tests = [...info.tests];
					return [
						pkg,
						{
							status: info.status,
							elapsedSeconds: info.elapsed,
							topLevelTests: new Set(tests.map((name) => name.split("/")[0])).size,
							leafTests: tests.filter(
								(name) => !tests.some((other) => other.startsWith(`${name}/`)),
							).length,
						},
					];
				}),
			);
			return {
				wallSeconds,
				subprocessWallSecondsIncludesCompile: true,
				packageElapsedLowerBoundSeconds: Math.max(
					0,
					...Object.values(result).map((pkg) => pkg.elapsedSeconds ?? 0),
				),
				packages: result,
				packageCounts: Object.fromEntries(
					["pass", "fail", "skip"].map((status) => [
						status,
						Object.values(result).filter((pkg) => pkg.status === status).length,
					]),
				),
				testpostgres: metrics,
			};
		},
	};
}

export function consumeJSONLines(stream, onLine) {
	const lines = createInterface({ input: stream, crlfDelay: Infinity });
	return new Promise((resolve, reject) => {
		lines.on("line", onLine);
		lines.on("close", resolve);
		lines.on("error", reject);
	});
}

export async function runGoTests({
	root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), ".."),
	spawnProcess = spawn,
	signals = process,
	stdout = process.stdout,
	stderr = process.stderr,
	propagateSignal = (signal) => process.kill(process.pid, signal),
	setExitCode = (code) => { process.exitCode = code; },
} = {}) {
	const outputDir = path.resolve(root, "build/test-results");
	await mkdir(outputDir, { recursive: true });
	const jsonPath = path.join(outputDir, "go.jsonl");
	const stderrPath = path.join(outputDir, "go.stderr.txt");
	const raw = createWriteStream(jsonPath);
	const errors = createWriteStream(stderrPath);
	const aggregator = createAggregator();
	const started = process.hrtime.bigint();
	const child = spawnProcess(
		"go",
		[
			"test",
			"-json",
			"-tags=integration",
			"-count=1",
			"-p",
			"8",
			"-parallel",
			"8",
			"./...",
		],
		{ cwd: path.join(root, "backend"), stdio: ["inherit", "pipe", "pipe"] },
	);
	let captureFailure;
	let captureKillSent = false;
	const failCapture = (error) => {
		captureFailure ??= error;
		if (!captureKillSent) {
			captureKillSent = true;
			child.kill();
		}
	};
	const rawDone = pipeline(child.stdout, raw).catch((error) => failCapture(error));
	const stderrDone = pipeline(child.stderr, errors).catch((error) => failCapture(error));
	const parseDone = consumeJSONLines(child.stdout, (line) => {
		aggregator.accept(line);
		let event;
		try {
			event = JSON.parse(line);
		} catch {
			return;
		}
		if (event.Package && !event.Test && ["pass", "fail"].includes(event.Action)) {
			console.log(`${event.Action.toUpperCase()} ${event.Package} (${event.Elapsed ?? 0}s)`);
		}
		if (event.Action === "output" && /testpostgres/i.test(event.Output ?? "")) {
			process.stdout.write(event.Output);
		}
	}).catch((error) => failCapture(error));
	let spawnError;
	let requestedSignal;
	const cancel = (signal) => {
		if (!requestedSignal) {
			requestedSignal = signal;
			child.kill(signal);
		}
	};
	const handlers = new Map(
		["SIGINT", "SIGTERM"].map((signal) => [signal, () => cancel(signal)]),
	);
	for (const [signal, handler] of handlers) signals.on(signal, handler);
	child.on("error", (error) => {
		spawnError = error;
		child.stdout.destroy();
		child.stderr.destroy();
	});
	const exit = await new Promise((resolve) =>
		child.on("close", (code, signal) => resolve({ code, signal })),
	);
	for (const [signal, handler] of handlers) signals.off(signal, handler);
	const streamResults = await Promise.allSettled([parseDone, rawDone, stderrDone]);
	const wallSeconds = Number(process.hrtime.bigint() - started) / 1e9;
	const summary = aggregator.summary(wallSeconds);
	await writeFile(
		path.join(outputDir, "go-summary.json"),
		`${JSON.stringify(summary, null, 2)}\n`,
	);
	if (spawnError) throw spawnError;
	if (captureFailure) throw captureFailure;
	const streamFailure = streamResults.find((result) => result.status === "rejected");
	if (streamFailure) throw streamFailure.reason;
	if (requestedSignal || exit.code !== 0 || exit.signal) {
		await replay(jsonPath, stdout);
		await replay(stderrPath, stderr);
		if (requestedSignal) propagateSignal(requestedSignal);
		else if (exit.signal) propagateSignal(exit.signal);
		else setExitCode(exit.code ?? 1);
	}
	return summary;
}

async function replay(filename, destination) {
	for await (const chunk of createReadStream(filename)) {
		if (!destination.write(chunk)) await once(destination, "drain");
	}
}

if (
	process.argv[1] &&
	fileURLToPath(import.meta.url) === path.resolve(process.argv[1])
) {
	runGoTests().catch((error) => {
		console.error(error);
		process.exitCode = 1;
	});
}
