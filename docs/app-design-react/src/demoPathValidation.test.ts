import { describe, expect, it } from "vitest";
import { demoPathsOverlap, overlapsAnyDemoPath } from "./demoPathValidation";

describe("demo path overlap validation", () => {
	it.each([
		["equal", "/music", "/music"],
		["first is parent", "/music", "/music/inbox"],
		["second is parent", "/music/inbox", "/music"],
		[
			"normalized separators and dot segments",
			"/music/./inbox",
			"\\music\\inbox\\",
		],
		["Windows drive paths, case-insensitive", "C:\\Music", "c:/music/inbox"],
	])("rejects overlap when %s", (_description, first, second) => {
		expect(demoPathsOverlap(first, second)).toBe(true);
	});

	it.each([
		["sibling prefix", "/music", "/music2"],
		["different volumes", "C:\\Music", "D:\\Music"],
		["different Unix roots", "/music", "/archive"],
	])("allows non-overlap for %s", (_description, first, second) => {
		expect(demoPathsOverlap(first, second)).toBe(false);
	});

	it("allows an unset work path and checks each configured peer independently", () => {
		expect(overlapsAnyDemoPath("", ["/music", "/tools", "/output"])).toBe(
			false,
		);
		for (const peer of ["/music", "/tools", "/output"]) {
			expect(overlapsAnyDemoPath(`${peer}/nested`, [peer])).toBe(true);
			expect(overlapsAnyDemoPath(peer, [`${peer}/nested`])).toBe(true);
		}
	});
});
