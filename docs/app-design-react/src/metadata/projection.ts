import { fieldByKey, navidromeFields } from "./registry";
import type { MetadataScope } from "./registry";

export type MetadataValues = Record<string, string[]>;

export interface MetadataGenre {
	id?: string;
	name?: string;
}

export interface MetadataIssue {
	key: string;
	message: string;
}

const uuidPattern =
	/^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
const partialDatePattern = /^\d{4}(?:-\d{2}(?:-\d{2})?)?$/;

function validDate(value: string): boolean {
	if (!partialDatePattern.test(value)) return false;
	const [year, month, day] = value.split("-").map(Number);
	if (month !== undefined && (month < 1 || month > 12)) return false;
	if (day !== undefined) {
		const date = new Date(0);
		date.setUTCFullYear(year, month - 1, day);
		date.setUTCHours(0, 0, 0, 0);
		return (
			date.getUTCFullYear() === year &&
			date.getUTCMonth() === month - 1 &&
			date.getUTCDate() === day
		);
	}
	return true;
}

export function validateMetadataValues(
	values: MetadataValues,
	scope: MetadataScope,
): string[] {
	const errors: string[] = [];
	for (const [key, entries] of Object.entries(values)) {
		const field = fieldByKey(key);
		if (!field) {
			errors.push(`Unknown metadata field: ${key}`);
			continue;
		}
		if (field.scope !== scope) {
			errors.push(`${key} belongs to ${field.scope}, not ${scope}`);
			continue;
		}
		if (
			!Array.isArray(entries) ||
			entries.some((entry) => typeof entry !== "string")
		) {
			errors.push(`${key} must be a list of strings`);
			continue;
		}
		for (const value of entries) {
			if (!value) continue;
			if (field.kind === "date" && !validDate(value))
				errors.push(`${key} must be YYYY, YYYY-MM, or YYYY-MM-DD`);
			if (field.kind === "integer" && !/^\d+$/.test(value))
				errors.push(`${key} must be a non-negative integer`);
			if (field.kind === "number" && !Number.isFinite(Number(value)))
				errors.push(`${key} must be a number`);
			if (field.kind === "boolean" && !/^(true|false|1|0)$/i.test(value))
				errors.push(`${key} must be true or false`);
			if (field.kind === "advisory" && !/^(1|2|4|0)$/.test(value))
				errors.push(`${key} must be an explicit/clean advisory value`);
			if (
				key.startsWith("musicbrainz_") &&
				key !== "musicbrainz_discid" &&
				!uuidPattern.test(value)
			)
				errors.push(`${key} must be a UUID`);
		}
	}
	return errors;
}

/** Map explicitly stored registry values to the file tags Navidrome reads. */
export function projectMetadataValues(
	values: MetadataValues,
	scope?: MetadataScope,
): {
	tags: Record<string, string[]>;
	issues: MetadataIssue[];
} {
	const tags: Record<string, string[]> = {};
	const issues: MetadataIssue[] = [];
	for (const [key, entries] of Object.entries(values)) {
		const field = fieldByKey(key);
		if (!field) {
			issues.push({
				key,
				message: "Неизвестное поле метаданных не опубликовано.",
			});
			continue;
		}
		if (field.derived) continue;
		if (scope && field.scope !== scope) {
			issues.push({
				key,
				message: `Поле относится к области ${field.scope}, а не ${scope}.`,
			});
			continue;
		}
		if (!Array.isArray(entries)) {
			issues.push({
				key,
				message: "Значение поля должно быть упорядоченным списком.",
			});
			continue;
		}
		if (
			(key.startsWith("musicbrainz_") &&
				key !== "musicbrainz_discid" &&
				entries.some((value) => value && !uuidPattern.test(value))) ||
			(field.kind === "advisory" &&
				entries.some((value) => value && !/^(0|1|2|4)$/.test(value)))
		) {
			issues.push({
				key,
				message:
					"Некорректное значение идентификатора или advisory не опубликовано.",
			});
			continue;
		}
		if (
			field.kind === "date" &&
			entries.some((value) => value && !validDate(value))
		)
			issues.push({
				key,
				message:
					"Navidrome принимает частичные даты YYYY, YYYY-MM или YYYY-MM-DD.",
			});
		if (
			[
				"artist",
				"artists",
				"musicbrainz_artistid",
				"albumartist",
				"albumartists",
				"musicbrainz_albumartistid",
			].includes(key)
		) {
			if (entries.some((value) => !value))
				issues.push({
					key,
					message: "В упорядоченном credit есть пропуск; значения не сдвинуты.",
				});
		}
		tags[field.tag] = entries.some(Boolean) ? [...entries] : [];
	}
	return { tags, issues };
}

export function metadataKeyToTag(key: string): string | undefined {
	return fieldByKey(key)?.tag;
}

export function metadataTagKeys(values: MetadataValues): string[] {
	return [
		...new Set(
			Object.keys(values).flatMap((key) => {
				const field = fieldByKey(key);
				return field && !field.derived ? [field.tag] : [];
			}),
		),
	];
}

export function metadataManagedTagKeys(): string[] {
	return [
		...new Set(
			navidromeFields
				.filter((field) => !field.derived)
				.map((field) => field.tag),
		),
	];
}
