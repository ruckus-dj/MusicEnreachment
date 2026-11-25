import {
	formatLocalDuration,
	type LocalReleaseDraft,
	parseLocalDuration,
} from "../../../ui/features/local-release/localReleaseFormModel";
import type { LocalReleaseFormInitialValues } from "../../../ui/features/local-release/useLocalReleaseForm";
import type { GenreReference } from "../library/metadata/GenrePicker";
import type {
	LocalProviderRecord,
	LocalProviderState,
	LocalProviderType,
} from "./localProviderModel";

export const LOCAL_CATALOG_PAGE_SIZE = 3;

export const localCatalogTypes = [
	"releases",
	"recordings",
	"artists",
] as const satisfies readonly LocalProviderType[];

export type LocalCatalogType = (typeof localCatalogTypes)[number];

export type LocalCatalogView =
	| { kind: "list" }
	| { kind: "details"; type: LocalCatalogType; id: string }
	| { kind: "create"; type: LocalCatalogType }
	| { kind: "edit"; type: LocalCatalogType; id: string };

const normalize = (value: unknown) =>
	String(value ?? "")
		.normalize("NFKC")
		.toLocaleLowerCase()
		.replace(/\s+/g, " ")
		.trim();

export function localCatalogRecords(
	state: LocalProviderState,
	type: LocalCatalogType,
) {
	return state.items[type].filter((item) => item.provider === "Local");
}

export function localCatalogGenreNames(record: LocalProviderRecord) {
	return (record.genres ?? [])
		.map((id) => record.genreNamesById?.[id] ?? id)
		.filter(Boolean);
}

export function localCatalogArtistNames(
	state: LocalProviderState,
	artistIds: readonly string[] | undefined,
) {
	const names = (artistIds ?? []).flatMap((id) => {
		const artist = state.items.artists.find((item) => item.id === id);
		return artist?.name ? [artist.name] : [];
	});
	return names;
}

export function localCatalogReleaseTitle(record: LocalProviderRecord) {
	return (
		record.metadataValues?.album?.[0] ||
		record.title ||
		record.release_group_title ||
		""
	);
}

export function localCatalogRecordingTitle(record: LocalProviderRecord) {
	return record.title || record.metadataValues?.title?.[0] || "";
}

function searchText(
	type: LocalCatalogType,
	record: LocalProviderRecord,
	state: LocalProviderState,
) {
	const artists = localCatalogArtistNames(state, record.artistIds).join(" ");
	if (type === "artists") {
		return normalize(
			[
				record.name,
				record.sortName,
				record.disambiguation,
				record.tags,
				localCatalogGenreNames(record).join(" "),
				record.key,
			].join(" "),
		);
	}
	if (type === "recordings") {
		return normalize(
			[
				localCatalogRecordingTitle(record),
				artists,
				record.disambiguation,
				record.isrcs,
				record.tags,
				localCatalogGenreNames(record).join(" "),
				record.key,
			].join(" "),
		);
	}
	const tracks = (record.tracks ?? []).flatMap((track) => {
		const recording = state.items.recordings.find(
			(item) => item.id === track.recordingId,
		);
		return [
			recording ? localCatalogRecordingTitle(recording) : "",
			track.titleOverride,
		];
	});
	return normalize(
		[
			localCatalogReleaseTitle(record),
			record.release_group_title,
			artists,
			record.release_date,
			record.country_code,
			record.format,
			record.label,
			record.catalog_number,
			record.disambiguation,
			record.primaryType,
			record.language,
			record.script,
			record.key,
			...tracks,
		].join(" "),
	);
}

export function filterLocalCatalogRecords(
	state: LocalProviderState,
	type: LocalCatalogType,
	records: readonly LocalProviderRecord[],
	query: string,
) {
	const terms = normalize(query).split(" ").filter(Boolean);
	if (!terms.length) return [...records];
	return records.filter((record) => {
		const text = searchText(type, record, state);
		return terms.every((term) => text.includes(term));
	});
}

export function localCatalogFindRecordings(
	recordings: readonly LocalProviderRecord[],
	artists: readonly LocalProviderRecord[],
	query: string,
) {
	const terms = normalize(query).split(" ").filter(Boolean);
	if (!terms.length) return [];
	return recordings
		.map((recording) => localCatalogRecordingWithCredits(recording, artists))
		.filter((recording) => {
			const text = normalize(
				[
					recording.title,
					recording.credits?.map((credit) => credit.creditName).join(" "),
					recording.provider,
				].join(" "),
			);
			return terms.every((term) => text.includes(term));
		});
}

