import { readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, it } from "vitest";

it("keeps only the Tailwind configuration entry, without handwritten CSS rules", () => {
	const source = dirname(fileURLToPath(import.meta.url));
	const files = readdirSync(source, { recursive: true }).map(String);
	expect(files.filter((file) => file.endsWith(".css"))).toEqual([
		"tailwind.css",
	]);
	const entry = readFileSync(join(source, "tailwind.css"), "utf8");
	expect(entry).toContain('@import "tailwindcss"');
	expect(entry).not.toMatch(
		/@apply\b|@layer\s+(base|components)|:root|\.demo-/,
	);
	const withoutConfiguration = entry
		.replace(/\/\*[\s\S]*?\*\//g, "")
		.replace(/@theme(?:\s+inline)?\s*\{[^}]*\}/g, "")
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
