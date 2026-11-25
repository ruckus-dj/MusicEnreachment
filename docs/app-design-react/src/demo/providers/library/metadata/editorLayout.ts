import { metadataFilledSummary } from "../../../../ui/features/library/MetadataFieldPresentation";
import type { Locale } from "../../../../ui/i18n/types";
import type { MetadataField, MetadataScope } from "./registry";

const essentials: Partial<Record<MetadataScope, string[]>> = {
	release: [
		"album",
		"releasedate",
		"releasecountry",
		"recordlabel",
		"catalognumber",
		"release_primary_type",
		"release_secondary_types",
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
	locale: Locale = "ru",
): string {
	const populated = fields.filter((field) =>
		values[field.key]?.some(Boolean),
	).length;
	return metadataFilledSummary(locale, populated);
}