export function localCatalogRecordingWithCredits(
	recording: LocalProviderRecord,
	artists: readonly LocalProviderRecord[],
): LocalProviderRecord {
	const artistIds = recording.artistIds ?? [];
	const names = artistIds.flatMap((artistId) => {
		const name = artists.find((artist) => artist.id === artistId)?.name;
		return name ? [{ artistId, creditName: name }] : [];
	});
	return {
		...recording,
		title: localCatalogRecordingTitle(recording),
		credits: recording.credits?.length
			? recording.credits
			: names.map((entry, index) => ({
					...entry,
					joinPhrase: index + 1 < names.length ? ", " : "",
				})),
	};
}

export function localCatalogFindArtists(
	artists: readonly LocalProviderRecord[],
	query: string,
) {
	const terms = normalize(query).split(" ").filter(Boolean);
	return artists.filter((artist) => {
		const text = normalize(
			[artist.name, artist.sortName, artist.provider].join(" "),
		);
		return terms.every((term) => text.includes(term));
	});
}

export const localCatalogPageCount = (totalItems: number) =>
	Math.max(1, Math.ceil(totalItems / LOCAL_CATALOG_PAGE_SIZE));

export function localCatalogPageSlice<T>(
	items: readonly T[],
	page: number,
): T[] {
	const start = (Math.max(1, page) - 1) * LOCAL_CATALOG_PAGE_SIZE;
	return items.slice(start, start + LOCAL_CATALOG_PAGE_SIZE);
}

function metadataFirst(record: LocalProviderRecord | undefined, key: string) {
	return record?.metadataValues?.[key]?.[0];
}

const firstDefined = (
	record: LocalProviderRecord | undefined,
	field: keyof LocalProviderRecord,
	metadataKey: string,
) => {
	const value = record?.[field];
	return typeof value === "string"
		? value
		: (metadataFirst(record, metadataKey) ?? "");
};

export function releaseRecordToFormValues(
	record: LocalProviderRecord,
): LocalReleaseFormInitialValues {
	const editionTitle = localCatalogReleaseTitle(record);
	const groupTitle = record.release_group_title || editionTitle;
	const releaseTitle = editionTitle !== groupTitle ? editionTitle : "";
	return {
		title: groupTitle,
		edition: releaseTitle,
		artistIds: [...(record.artistIds ?? [])],
		primaryType: record.primaryType ?? "",
		secondaryTypes: record.secondaryTypes ?? [],
		language: record.language ?? "",
		script: firstDefined(record, "script", "script"),
		editionDetails: {
			release_date: firstDefined(record, "release_date", "releasedate"),
			country: firstDefined(record, "country_code", "releasecountry"),
			format: firstDefined(record, "format", "media"),
			label: firstDefined(record, "label", "recordlabel"),
			catalog_number: firstDefined(record, "catalog_number", "catalognumber"),
			disambiguation: firstDefined(record, "disambiguation", "albumversion"),
		},
		genres: (record.genres ?? []).map((id) => ({
			id,
			name: record.genreNamesById?.[id] ?? id,
			source_name: record.genreNamesById?.[id] ?? id,
		})),
		tracks: (record.tracks ?? []).map((track) => ({
			...structuredClone(track),
			id: track.id || crypto.randomUUID(),
			durationInput: formatLocalDuration(track.durationOverrideMs),
		})),
	};
}

function splitList(value: string) {
	return value
		.split(",")
		.map((item) => item.trim())
		.filter(Boolean);
}

const advisory = (value: string) =>
	value === "true" ? "1" : value === "false" ? "2" : "";

export type LocalArtistFormDraft = {
	name: string;
	sortName: string;
	disambiguation: string;
	tags: string;
	genres: GenreReference[];
};

export type LocalRecordingFormDraft = {
	title: string;
	durationInput: string;
	disambiguation: string;
	explicit: string;
	isrcs: string;
	tags: string;
	genres: GenreReference[];
	artistIds: string[];
};

const genreRecordFields = (genres: readonly GenreReference[]) => ({
	genres: genres.map((genre) => genre.id ?? genre.name),
	genreNamesById: Object.fromEntries(
		genres.map((genre) => [genre.id ?? genre.name, genre.name ?? ""]),
	),
});

function syncCredits(
	existing: LocalProviderRecord | undefined,
	artistIds: readonly string[],
	artists: readonly LocalProviderRecord[],
) {
	const same =
		existing?.credits?.length === artistIds.length &&
		existing.credits.every(
			(credit, index) => (credit.artistId ?? "") === artistIds[index],
		);
	if (same) return structuredClone(existing?.credits);
	if (!artistIds.length) return [];
	return artistIds.map((artistId, index) => {
		const previous = existing?.credits?.find(
			(credit) => credit.artistId === artistId,
		);
		const name =
			previous?.creditName ||
			artists.find((artist) => artist.id === artistId)?.name ||
			"";
		return {
			artistId,
			creditName: name,
			joinPhrase: index + 1 < artistIds.length ? ", " : "",
		};
	});
}

