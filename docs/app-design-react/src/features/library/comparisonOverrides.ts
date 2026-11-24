import type { MetadataValues } from "../../metadata/projection";

const sameValues = (left: readonly string[], right: readonly string[]) =>
	left.length === right.length &&
	left.every((value, index) => value === right[index]);

/**
 * Keep only fields that differ from the editor's seeded baseline. Editors seed
 * values from existing metadata, so persisting every input would turn untouched
 * fields into false overrides.
 */
export function mergeComparisonOverrides(
	previous: MetadataValues | undefined,
	seeded: MetadataValues,
	next: MetadataValues,
): MetadataValues {
	const overrides: MetadataValues = Object.fromEntries(
		Object.entries(previous ?? {}).map(([key, values]) => [key, [...values]]),
	);
	for (const [key, values] of Object.entries(next)) {
		if (sameValues(values, seeded[key] ?? [])) continue;
		overrides[key] = [...values];
	}
	return overrides;
}
