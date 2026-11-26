import { metadataFilledSummary } from "../../../../ui/features/library/MetadataFieldPresentation";
import { releaseBasicsFields } from "../../../../ui/features/library/metadata/releaseMetadataEditorMessages";
import type { Locale } from "../../../../ui/i18n/types";
import type { MetadataField, MetadataScope } from "./registry";

const essentials: Partial<Record<MetadataScope, string[]>> = {
	release: releaseBasicsFields.map(({ metadataKey }) => metadataKey),
	recording: ["title", "explicitstatus", "isrc"],
	track: ["track", "disc"],
};

export function essentialMetadataFields(
	fields: MetadataField[],
	scope: MetadataScope,
): MetadataField[] {
	if (scope === "audio")
		return fields.filter((field) => field.kind !== "genres");
	if (scope === "release") {
		const selected = new Map(fields.map((field) => [field.key, field]));
		return releaseBasicsFields.flatMap(({ metadataKey }) => {
			const field = selected.get(metadataKey);
			return field && field.kind !== "genres" ? [field] : [];
		});
	}
	const keys = new Set(essentials[scope] ?? []);
	return fields.filter(
		(field) => field.kind !== "genres" && keys.has(field.key),
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