function editionMetadata(
	existing: LocalProviderRecord | undefined,
	draft: LocalReleaseDraft,
) {
	const values = structuredClone(existing?.metadataValues ?? {});
	const set = (key: string, value: string | undefined) => {
		if (value) values[key] = [value];
		else delete values[key];
	};
	set("album", draft.title);
	set("releasedate", draft.edition?.release_date);
	set("releasecountry", draft.edition?.country);
	set("media", draft.edition?.format);
	set("recordlabel", draft.edition?.label);
	if (
		!existing ||
		(draft.edition?.catalog_number ?? "") !==
			firstDefined(existing, "catalog_number", "catalognumber")
	) {
		set("catalognumber", draft.edition?.catalog_number);
	}
	set("albumversion", draft.edition?.disambiguation);
	set("script", draft.script);
	return values;
}

export function releaseDraftToRecord(
	draft: LocalReleaseDraft,
	existing: LocalProviderRecord | undefined,
	artists: readonly LocalProviderRecord[],
): LocalProviderRecord {
	const key = existing?.key || crypto.randomUUID();
	return {
		...existing,
		id: existing?.id || `local-${key}`,
		key,
		provider: "Local",
		title: draft.title,
		release_group_title: draft.release_group_title,
		artistIds: [...draft.artistIds],
		primaryType: draft.primaryType ?? "",
		language: draft.language ?? "",
		script: draft.script ?? "",
		release_date: draft.edition?.release_date ?? "",
		country_code: draft.edition?.country ?? "",
		format: draft.edition?.format ?? "",
		label: draft.edition?.label ?? "",
		catalog_number: draft.edition?.catalog_number ?? "",
		disambiguation: draft.edition?.disambiguation ?? "",
		status: existing?.status ?? "",
		packaging: existing?.packaging ?? "",
		barcode: existing?.barcode ?? "",
		secondaryTypes: draft.secondaryTypes ?? existing?.secondaryTypes ?? [],
		...genreRecordFields(draft.genres),
		credits: syncCredits(existing, draft.artistIds, artists),
		metadataValues: editionMetadata(existing, draft),
		tracks: draft.tracks.map((track) => ({
			...existing?.tracks?.find((item) => item.id === track.id),
			id: track.id,
			recordingId: track.recordingId,
			titleOverride: track.titleOverride,
			durationOverrideMs: track.durationOverrideMs,
		})),
	};
}

export function artistDraftToRecord(
	draft: LocalArtistFormDraft,
	existing: LocalProviderRecord | undefined,
): LocalProviderRecord {
	const key = existing?.key || crypto.randomUUID();
	return {
		...existing,
		id: existing?.id || `local-${key}`,
		key,
		provider: "Local",
		name: draft.name,
		sortName: draft.sortName,
		disambiguation: draft.disambiguation,
		tags: draft.tags,
		...genreRecordFields(draft.genres),
	};
}

export function recordingDraftToRecord(
	draft: LocalRecordingFormDraft,
	existing: LocalProviderRecord | undefined,
	artists: readonly LocalProviderRecord[],
): LocalProviderRecord {
	const key = existing?.key || crypto.randomUUID();
	const metadata = structuredClone(existing?.metadataValues ?? {});
	const title = draft.title;
	const isrcs = splitList(draft.isrcs);
	const explicit = advisory(draft.explicit);
	const set = (metadataKey: string, values: string[]) => {
		if (values.some(Boolean)) metadata[metadataKey] = values;
		else delete metadata[metadataKey];
	};
	set("title", title ? [title] : []);
	set("isrc", isrcs);
	set("explicitstatus", explicit ? [explicit] : []);
	return {
		...existing,
		id: existing?.id || `local-${key}`,
		key,
		provider: "Local",
		title,
		artistIds: [...draft.artistIds],
		duration_ms:
			existing &&
			draft.durationInput === formatLocalDuration(existing.duration_ms)
				? existing.duration_ms
				: parseLocalDuration(draft.durationInput),
		disambiguation: draft.disambiguation,
		explicit: draft.explicit,
		isrcs: draft.isrcs,
		tags: draft.tags,
		credits: syncCredits(existing, draft.artistIds, artists),
		metadataValues: metadata,
		...genreRecordFields(draft.genres),
	};
}
