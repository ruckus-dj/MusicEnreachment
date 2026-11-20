import {
	type CatalogArtist,
	effectivePublicationTags,
	metadataChanged,
	type OriginalRelease,
	type OriginalTrack,
	originalArtistName,
	publicationMetadataIssues,
	type SourceFiles,
} from "../library/catalogData";
import {
	choosePublicationOutput,
	type PublicationDestination,
} from "./publicationOutput";

export type DemoPublicationMode = "ordinary" | "forced";
type Disposition =
	| "create"
	| "update"
	| "unchanged"
	| "skipped"
	| "unprocessable"
	| "blocked";

export interface DemoPublicationCatalog {
	releases: readonly OriginalRelease[];
	files: SourceFiles;
	artists: CatalogArtist[];
	destination: PublicationDestination;
	/** The standalone demo only implements the fixture's destination projection. */
	hasCustomTemplate?: boolean;
	inventoryVersion: number;
}

export interface DemoPublicationPosition {
	release_id: string;
	position_id: string;
}

export interface DemoPublicationRow extends DemoPublicationPosition {
	mode: DemoPublicationMode;
	disposition: Disposition;
	reason?: string;
	resolved_path?: string;
	path_clash: boolean;
	downgrade: boolean;
	source?: SourceFiles[string];
	renderedPath?: string;
	current: OriginalTrack["published"];
	output?: NonNullable<OriginalTrack["published"]>;
	evidence?: string;
}

/** Compare exact metadata values, without depending on object key order. */
function canonical(value: unknown): string {
	if (Array.isArray(value)) return `[${value.map(canonical).join(",")}]`;
	if (value && typeof value === "object")
		return `{${Object.entries(value)
			.sort(([a], [b]) => (a < b ? -1 : a === b ? 0 : 1))
			.map(([key, item]) => `${JSON.stringify(key)}:${canonical(item)}`)
			.join(",")}}`;
	return JSON.stringify(value) ?? "null";
}

function planPosition(
	catalog: DemoPublicationCatalog,
	position: DemoPublicationPosition,
	mode: DemoPublicationMode,
): DemoPublicationRow {
	const row: DemoPublicationRow = {
		...position,
		mode,
		disposition: "skipped",
		path_clash: false,
		downgrade: false,
		current: null,
	};
	const release = catalog.releases.find(({ id }) => id === position.release_id);
	const track = release?.tracks.find(({ id }) => id === position.position_id);
	if (!release || !track) return { ...row, reason: "position_not_found" };

	const current = track.published;
	const file = track.sourceId ? catalog.files[track.sourceId] : undefined;
	const source = file?.available && file.locations?.length ? file : undefined;
	const issues = publicationMetadataIssues(release, track);
	if (issues.length)
		return {
			...row,
			current,
			source,
			disposition: "unprocessable",
			reason: "invalid_metadata",
		};
	if (!source)
		return {
			...row,
			current,
			disposition: current ? "blocked" : "skipped",
			reason: track.sourceId ? "missing_audio" : "source_selection_required",
		};
	if (catalog.hasCustomTemplate)
		return {
			...row,
			current,
			source,
			disposition: "blocked",
			reason: "publication_path_unavailable",
		};

	const target = choosePublicationOutput(source, {
		...catalog.destination,
		artist: originalArtistName(release.artistId, catalog.artists),
		year: release.year,
		release: release.title,
		position: track.position,
		title: track.title,
	});
	if (!target.container || (current && current.container !== target.container))
		return {
			...row,
			current,
			source,
			disposition: "blocked",
			reason: "publication_format_unavailable",
		};
	const path = current?.path ?? target.path;
	if (publicationPathOwned(catalog, position, path))
		return {
			...row,
			current,
			source,
			disposition: "blocked",
			reason: "resolved_path_unavailable",
		};
	const output = {
		...current,
		path,
		container: target.container,
		codec: source.codec,
		sourceId: source.id,
		tags: effectivePublicationTags(
			release,
			track,
			catalog.artists,
			catalog.files,
		),
		coverHash: track.coverHash ?? null,
		lyrics: track.lyrics ?? null,
		readAt: current?.readAt ?? "",
	};
	const unchanged =
		mode === "ordinary" &&
		current?.sourceId === source.id &&
		current.codec === source.codec &&
		!metadataChanged(release, track, catalog.artists) &&
		canonical(current.tags) === canonical(output.tags) &&
		(current.coverHash ?? null) === output.coverHash &&
		(current.lyrics ?? null) === output.lyrics &&
		current.path === target.path;
	const currentSource = current?.sourceId
		? catalog.files[current.sourceId]
		: undefined;
	const currentBitrate =
		currentSource?.codec === current?.codec
			? currentSource?.bitrate
			: undefined;
	const downgrade =
		(current?.codec === "FLAC" && source.codec === "MP3") ||
		(current?.codec === "MP3" &&
			source.codec === "MP3" &&
			Boolean(
				currentBitrate && source.bitrate && source.bitrate < currentBitrate,
			));
	return {
		...row,
		current,
		source,
		output,
		disposition: unchanged ? "unchanged" : current ? "update" : "create",
		resolved_path: output.path,
		renderedPath: target.path,
		downgrade,
		evidence: canonical({
			mode,
			inventoryVersion: catalog.inventoryVersion,
			destination: catalog.destination,
			source,
			output,
			current,
			target,
			downgrade,
		}),
	};
}

export function planDemoPublication(
	catalog: DemoPublicationCatalog,
	positions: readonly DemoPublicationPosition[],
	mode: DemoPublicationMode = "ordinary",
): DemoPublicationRow[] {
	const rows = positions.map((position) =>
		planPosition(catalog, position, mode),
	);
	const paths = new Map<string, number>();
	for (const row of rows) {
		if (isReadyPublication(row) && row.resolved_path)
			paths.set(row.resolved_path, (paths.get(row.resolved_path) ?? 0) + 1);
	}
	return rows.map((row) => ({
		...row,
		path_clash: Boolean(
			isReadyPublication(row) &&
				row.resolved_path &&
				(paths.get(row.resolved_path) ?? 0) > 1,
		),
	}));
}

export function isReadyPublication(row: DemoPublicationRow) {
	return row.disposition === "create" || row.disposition === "update";
}

function publicationPathOwned(
	catalog: DemoPublicationCatalog,
	position: DemoPublicationPosition,
	path: string,
) {
	return catalog.releases.some((release) =>
		release.tracks.some(
			(track) =>
				(release.id !== position.release_id ||
					track.id !== position.position_id) &&
				track.published?.path === path,
		),
	);
}

/** Validate and settle this position only; sibling successes are not freshness inputs. */
export function settleDemoPublication(
	catalog: DemoPublicationCatalog,
	prepared: DemoPublicationRow,
): OriginalTrack["published"] {
	if (!isReadyPublication(prepared) || !prepared.evidence) return null;
	const live = planPosition(
		catalog,
		{ release_id: prepared.release_id, position_id: prepared.position_id },
		prepared.mode,
	);
	if (
		!isReadyPublication(live) ||
		live.evidence !== prepared.evidence ||
		!live.output
	)
		return null;
	return { ...live.output, readAt: new Date().toISOString() };
}
