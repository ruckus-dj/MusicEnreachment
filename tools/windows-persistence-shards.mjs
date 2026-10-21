import { spawnSync } from "node:child_process";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";

const persistencePackage =
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence";
const shardCount = 2;
const testFlags = [
	"-tags=integration",
	"-count=1",
	"-p",
	"1",
	"-parallel",
	"8",
];
const topLevelPrefixes = ["Test", "Example", "Fuzz"];
// "Test", "Example", and "Fuzz" are valid bare Go test names. The prefix check
// plus whole-identifier validation rejects TestMain debug output such as
// "Test output" while leaving Go's own lower-case-suffix filtering to -list.
// A TestMain that prints a valid identifier beginning with Test is
// indistinguishable from a listed test; -list output is treated as authoritative.
const goIdentifier = /^[\p{L}_][\p{L}\p{Nd}_]*$/u;

export function partitionTests(testNames, count = shardCount) {
	if (!Number.isInteger(count) || count < 1) {
		throw new Error("shard count must be a positive integer");
	}
	if (!Array.isArray(testNames) || testNames.length < count) {
		throw new Error(`at least ${count} top-level tests are required`);
	}
	if (
		testNames.some(
			(name) =>
				typeof name !== "string" ||
				!topLevelPrefixes.some((prefix) => name.startsWith(prefix)) ||
				!goIdentifier.test(name),
		)
	) {
		throw new Error("test list contains an invalid top-level test name");
	}

	const sorted = [...testNames].sort();
	if (new Set(sorted).size !== sorted.length) {
		throw new Error("test list contains duplicate names");
	}
	return Array.from({ length: count }, (_, shard) =>
		sorted.filter((_, index) => index % count === shard),
	);
}

export function testRunPattern(testNames) {
	if (testNames.length === 0) {
		throw new Error("cannot create a run pattern for an empty shard");
	}
	const escaped = testNames.map((name) =>
		name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"),
	);
	return `^(${escaped.join("|")})$`;
}

export function parseTestList(output) {
	const names = [];
	for (const line of output.split(/\r?\n/)) {
		if (!topLevelPrefixes.some((prefix) => line.startsWith(prefix))) {
			continue;
		}
		if (!goIdentifier.test(line)) {
			throw new Error(
				`go test -list returned an invalid top-level test name: ${JSON.stringify(line)}`,
			);
		}
		names.push(line);
	}
	if (names.length === 0) {
		throw new Error(
			`go test -list returned no tests for ${persistencePackage}`,
		);
	}
	return names;
}

export function run(
	command,
	args,
	{
		capture = false,
		spawn = spawnSync,
		writeStdout = (text) => process.stdout.write(text),
		writeStderr = (text) => process.stderr.write(text),
		exit = (code) => process.exit(code),
	} = {},
) {
	const result = spawn(command, args, {
		encoding: capture ? "utf8" : undefined,
		stdio: capture ? "pipe" : "inherit",
		maxBuffer: 64 * 1024 * 1024,
	});
	if (result.error) {
		throw result.error;
	}
	if (result.status !== 0) {
		writeStdout(result.stdout ?? "");
		writeStderr(result.stderr ?? "");
		exit(result.status ?? 1);
		return "";
	}
	return result.stdout ?? "";
}

function listTopLevelTests() {
	return parseTestList(
		run("go", ["test", "-tags=integration", "-list=.", persistencePackage], {
			capture: true,
		}),
	);
}

function main() {
	const packages = run("go", ["list", "./..."], { capture: true })
		.split(/\r?\n/)
		.filter(Boolean);
	if (!packages.includes(persistencePackage)) {
		throw new Error(
			`go list did not include required package ${persistencePackage}`,
		);
	}

	const otherPackages = packages.filter((pkg) => pkg !== persistencePackage);
	if (otherPackages.length > 0) {
		console.log(
			`Testing ${otherPackages.length} non-persistence backend packages once`,
		);
		run("go", ["test", ...testFlags, ...otherPackages]);
	}

	const shards = partitionTests(listTopLevelTests());
	for (const [index, names] of shards.entries()) {
		console.log(
			`Persistence integration shard ${index + 1}/${shardCount} (${names.length} top-level tests): ${names.join(", ")}`,
		);
		run("go", [
			"test",
			...testFlags,
			"-run",
			testRunPattern(names),
			persistencePackage,
		]);
	}
}

if (
	process.argv[1] &&
	fileURLToPath(import.meta.url) === resolve(process.argv[1])
) {
	try {
		main();
	} catch (error) {
		console.error(error instanceof Error ? error.message : error);
		process.exitCode = 1;
	}
}
