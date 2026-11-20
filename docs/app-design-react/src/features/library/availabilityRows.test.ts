import { expect, it } from "vitest";
import { originalReleases } from "./catalogData";
import { availabilityRowClass } from "./availabilityRows";

const track = (id: string) => {
	const found = originalReleases
		.flatMap((release) => release.tracks)
		.find((item) => item.id === id);
	if (!found) throw new Error(`Missing track fixture: ${id}`);
	return found;
};

it("applies unified filter semantics without changing all-state row classes", () => {
	const published = track("north-t1");
	const missing = {
		...track("north-t9"),
		published: null,
		sourceId: null,
		sourceIds: [],
	};
	const audio = (item: typeof published) =>
		Boolean(item.published || item.sourceId);

	expect(availabilityRowClass(published, "all", audio)).toBe("");
	expect(availabilityRowClass(missing, "all", audio)).toBe("");
	expect(availabilityRowClass(missing, "missing", audio)).toBe("filter-match");
	expect(availabilityRowClass(published, "missing", audio)).toBe(
		"filter-muted",
	);
	expect(availabilityRowClass(published, "available", audio)).toBe(
		"filter-match",
	);
	expect(availabilityRowClass(missing, "available", audio)).toBe(
		"filter-muted",
	);
	expect(availabilityRowClass(published, "published", audio)).toBe(
		"filter-match",
	);
	expect(availabilityRowClass(missing, "published", audio)).toBe(
		"filter-muted",
	);
});
