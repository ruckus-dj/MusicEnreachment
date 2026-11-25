import { readdirSync, readFileSync } from "node:fs";
import { dirname, join, sep } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, it } from "vitest";

it("keeps the shared Tailwind configuration separate from host source discovery", () => {
	const source = join(dirname(fileURLToPath(import.meta.url)), "../..");
	expect(
		readdirSync(source, { withFileTypes: true })
			.map((entry) => entry.name)
			.sort(),
	).toEqual(["app", "demo", "ui"]);
	const files = readdirSync(source, { recursive: true })
		.map(String)
		.map((file) => file.split(sep).join("/"));
	expect(files.filter((file) => file.endsWith(".css")).sort()).toEqual([
		"app/styles.css",
		"ui/styles/index.css",
	]);
	const entry = readFileSync(join(source, "ui/styles/index.css"), "utf8");
	const hostEntry = readFileSync(join(source, "app/styles.css"), "utf8");
	expect(hostEntry).toContain('@import "../ui/styles/index.css"');
	expect(hostEntry.replace(/@(import|source)[^;]*;/g, "").trim()).toBe("");
	expect(entry).toContain('@import "tailwindcss"');
	expect(entry).not.toMatch(
		/@apply\b|@layer\s+(base|components)|:root|\.demo-/,
	);
	const withoutConfiguration = entry
		.replace(/\/\*[\s\S]*?\*\//g, "")
		.replace(/@theme(?:\s+inline)?\s*\{[^}]*\}/g, "")
		// A block variant may wrap @slot in selectors or media conditions,
		// but cannot contain declarations or handwritten component rules.
		.replace(
			/@custom-variant dark\s*\{\s*&:where\([^{};]+\)\s*\{\s*@slot;\s*\}\s*@media \(prefers-color-scheme: dark\)\s*\{\s*&:where\([^{};]+\)\s*\{\s*@slot;\s*\}\s*\}\s*\}/g,
			"",
		)
		.replace(/@(import|source|custom-variant)[^;]*;/g, "");
	expect(withoutConfiguration.trim()).toBe("");
	for (const file of files.filter(
		(file) => file.endsWith(".tsx") && !file.endsWith(".test.tsx"),
	)) {
		expect(readFileSync(join(source, file), "utf8"), file).not.toMatch(
			/\bstyle\s*=|<style\b/,
		);
	}
});
