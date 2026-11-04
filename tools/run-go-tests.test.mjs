import assert from "node:assert/strict";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { EventEmitter } from "node:events";
import { PassThrough } from "node:stream";
import { test } from "node:test";
import { consumeJSONLines, createAggregator, runGoTests } from "./run-go-tests.mjs";

test("JSON event aggregation reports package elapsed and conservative runtime", () => {
	const aggregate = createAggregator();
	for (const event of [
		{ Package: "p", Action: "run", Test: "TestA" },
		{ Package: "p", Action: "pass", Test: "TestA" },
		{ Package: "p", Action: "run", Test: "TestB/child" },
		{ Package: "p", Action: "pass", Test: "TestB/child" },
		{ Package: "p", Action: "output", Output: "testpostgres: created database\n" },
		{ Package: "p", Action: "pass", Elapsed: 4 },
		{ Package: "q", Action: "pass", Elapsed: 2 },
	]) aggregate.accept(JSON.stringify(event));
	const summary = aggregate.summary(8);
	assert.equal(summary.wallSeconds, 8);
	assert.equal(summary.packageElapsedLowerBoundSeconds, 4);
	assert.equal(summary.packages.p.topLevelTests, 2);
	assert.equal(summary.packages.p.leafTests, 2);
	assert.equal(summary.packages.p.elapsedSeconds, 4);
	assert.deepEqual(summary.packageCounts, { pass: 2, fail: 0, skip: 0 });
	assert.deepEqual(summary.testpostgres, ["testpostgres: created database"]);
});

test("stream parser handles chunk boundaries and a final unterminated line", async () => {
	const input = new PassThrough();
	const lines = [];
	const done = consumeJSONLines(input, (line) => lines.push(line));
	input.write('{"Action":"pass",');
	input.end('"Package":"p"}');
	await done;
	assert.deepEqual(lines, ['{"Action":"pass","Package":"p"}']);
});

function fakeProcess({ code = 0, signal = null, json = "", stderr = "", error } = {}) {
	const child = new EventEmitter();
	child.stdout = new PassThrough();
	child.stderr = new PassThrough();
	child.killedWith = [];
	child.kill = (requested) => {
		child.killedWith.push(requested);
		finish(null, requested);
		return true;
	};
	function finish(exitCode = code, exitSignal = signal) {
		child.stdout.end(json);
		child.stderr.end(stderr);
		process.nextTick(() => {
			if (error) child.emit("error", error);
			child.emit("close", exitCode, exitSignal);
		});
	}
	return { child, finish };
}

async function fixture(t) {
	const root = await mkdtemp(path.join(tmpdir(), "run-go-tests-"));
	t.after(() => rm(root, { recursive: true, force: true }));
	const output = new PassThrough();
	const errors = new PassThrough();
	let stdoutText = "";
	let stderrText = "";
	output.on("data", (chunk) => { stdoutText += chunk; });
	errors.on("data", (chunk) => { stderrText += chunk; });
	return {
		root,
		output,
		errors,
		get stdoutText() { return stdoutText; },
		get stderrText() { return stderrText; },
	};
}

const passEvent = `${JSON.stringify({ Package: "p", Action: "pass", Elapsed: 0.2 })}\n`;

test("runner finalizes artifacts on success and includes the final JSON line", async (t) => {
	const f = await fixture(t);
	const fake = fakeProcess({ json: passEvent.trimEnd() });
	let invokedWith;
	const result = await runGoTests({
		root: f.root,
		spawnProcess: (...args) => { invokedWith = args; process.nextTick(fake.finish); return fake.child; },
		stdout: f.output,
		stderr: f.errors,
		propagateSignal: assert.fail,
	});
	assert.equal(invokedWith[0], "go");
	assert.deepEqual(invokedWith[1], [
		"test",
		"-json",
		"-tags=integration",
		"-count=1",
		"-p",
		"8",
		"-parallel",
		"8",
		"./...",
	]);
	assert.deepEqual(invokedWith[2], {
		cwd: path.join(f.root, "backend"),
		stdio: ["inherit", "pipe", "pipe"],
	});
	assert.equal(result.packages.p.status, "pass");
	assert.equal((await readFile(path.join(f.root, "build/test-results/go.jsonl"), "utf8")), passEvent.trimEnd());
	assert.deepEqual(JSON.parse(await readFile(path.join(f.root, "build/test-results/go-summary.json"), "utf8")).packageCounts, { pass: 1, fail: 0, skip: 0 });
});

test("runner replays complete diagnostics on nonzero exit after artifacts finish", async (t) => {
	const f = await fixture(t);
	const diagnostic = JSON.stringify({ Package: "p", Action: "output", Output: "FAIL detail\n" });
	const fake = fakeProcess({ code: 7, json: `${diagnostic}\n`, stderr: "compiler diagnostic\n" });
	let exitCode;
	await runGoTests({ root: f.root, spawnProcess: () => { process.nextTick(fake.finish); return fake.child; }, stdout: f.output, stderr: f.errors, setExitCode: (code) => { exitCode = code; } });
	assert.equal(exitCode, 7);
	assert.match(f.stdoutText, /FAIL detail/);
	assert.match(f.stderrText, /compiler diagnostic/);
	assert.equal(await readFile(path.join(f.root, "build/test-results/go.jsonl"), "utf8"), `${diagnostic}\n`);
	assert.equal(await readFile(path.join(f.root, "build/test-results/go.stderr.txt"), "utf8"), "compiler diagnostic\n");
});

test("runner finalizes artifacts after spawn errors", async (t) => {
	const f = await fixture(t);
	const expected = new Error("missing go");
	const fake = fakeProcess({ error: expected });
	await assert.rejects(runGoTests({ root: f.root, spawnProcess: () => { process.nextTick(fake.finish); return fake.child; }, stdout: f.output, stderr: f.errors }), expected);
	assert.ok(await readFile(path.join(f.root, "build/test-results/go.jsonl")));
	assert.ok(await readFile(path.join(f.root, "build/test-results/go-summary.json")));
});

test("runner forwards parent cancellation and propagates it after child close", async (t) => {
	const f = await fixture(t);
	const signals = new EventEmitter();
	const fake = fakeProcess();
	let propagated;
	const running = runGoTests({
		root: f.root,
		spawnProcess: () => { process.nextTick(() => signals.emit("SIGTERM")); return fake.child; },
		signals,
		stdout: f.output,
		stderr: f.errors,
		propagateSignal: (signal) => { propagated = signal; },
	});
	await running;
	assert.deepEqual(fake.child.killedWith, ["SIGTERM"]);
	assert.equal(propagated, "SIGTERM");
	assert.equal(signals.listenerCount("SIGTERM"), 0);
});
