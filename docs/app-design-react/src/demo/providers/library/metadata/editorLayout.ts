import { metadataFilledSummary } from "../../../../ui/features/library/MetadataFieldPresentation";
import {
	releaseAdditionalFields,
	releaseBasicsFields,
	releaseDetailFields,
	type ReleaseScalarField,
} from "../../../../ui/features/library/metadata/releaseMetadataEditorMessages";
import type { Locale } from "../../../../ui/i18n/types";
import {
	fieldByKey,
	fieldsForScope,
	type MetadataField,
	type MetadataScope,
} from "./registry";

function releaseEditorFields(
	fields: readonly ReleaseScalarField[],
	group: string,
): MetadataField[] {
	return fields.map(({ metadataKey, kind }) => ({
		key: metadataKey,
		label: metadataKey,
		tag: fieldByKey(metadataKey)?.tag ?? "",
		scope: "release",
		kind,
		group,
	}));
}

export const libraryReleaseEditorFields = [
	...releaseEditorFields(releaseBasicsFields, "release"),
	...fieldsForScope("release").filter(
		({ group }) => group === "release artists",
	),
	...releaseEditorFields(releaseDetailFields, "release"),
	...releaseEditorFields(releaseAdditionalFields, "additional"),
];

/**
 * Position metadata keeps its title in the same "Titles" group as the library
 * editor. The shared registry assigns `title` to the recording scope, so the
 * position editor supplies its own track-scoped field instead of widening it.
 */
export const libraryPositionEditorFields: MetadataField[] = [
	{
		key: "title",
		label: "title",
		tag: fieldByKey("title")?.tag ?? "TITLE",
		scope: "track",
		kind: "text",
		group: "main",
	},
	...fieldsForScope("track"),
];

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
