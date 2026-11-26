import type {
	ArtistCreditDraft,
	ArtistCreditRow,
} from "../../../../ui/features/library/metadata/artistCreditDraft";
import type {
	CatalogArtist,
	OriginalRelease,
	OriginalTrack,
} from "../catalogData";
import type { ArtistProjectionCredit } from "./artistProjection";
import type { MetadataValues } from "./projection";

type CreditRow = ArtistProjectionCredit & { role?: string };

export type ScopedArtistCredit =
	| { state: "absent" | "explicit_empty" }
	| { state: "value"; rows: CreditRow[] };

/** Accepted position evidence, independent of the manually edited desired key. */
export type PositionCreditEvidence =
	| { origin: "manual_matching" | "provider_position"; rows: CreditRow[] }
	| { origin: "recording"; available: boolean };

export function scopedCreditValues(intent: ScopedArtistCredit): CreditRow[] {
	return intent.state === "value" ? intent.rows : [];
}

export function knownArtistCredit(artist?: CatalogArtist): CreditRow[] {
	return artist
		? [{ artistId: artist.id, creditName: artist.name, joinPhrase: "" }]
		: [];
}

export function releaseCreditBaseline(
	release: OriginalRelease,
	artists: readonly CatalogArtist[],
): CreditRow[] {
	return release.credits
		? release.credits.map((credit) => ({
				artistId: credit.artistId ?? "",
				creditName: credit.credit ?? credit.name ?? "",
				joinPhrase: credit.joinPhrase ?? "",
				role: credit.role,
			}))
		: knownArtistCredit(artists.find(({ id }) => id === release.artistId));
}

export function recordingCreditBaseline(
	track: OriginalTrack,
	artists: readonly CatalogArtist[],
): CreditRow[] {
	return (
		track.recordingCredits ??
		knownArtistCredit(artists.find(({ id }) => id === track.artistId))
	);
}

export function recordingCreditValues(
	track: OriginalTrack,
	artists: readonly CatalogArtist[],
) {
	return track.recordingArtistCredit
		? scopedCreditValues(track.recordingArtistCredit)
		: recordingCreditBaseline(track, artists);
}

export function positionCreditValues(
	track: OriginalTrack,
	artists: readonly CatalogArtist[],
) {
	if (track.positionArtistCredit)
		return scopedCreditValues(track.positionArtistCredit);
	const evidence = track.positionCreditEvidence;
	if (evidence?.origin === "recording")
		return evidence.available ? recordingCreditValues(track, artists) : [];
	if (evidence) return evidence.rows;
	// Legacy fixture projection is not proof of recording inheritance.
	return recordingCreditBaseline(track, artists);
}

export function releaseCreditValues(
	release: OriginalRelease,
	artists: readonly CatalogArtist[],
) {
	return release.releaseArtistCredit
		? scopedCreditValues(release.releaseArtistCredit)
		: releaseCreditBaseline(release, artists);
}

function displayRows(
	credits: readonly CreditRow[],
	artists: readonly CatalogArtist[],
	prefix: string,
): ArtistCreditRow[] {
	return credits.map((credit, index) => {
		const artist = artists.find(({ id }) => id === credit.artistId);
		return {
			clientKey: `${prefix}-${index}`,
			id: credit.artistId,
			name: artist?.name ?? credit.creditName,
			credit_name: credit.creditName,
			join_phrase: credit.joinPhrase ?? "",
			role: credit.role,
			disambiguation: artist?.disambiguation,
			sortName: artist?.sortName,
			hasMbid: Boolean(artist?.mbid),
		};
	});
}

export function scopedArtistCreditEditor(
	intent: ScopedArtistCredit | undefined,
	creditedArtists: readonly CreditRow[],
	artists: readonly CatalogArtist[],
	scope: "release" | "recording" | "position",
	label: string,
	evidence?: PositionCreditEvidence,
) {
	const saved: ArtistCreditDraft = {
		hasDesiredOverride: intent !== undefined,
		state: intent?.state ?? "absent",
		rows: intent
			? displayRows(scopedCreditValues(intent), artists, "saved")
			: [],
	};
	// Relational baseline rows alone do not classify the provider position.
	const effective =
		scope !== "position" || intent !== undefined
			? { status: "resolved" as const, origin: scope }
			: evidence?.origin === "recording" && !evidence.available
				? { status: "unavailable" as const, origin: null }
				: evidence
					? { status: "resolved" as const, origin: evidence.origin }
					: { status: "unclassified" as const, origin: null };
	return {
		label,
		saved,
		baseline: displayRows(creditedArtists, artists, "baseline"),
		effective,
		search: (input: string) => {
			const query = input.trim().toLowerCase();
			return query
				? artists
						.filter(({ name }) => name.toLowerCase().includes(query))
						.slice(0, 20)
						.map(({ id, name, disambiguation }) => ({
							id,
							name,
							disambiguation,
						}))
				: [];
		},
	};
}

export function saveScopedArtistCredit(
	draft: ArtistCreditDraft,
): ScopedArtistCredit | undefined {
	if (!draft.hasDesiredOverride) return undefined;
	if (draft.state !== "value") return { state: draft.state };
	return {
		state: "value",
		rows: draft.rows.map((row) => ({
			artistId: row.id,
			creditName: row.credit_name,
			joinPhrase: row.join_phrase,
			...(row.role !== undefined ? { role: row.role } : {}),
		})),
	};
}

export function creditTagKeys(context: "ARTIST" | "ALBUMARTIST") {
	return [
		context,
		`${context}S`,
		`MUSICBRAINZ_${context}ID`,
		`${context}SORT`,
		`${context}SSORT`,
	];
}

export function scopedCreditComparisonTags(
	intent: ScopedArtistCredit,
	artists: readonly CatalogArtist[],
	context: "ARTIST" | "ALBUMARTIST",
): MetadataValues {
	const idsTag = `MUSICBRAINZ_${context}ID`;
	if (intent.state === "explicit_empty")
		return Object.fromEntries(creditTagKeys(context).map((key) => [key, []]));
	const rows = scopedCreditValues(intent);
	if (!rows.length) return {};
	const tags: MetadataValues = {
		[context]: [
			rows
				.map(({ creditName, joinPhrase }) => `${creditName}${joinPhrase ?? ""}`)
				.join(""),
		],
		[`${context}S`]: rows.map(
			({ artistId, creditName }) =>
				artists.find(({ id }) => id === artistId)?.name || creditName,
		),
	};
	const ids = rows.map(
		({ artistId }) => artists.find(({ id }) => id === artistId)?.mbid ?? "",
	);
	if (ids.every(Boolean)) tags[idsTag] = ids;
	return tags;
}
