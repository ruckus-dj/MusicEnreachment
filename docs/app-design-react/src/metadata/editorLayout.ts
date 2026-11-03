import type { MetadataField, MetadataScope } from "./registry";

const essentials: Partial<Record<MetadataScope, string[]>> = {
	release: [
		"album",
		"releasedate",
		"releasecountry",
		"recordlabel",
		"catalognumber",
		"releasetype",
	],
	recording: ["title", "explicitstatus", "isrc"],
	track: ["track", "disc"],
};

export function essentialMetadataFields(
	fields: MetadataField[],
	scope: MetadataScope,
): MetadataField[] {
	const keys = new Set(essentials[scope] ?? []);
	return fields.filter(
		(field) =>
			field.kind !== "genres" && (scope === "audio" || keys.has(field.key)),
	);
}

export function metadataSummary(
	fields: MetadataField[],
	values: Record<string, string[]>,
): string {
	const populated = fields.filter((field) =>
		values[field.key]?.some(Boolean),
	).length;
	return populated ? `${populated} заполнено` : "Не заполнено";
}
