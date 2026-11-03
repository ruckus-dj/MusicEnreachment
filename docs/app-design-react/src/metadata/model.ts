import { fieldByKey } from "./registry";
import type { MetadataValues } from "./projection";

export interface MetadataCredit {
	/** Local catalog identity; never inferred from a MusicBrainz UUID. */
	artistId: string;
	mbid?: string;
	creditName: string;
	joinPhrase: string;
}

export interface MetadataContributor {
	name: string;
	sortName?: string;
	mbid?: string;
	instrument?: string;
}

export interface MetadataWork {
	title: string;
	mbid?: string;
}

function performerParts(value: string): { name: string; instrument?: string } {
	const match = /^(.*?)\s+\((.*)\)$/.exec(value);
	return match ? { name: match[1], instrument: match[2] } : { name: value };
}

/** Return an ordered credit list without shifting values when an aligned array is sparse. */
export function readMetadataCredits(
	values: MetadataValues,
	context: "artist" | "albumartist",
): MetadataCredit[] {
	const names = values[context] ?? [];
	const ids = values[`musicbrainz_${context}id`] ?? [];
	const plurals = values[`${context}s`] ?? [];
	const count = Math.max(names.length, ids.length, plurals.length);
	return Array.from({ length: count }, (_, index) => ({
		artistId: "",
		...(ids[index] ? { mbid: ids[index] } : {}),
		creditName: plurals[index] ?? names[index] ?? "",
		joinPhrase: "",
	}));
}

export function writeMetadataCredits(
	values: MetadataValues,
	context: "artist" | "albumartist",
	credits: MetadataCredit[],
): MetadataValues {
	return {
		...values,
		[context]: credits.length
			? [
					credits
						.map((credit) => credit.creditName + credit.joinPhrase)
						.join(""),
				]
			: [],
		[`${context}s`]: credits.map((credit) => credit.creditName),
		[`musicbrainz_${context}id`]: credits.map((credit) => credit.mbid ?? ""),
	};
}

export function readMetadataContributors(
	values: MetadataValues,
	role: string,
): MetadataContributor[] {
	const names = values[role] ?? [];
	const ids = values[`musicbrainz_${role}id`] ?? [];
	const legacyInstruments =
		role === "performer" ? (values.performerinstrument ?? []) : [];
	const count = Math.max(names.length, ids.length, legacyInstruments.length);
	return Array.from({ length: count }, (_, index) => ({
		...(role === "performer"
			? performerParts(names[index] ?? "")
			: { name: names[index] ?? "" }),
		...(role === "performer" && legacyInstruments[index]
			? { instrument: legacyInstruments[index] }
			: {}),
		...(ids[index] ? { mbid: ids[index] } : {}),
	}));
}

export function writeMetadataContributors(
	values: MetadataValues,
	role: string,
	contributors: MetadataContributor[],
): MetadataValues {
	const next: MetadataValues = {
		...values,
		[role]: contributors.map((contributor) =>
			role === "performer" && contributor.instrument
				? `${contributor.name} (${contributor.instrument})`
				: contributor.name,
		),
		[`musicbrainz_${role}id`]: contributors.map(
			(contributor) => contributor.mbid ?? "",
		),
	};
	delete next.performerinstrument;
	return next;
}

export function readMetadataWorks(values: MetadataValues): MetadataWork[] {
	const titles = values.work ?? [];
	const ids = values.musicbrainz_workid ?? [];
	const count = Math.max(titles.length, ids.length);
	return Array.from({ length: count }, (_, index) => ({
		title: titles[index] ?? "",
		...(ids[index] ? { mbid: ids[index] } : {}),
	}));
}

export function writeMetadataWorks(
	values: MetadataValues,
	works: MetadataWork[],
): MetadataValues {
	return {
		...values,
		work: works.map((work) => work.title),
		musicbrainz_workid: works.map((work) => work.mbid ?? ""),
	};
}

export function knownMetadataKey(key: string): boolean {
	return fieldByKey(key) !== undefined;
}
