import assert from "node:assert/strict";
import canonical from "./src/original-fixtures.json" with { type: "json" };
import { validateFixtures } from "./check-fixtures.mjs";

const { test } = process.env.VITEST
	? await import("vitest")
	: await import("node:test");

test("canonical fixtures retain valid references", () => {
	assert.doesNotThrow(() => validateFixtures(structuredClone(canonical)));
});

for (const section of [
	"manualReleaseDemoData",
	"recoveryFixture",
	"localProvider",
]) {
	test(`reject missing retained section ${section}`, () => {
		const data = structuredClone(canonical);
		delete data[section];
		assert.throws(() => validateFixtures(data));
	});
}

test("reject a dangling Local release recording reference", () => {
	const data = structuredClone(canonical);
	const release = data.localProvider.releases.find(
		(item) => item.tracks?.length,
	);
	assert.ok(release, "fixture must contain a Local release track");
	release.tracks[0].recordingId = "missing-recording";
	assert.throws(() => validateFixtures(data));
});

test("reject library releases without a linked provider identity", () => {
	const data = structuredClone(canonical);
	data.releases.find((item) => item.id === "mix").providerId = "p-missing";
	assert.throws(() => validateFixtures(data), /provider identity/);
});

test("reject a dangling recovery track reference", () => {
	const data = structuredClone(canonical);
	data.recoveryFixture.trackId = "missing-track";
	assert.throws(() => validateFixtures(data));
});
