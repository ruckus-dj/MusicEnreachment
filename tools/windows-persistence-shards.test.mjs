import assert from "node:assert/strict";
import { test } from "node:test";
import {
	parseTestList,
	partitionTests,
	run,
	testRunPattern,
} from "./windows-persistence-shards.mjs";

test("persistence tests are split into stable, balanced, disjoint shards", () => {
	const input = [
		"TestDelta",
		"TestAlpha",
		"ExamplePackage",
		"FuzzSeeds",
		"TestCharlie",
	];
	const shards = partitionTests(input);

	assert.deepEqual(shards, [
		["ExamplePackage", "TestAlpha", "TestDelta"],
		["FuzzSeeds", "TestCharlie"],
	]);
	assert.deepEqual(partitionTests(input), shards);
	assert.ok(shards.every((shard) => shard.length > 0));
	assert.ok(Math.abs(shards[0].length - shards[1].length) <= 1);
	assert.deepEqual(shards.flat().sort(), [...input].sort());
	assert.equal(new Set(shards.flat()).size, input.length);
});

test("shard run patterns anchor and escape every selected top-level name", () => {
	const pattern = testRunPattern(["TestA.B+", "ExamplePackage_Name"]);
	const exact = new RegExp(pattern);

	assert.equal(pattern, "^(TestA\\.B\\+|ExamplePackage_Name)$");
	assert.ok(exact.test("TestA.B+"));
	assert.ok(exact.test("ExamplePackage_Name"));
	assert.ok(!exact.test("TestA.B+Subtest"));
	assert.ok(!exact.test("TestA.B"));
});

test("invalid, duplicate, and empty shard selections fail instead of dropping tests", () => {
	assert.throws(() => partitionTests(["TestOnly"]), /at least 2/);
	assert.throws(() => partitionTests(["TestOne", "TestOne"]), /duplicate/);
	assert.throws(() => partitionTests(["TestOne", "Helper"]), /invalid/);
	assert.throws(() => testRunPattern([]), /empty shard/);
});

test("bare Test, Example, and Fuzz names are valid top-level tests", () => {
	const shards = partitionTests(["Test", "Example", "Fuzz"]);
	assert.deepEqual(shards.flat().sort(), ["Example", "Fuzz", "Test"]);
	assert.equal(testRunPattern(["Test", "Example"]), "^(Test|Example)$");
});

test("test lists ignore status lines but reject malformed prefix output", () => {
	assert.deepEqual(
		parseTestList(
			"TestAlpha\r\nExamplePackage\r\nFuzzSeeds\r\nok  example/package\r\nPASS\r\n",
		),
		["TestAlpha", "ExamplePackage", "FuzzSeeds"],
	);
	assert.deepEqual(parseTestList("Test\nExample\nFuzz\n"), [
		"Test",
		"Example",
		"Fuzz",
	]);
	assert.throws(() => parseTestList("Test output\n"), /invalid top-level/);
	assert.throws(() => parseTestList("Test-Name\n"), /invalid top-level/);
	assert.throws(() => parseTestList("ok  example/package\n"), /no tests/);
});

test("test lists accept whole Go identifiers, including Unicode names", () => {
	assert.deepEqual(
		parseTestList("TestПривет\nExample日本\nFuzz_ñ\nTest_123\n"),
		["TestПривет", "Example日本", "Fuzz_ñ", "Test_123"],
	);
	assert.throws(() => partitionTests(["TestПривет", "Helper日本"]), /invalid/);
});

test("captured subprocess failures emit stdout and stderr before exit", () => {
	let stdout = "";
	let stderr = "";
	let exitCode;
	run(
		process.execPath,
		[
			"-e",
			"process.stdout.write('child stdout\\n'); process.stderr.write('child stderr\\n'); process.exit(7);",
		],
		{
			capture: true,
			writeStdout: (text) => {
				stdout += text;
			},
			writeStderr: (text) => {
				stderr += text;
			},
			exit: (code) => {
				exitCode = code;
			},
		},
	);

	assert.equal(exitCode, 7);
	assert.match(stdout, /child stdout/);
	assert.match(stderr, /child stderr/);
});
