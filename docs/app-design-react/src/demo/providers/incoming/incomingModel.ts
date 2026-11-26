import fixtureData from "../../fixtures/original-fixtures.json";
import {
	type CatalogArtist,
	type SourceFile as CatalogSourceFile,
	desiredTags,
	effectiveReleaseTitle,
	effectiveReleaseYear,
	type OriginalRelease,
	type OriginalTrack,
	originalArtistName,
	originalArtists,
	originalFiles,
	originalProviders,
	originalReleases,
	publicationMetadataIssues,
} from "../library/catalogData";
import { replaceAudioMeasurements } from "../library/metadata/audioMeasurements";
import { sameTagValues } from "../library/tagEquality";
import type { UiNotice } from "../notices/uiNotice";

export type TagMap = Record<string, string[]>;
export type SourceFile = CatalogSourceFile & {
	confidence?: string | null;
	fingerprint?: string;
	providerTrackId?: string | null;
};
export type IncomingFiles = Record<string, SourceFile>;
export type IncomingReleases = OriginalRelease[];
export type IncomingReason = {
	code: "incomplete" | "confident" | "compilation";
	params?: Record<string, string | number>;
};
export type IncomingNotice = {
	code: "automaticPublished" | "nothingToPublish";
	params?: Record<string, string | number>;
};
export type Assignment = {
	sourceIds: string[];
	selectedSourceId: string | null;
	sourceMeta: Record<string, { method: string; confidence: string | null }>;
	method: string;
	confidence: string | null;
	providerTrackId: string | null;
	included: boolean;
	fileId?: string | null;
};
export type IncomingGroup = {
	id: string;
	title: string;
	artistId: string;
	releaseId: string;
	fileIds: string[];
	candidateId: string | null;
	mode: "local" | "provider";
	autoEligible: boolean;
	processed: boolean;
	reason: IncomingReason | string;
	drafts: Record<string, Record<string, Assignment>>;
};
/** One exact position included in a saved matching result. */
export type MatchingLinkReceiptPosition = {
	trackId: string;
	title: string;
	position: number;
	displayedNumber?: string;
	sourceIds: string[];
	selectedSourceId: string | null;
	included: boolean;
};
/**
 * Saved result of an explicit link save. The receipt is domain state owned by
 * the data provider; presentation code only maps it to a typed view.
 */
export type MatchingLinkReceipt = {
	id: string;
	groupId: string;
	groupTitle: string;
	releaseId: string;
	releaseTitle: string;
	providerReleaseId: string | null;
	positions: MatchingLinkReceiptPosition[];
	remainingFileIds: string[];
	remainingFileNames: string[];
};
export type IncomingState = {
	groups: IncomingGroup[];
	groupId: string;
	selectedFile: string | null;
	selectedTarget: string | null;
	providerQuery: string;
	providerResults: IncomingReleases | null;
	recordingQuery: string;
	recordingResults: { release: OriginalRelease; track: OriginalTrack }[] | null;
	recordingTarget: string | null;
	notice: string;
	noticeDescriptor: IncomingNotice | null;
	uiNotice?: UiNotice | null;
	inventoryVersion: number;
	plan: IncomingPlan | null;
	confirmDowngrade: boolean;
	receipts: MatchingLinkReceipt[];
};

const fixtures = fixtureData as unknown as {
	artists: { id: string; name: string }[];
	incomingGroups: IncomingGroup[];
};

export const incomingFixtures = {
	...fixtures,
	artists: originalArtists,
	files: originalFiles as IncomingFiles,
	releases: originalReleases,
	providerReleases: originalProviders,
};

function reasonForGroup(group: IncomingGroup): IncomingReason {
	if (group.mode === "local") return { code: "compilation" };
	if (group.autoEligible) {
		const count = group.fileIds.length;
		return { code: "confident", params: { count, total: count } };
	}
	return { code: "incomplete" };
}

export const artistName = (
	artistId: string,
	artists: CatalogArtist[] = fixtures.artists,
) => artists.find((artist) => artist.id === artistId)?.name ?? "";
export const providerReleases: IncomingReleases = originalProviders;
export const normalized = (value: unknown) =>
	String(value ?? "")
		.normalize("NFKC")
		.toLocaleLowerCase()
		.replace(/\s+/g, " ")
		.trim();
export const sourceIds = (assignment?: Partial<Assignment> | null) =>
	assignment?.sourceIds ?? (assignment?.fileId ? [assignment.fileId] : []);
export const selectedSourceId = (assignment?: Partial<Assignment> | null) =>
	assignment && Object.hasOwn(assignment, "selectedSourceId")
		? (assignment.selectedSourceId ?? null)
		: (assignment?.fileId ?? null);
export const usableFile = (file?: SourceFile) =>
	Boolean(file?.available && file.locations?.length);

export function createIncomingState(): IncomingState {
	return {
		groups: structuredClone(fixtures.incomingGroups).map((group) => ({
			...group,
			reason: reasonForGroup(group),
		})),
		groupId: "north",
		selectedFile: null,
		selectedTarget: null,
		providerQuery: "Northern Lines",
		providerResults: null,
		recordingQuery: "",
		recordingResults: null,
		recordingTarget: null,
		notice: "",
		noticeDescriptor: null,
		uiNotice: null,
		inventoryVersion: 0,
		plan: null,
		confirmDowngrade: false,
		receipts: [],
	};
}

export function findTarget(
	group: IncomingGroup,
	releases: IncomingReleases,
	providers: IncomingReleases = providerReleases,
): OriginalRelease | null {
	if (group.mode === "local")
		return (
			providers.find(
				(release) =>
					release.provider === "Local" && release.id === group.releaseId,
			) ??
			releases.find((release) => release.id === group.releaseId) ??
			null
		);
	return providers.find((release) => release.id === group.candidateId) ?? null;
}

/** Stable per-edition draft identity without re-resolving a target. */
export function matchingDraftKey(
	mode: IncomingGroup["mode"],
	targetId: string | null | undefined,
) {
	const id = targetId == null ? "" : String(targetId);
	return mode === "local" ? `local:${id}` : id;
}

export function getDraft(
	group: IncomingGroup,
	files: IncomingFiles,
	providers: IncomingReleases = providerReleases,
	releases: IncomingReleases = originalReleases,
	resolvedTarget?: OriginalRelease | null,
) {
	const target = resolvedTarget ?? findTarget(group, releases, providers);
	if (!target) return {};
	const targetTracks = target.tracks;
	const key = matchingDraftKey(group.mode, target.id);
	if (!Object.hasOwn(group.drafts, key)) {
		const assignments: Record<string, Assignment> = {};
		const localRelease =
			group.mode === "local" && target.providerId
				? releases.find(
						(release) =>
							release.provider === "Local" &&
							release.providerId === target.providerId,
					)
				: undefined;
		for (const track of targetTracks) {
			const linkedTrack = localRelease?.tracks.find(
				(candidate) => candidate.localProviderTrackId === track.id,
			);
			const source = group.fileIds
				.map((id) => files[id])
				.find(
					(file) =>
						file &&
						(group.mode === "local"
							? file.id === (linkedTrack?.sourceId ?? track.sourceId)
							: file.providerTrackId === track.id &&
								file.confidence === "high"),
				);
			if (!source) continue;
			const method =
				group.mode === "local"
					? "Из исходной группы"
					: "Авто · тестовый вердикт";
			assignments[String(track.id)] = {
				sourceIds: [source.id],
				selectedSourceId: source.id,
				sourceMeta: {
					[source.id]: { method, confidence: source.confidence ?? null },
				},
				method,
				confidence: source.confidence ?? null,
				providerTrackId:
					group.mode === "local"
						? (linkedTrack?.providerTrackId ?? track.providerTrackId ?? null)
						: String(track.id),
				included: true,
			};
		}
		group.drafts[key] = assignments;
	}
	return group.drafts[key];
}

export function assignFile(
	group: IncomingGroup,
	targetId: string,
	fileId: string,
	files: IncomingFiles,
	releases: IncomingReleases = originalReleases,
	providers: IncomingReleases = providerReleases,
	resolvedTarget?: OriginalRelease | null,
) {
	if (!group.fileIds.includes(fileId) || !usableFile(files[fileId]))
		return false;
	const target = resolvedTarget ?? findTarget(group, releases, providers);
	if (!target?.tracks.some((track) => track.id === targetId)) return false;
	const assignments = getDraft(group, files, providers, releases, target);
	const old = assignments[targetId];
	if (sourceIds(old).includes(fileId)) return false;
	const assignment = old ?? {
		sourceIds: [],
		selectedSourceId: null,
		sourceMeta: {},
		method: "Вручную · перетаскивание или выбор",
		confidence: null,
		providerTrackId: group.mode === "local" ? null : targetId,
		included: false,
	};
	for (const id of assignment.sourceIds)
		assignment.sourceMeta[id] ??= {
			method: assignment.method,
			confidence: files[id]?.confidence ?? null,
		};
	assignment.sourceMeta[fileId] = {
		method: "Вручную · добавлен вариант",
		confidence: files[fileId].confidence ?? null,
	};
	assignment.sourceIds = [...assignment.sourceIds, fileId];
	assignments[targetId] = assignment;
	return true;
}

export function unassignFile(
	assignmentMap: Record<string, Assignment>,
	targetId: string,
	fileId: string,
) {
	const assignment = assignmentMap[targetId];
	if (!assignment) return;
	assignment.sourceIds = assignment.sourceIds.filter((id) => id !== fileId);
	if (assignment.selectedSourceId === fileId)
		assignment.selectedSourceId = null;
	delete assignment.sourceMeta[fileId];
	if (!assignment.sourceIds.length) delete assignmentMap[targetId];
}

export function providerTags(
	release: OriginalRelease,
	track: OriginalTrack,
	artists?: CatalogArtist[],
	audioSourceId: string | null = track.sourceId,
): TagMap {
	return desiredTags(release, track, artists, audioSourceId);
}

export function findProviderTrack(
	id: string | null,
	providers: IncomingReleases = providerReleases,
) {
	if (!id) return null;
	for (const release of providers) {
		const track = release.tracks.find((item) => item.id === id);
		if (track) return { release, track };
	}
	return null;
}

export type PlanRow = {
	targetTrack: OriginalTrack;
	assignment?: Assignment;
	track?: OriginalTrack;
	file?: SourceFile;
	outputCodec?: string;
	provider: ReturnType<typeof findProviderTrack>;
	nextTags: TagMap;
	previous?: NonNullable<OriginalTrack["published"]>;
	action: "Пропустить" | "Без изменений" | "Заменить" | "Создать";
	downgrade: boolean;
};

/**
 * Complete single-position row. `ordinaryUnchanged` additionally proves the
 * recorded CURRENT path, container, assets and lyrics, so metadata/cover edits
 * are not hidden by the generic source/codec/tag gate.
 */
export type PositionPlanRow = PlanRow & {
	/** Path rendered from current settings, independent of retained target. */
	renderedPath?: string;
	renderedContainer?: string;
	/** Path retained from CURRENT; absent outside retained workflow. */
	retainedPath?: string;
	ordinaryUnchanged: boolean;
};
export type IncomingPlan = {
	mode: IncomingGroup["mode"];
	groupId: string;
	target: OriginalRelease;
	localReleaseId: string | null;
	assignments: Record<string, Assignment>;
	inventoryVersion: number;
	/** Exact single-position context; a one-row group is still a group plan. */
	positionId?: string;
	/** Rebuild the selected source even when the current snapshot is unchanged. */
	force?: boolean;
	/** Retained-position workflow replaces the output without changing its path. */
	retainCurrentPath?: boolean;
};

export function makePlan(
	state: IncomingState,
	files: IncomingFiles,
	releases: IncomingReleases,
	providers: IncomingReleases = providerReleases,
): IncomingPlan | null {
	const group = state.groups.find((item) => item.id === state.groupId);
	return group ? makeGroupPlan(state, group, files, releases, providers) : null;
}

export function makeGroupPlan(
	state: IncomingState,
	group: IncomingGroup,
	files: IncomingFiles,
	releases: IncomingReleases,
	providers: IncomingReleases = providerReleases,
): IncomingPlan | null {
	const target = findTarget(group, releases, providers);
	if (!target) return null;
	const local =
		group.mode === "local"
			? target
			: releases.find(
					(release) => release.providerId === (target.providerId ?? target.id),
				);
	return {
		mode: group.mode,
		groupId: group.id,
		target: structuredClone(target),
		localReleaseId: local ? String(local.id) : null,
		assignments: structuredClone(getDraft(group, files, providers, releases)),
		inventoryVersion: state.inventoryVersion,
	};
}

export function makeReleasePlan(
	state: IncomingState,
	release: OriginalRelease,
	files: IncomingFiles,
	trackId: string | null = null,
): IncomingPlan {
	const assignments: Record<string, Assignment> = {};
	for (const track of release.tracks) {
		const selectedSource = track.sourceId;
		const file = selectedSource ? files[selectedSource] : undefined;
		if (!selectedSource || !usableFile(file)) continue;
		const availableSources = [
			...new Set([...(track.sourceIds ?? []), selectedSource]),
		].filter((id) => usableFile(files[id]));
		assignments[String(track.id)] = {
			sourceIds: availableSources,
			selectedSourceId: selectedSource,
			sourceMeta: {},
			method: "Текущий выбранный источник",
			confidence: null,
			providerTrackId: track.providerTrackId ?? null,
			included: !trackId || track.id === trackId,
		};
	}
	return {
		mode: "local",
		groupId: "",
		target: structuredClone(release),
		localReleaseId: release.id,
		assignments,
		inventoryVersion: state.inventoryVersion,
		...(trackId ? { positionId: trackId } : {}),
	};
}

export function planRows(
	plan: IncomingPlan,
	files: IncomingFiles,
	releases: IncomingReleases,
	providers: IncomingReleases = providerReleases,
	artists?: CatalogArtist[],
	destination: PublicationDestination = fixturePublicationDestination,
): PlanRow[] {
	const existing = releases.find(
		(release) => release.id === plan.localReleaseId,
	);
	const targetTracks = plan.target.tracks;
	return targetTracks.map((targetTrack) => {
		const assignment = plan.assignments[String(targetTrack.id)];
		const track = existing?.tracks.find((item) =>
			plan.positionId && plan.mode === "local"
				? item.id === targetTrack.id
				: item.position === targetTrack.position,
		);
		const candidate = assignment && files[selectedSourceId(assignment) ?? ""];
		const file = usableFile(candidate) ? candidate : undefined;
		const provider = findProviderTrack(
			assignment?.providerTrackId ?? null,
			providers,
		);
		const releaseForTags =
			plan.mode === "local" ? (existing ?? plan.target) : plan.target;
		const projected =
			plan.mode === "local"
				? {
						...(track ?? targetTrack),
						recordingId:
							provider?.track.recordingId || track?.recordingId || null,
					}
				: { ...targetTrack, sourceId: file?.id ?? null };
		const nextTags = providerTags(
			releaseForTags,
			projected,
			artists,
			selectedSourceId(assignment),
		);
		const previous = track?.published ?? undefined;
		const outputCodec = file
			? demoOutputCodec(file.codec, destination.format)
			: undefined;
		const unchanged =
			previous &&
			file &&
			previous.sourceId === file.id &&
			previous.codec === outputCodec &&
			JSON.stringify(previous.tags) === JSON.stringify(nextTags);
		return {
			targetTrack,
			assignment,
			track,
			file,
			outputCodec,
			provider,
			nextTags,
			previous,
			action:
				!assignment?.included || !file
					? "Пропустить"
					: unchanged && !plan.force
						? "Без изменений"
						: previous
							? "Заменить"
							: "Создать",
			downgrade: Boolean(
				demoIsLosslessCodec(previous?.codec) &&
					file &&
					!demoIsLosslessCodec(file.codec) &&
					assignment?.included,
			),
		};
	});
}

import {
	choosePublicationOutput,
	demoIsLosslessCodec,
	demoOutputCodec,
	demoSourceContainer,
	fixturePublicationDestination,
	type PublicationDestination,
	retainedOutputIssue,
} from "../publication/publicationOutput";

/**
 * Targeted single-position projection. The generic plan gate proves only
 * source, codec and tag equality; a contextual position must also prove the
 * recorded CURRENT path, container, assets and lyrics before calling the row
 * unchanged. Retaining the destination does not make the rendered path equal
 * to CURRENT.
 */
export function planPositionRow(
	plan: IncomingPlan,
	files: IncomingFiles,
	releases: IncomingReleases,
	providers: IncomingReleases = providerReleases,
	artists?: CatalogArtist[],
	destination: PublicationDestination = fixturePublicationDestination,
	positionId: string | undefined = plan.positionId,
): PositionPlanRow | null {
	const target = plan.target.tracks.find((track) => track.id === positionId);
	if (!target || plan.mode !== "local" || plan.groupId) return null;
	const row = planRows(
		{ ...plan, positionId, target: { ...plan.target, tracks: [target] } },
		files,
		releases,
		providers,
		artists,
		destination,
	)[0];
	if (!row) return null;
	const release =
		releases.find((item) => item.id === plan.localReleaseId) ?? plan.target;
	const output = row.file
		? choosePublicationOutput(row.file, {
				...destination,
				artist: originalArtistName(release.artistId, artists),
				year: effectiveReleaseYear(release),
				release: effectiveReleaseTitle(release),
				position: row.targetTrack.position,
				title: row.targetTrack.title,
			})
		: undefined;
	const plannedPath =
		plan.retainCurrentPath && row.previous ? row.previous.path : output?.path;
	const plannedContainer =
		plan.retainCurrentPath && row.previous
			? row.previous.container
			: output?.container;
	const renderedPath = output?.path;
	const renderedContainer = output?.container;
	const retainedPath = plan.retainCurrentPath ? row.previous?.path : undefined;
	const copyCompatibleSource = Boolean(
		row.file &&
			row.outputCodec &&
			renderedContainer &&
			row.file.codec.toUpperCase() === row.outputCodec.toUpperCase() &&
			demoSourceContainer(row.file) === renderedContainer,
	);
	const tagsUnchanged = sameTagValues(row.previous?.tags, row.nextTags);
	const assetsUnchanged =
		(row.previous?.coverHash ?? null) === (row.track?.coverHash ?? null) &&
		(row.previous?.lyrics ?? null) === (row.track?.lyrics ?? null);
	const ordinaryUnchanged = Boolean(
		row.previous &&
			row.file &&
			row.previous.sourceId === row.file.id &&
			row.previous.path === plannedPath &&
			row.previous.path === renderedPath &&
			row.previous.codec === row.outputCodec &&
			row.previous.container === plannedContainer &&
			row.previous.container === renderedContainer &&
			copyCompatibleSource &&
			tagsUnchanged &&
			assetsUnchanged,
	);
	return {
		...row,
		renderedPath,
		renderedContainer,
		retainedPath,
		ordinaryUnchanged,
		downgrade: Boolean(
			row.previous &&
				row.outputCodec &&
				demoIsLosslessCodec(row.previous.codec) &&
				!demoIsLosslessCodec(row.outputCodec) &&
				row.assignment?.included,
		),
		action:
			!row.assignment?.included || !row.file
				? "Пропустить"
				: ordinaryUnchanged && !plan.force
					? "Без изменений"
					: row.previous
						? "Заменить"
						: "Создать",
	};
}

export function applyPlan(
	plan: IncomingPlan,
	files: IncomingFiles,
	releases: IncomingReleases,
	providers: IncomingReleases = providerReleases,
	automatic = false,
	artists?: CatalogArtist[],
	destination: PublicationDestination = fixturePublicationDestination,
	positionId?: string,
) {
	let release = releases.find((item) => item.id === plan.localReleaseId);
	if (plan.mode === "local" && !release) return 0;
	const rows = positionId
		? [
				planPositionRow(
					plan,
					files,
					releases,
					providers,
					artists,
					destination,
					positionId,
				),
			].filter((row): row is PositionPlanRow => row !== null)
		: planRows(plan, files, releases, providers, artists, destination);
	const hasApplicableRow = rows.some((row) => {
		if (
			!row.assignment?.included ||
			!row.file ||
			(row.action === "Без изменений" && !plan.force)
		)
			return false;
		const metadataRelease =
			plan.mode === "local" ? (release ?? plan.target) : plan.target;
		return !publicationMetadataIssues(
			metadataRelease,
			row.track ?? row.targetTrack,
			selectedSourceId(row.assignment),
		).length;
	});
	if (!release && !hasApplicableRow) return 0;
	if (!release) {
		release = {
			...structuredClone(plan.target),
			id: `import-${plan.target.id}`,
			providerId:
				plan.mode === "provider"
					? (plan.target.providerId ?? plan.target.id)
					: null,
			tracks: plan.target.tracks.map((track) => ({
				...track,
				id: `import-${plan.target.id}-t${track.position}`,
				sourceId: null,
				providerTrackId: null,
				published: null,
			})),
		};
		releases.push(release);
		plan.localReleaseId = String(release.id);
	}
	let changed = 0;
	for (const row of rows) {
		const track = release.tracks.find((item) =>
			positionId
				? item.id === positionId
				: item.position === row.targetTrack.position,
		);
		if (!track) continue;
		if (positionId && row.action === "Без изменений") continue;
		if (
			plan.retainCurrentPath &&
			(!track.published ||
				!row.file ||
				retainedOutputIssue(track.published, row.file, destination))
		)
			continue;
		if (
			row.assignment?.included &&
			row.file &&
			publicationMetadataIssues(
				plan.mode === "local" ? release : plan.target,
				row.track ?? row.targetTrack,
				selectedSourceId(row.assignment),
			).length
		)
			continue;
		const assigned = automatic && row.previous ? [] : sourceIds(row.assignment);
		const usableAssignments = assigned.filter((id) => usableFile(files[id]));
		const existingSources = [
			...new Set([
				...(track.sourceIds ?? []),
				...(track.sourceId ? [track.sourceId] : []),
			]),
		].filter((id) => usableFile(files[id]));
		if (usableAssignments.length)
			track.sourceIds = [
				...new Set([...existingSources, ...usableAssignments]),
			];
		if (
			!row.assignment?.included ||
			!row.file ||
			(row.action === "Без изменений" && !plan.force)
		)
			continue;
		if (
			automatic &&
			row.previous &&
			!(row.previous.codec === "MP3" && row.file.codec === "FLAC")
		)
			continue;
		track.sourceId = selectedSourceId(row.assignment);
		if (!(automatic && row.previous)) {
			track.providerTrackId = row.assignment.providerTrackId;
			if (row.provider) track.recordingId = row.provider.track.recordingId;
		}
		const published = track.published;
		if (automatic && row.previous) {
			const replacementTags = replaceAudioMeasurements(
				row.previous.tags,
				desiredTags(release, track, artists, row.file.id),
			);
			track.published = {
				...row.previous,
				tags: replacementTags,
				codec: row.file.codec,
				sourceId: row.file.id,
				readAt: "Только что · демо",
			};
			track.metadataBaseline = structuredClone(replacementTags);
			changed++;
			continue;
		}
		const output = choosePublicationOutput(row.file, {
			...destination,
			artist: originalArtistName(release.artistId, artists),
			year: effectiveReleaseYear(release),
			release: effectiveReleaseTitle(release),
			position: row.targetTrack.position,
			title: row.targetTrack.title,
		});
		const rebuildSource =
			!published ||
			plan.force ||
			published.sourceId !== row.file.id ||
			published.codec !== demoOutputCodec(row.file.codec, destination.format);
		track.published = {
			tags: structuredClone(row.nextTags),
			coverHash: track.coverHash ?? null,
			lyrics: track.lyrics ?? null,
			codec: demoOutputCodec(row.file.codec, destination.format),
			container:
				rebuildSource || !published ? output.container : published.container,
			sourceId: row.file.id,
			readAt: "Только что · демо",
			path:
				published && plan.retainCurrentPath
					? published.path
					: rebuildSource || !published
						? output.path
						: published.path,
		};
		track.metadataBaseline = structuredClone(row.nextTags);
		track.metadataBaselineExtras = {
			coverHash: track.coverHash ?? null,
			lyrics: track.lyrics ?? null,
		};
		changed++;
	}
	return changed;
}

export function runAutomaticMatching(
	state: IncomingState,
	files: IncomingFiles,
	releases: IncomingReleases,
	providers: IncomingReleases = providerReleases,
	artists?: CatalogArtist[],
	destination: PublicationDestination = fixturePublicationDestination,
) {
	let count = 0;
	for (const group of state.groups) {
		const assignments = getDraft(group, files, providers, releases);
		const target = findTarget(group, releases, providers);
		if (
			!target ||
			!group.autoEligible ||
			group.processed ||
			group.mode !== "provider" ||
			target.tracks.length !== group.fileIds.length ||
			!target.tracks.every((track) => {
				const assignment = assignments[track.id];
				const selected = selectedSourceId(assignment);
				const method = selected
					? assignment?.sourceMeta[selected]?.method || assignment?.method || ""
					: "";
				return Boolean(
					selected &&
						usableFile(files[selected]) &&
						!publicationMetadataIssues(target, track, selected).length &&
						files[selected]?.confidence === "high" &&
						assignment?.included &&
						method.startsWith("Авто"),
				);
			})
		)
			continue;
		const plan = makeGroupPlan(state, group, files, releases, providers);
		if (!plan) continue;
		const applied = applyPlan(
			plan,
			files,
			releases,
			providers,
			true,
			artists,
			destination,
		);
		count += applied;
		if (applied > 0) group.processed = true;
	}
	return count;
}

export function removeRootFromIncomingInventory(
	rootId: string,
	state: IncomingState,
	files: IncomingFiles,
	releases: IncomingReleases,
) {
	for (const file of Object.values(files)) {
		file.locations = (file.locations ?? []).filter(
			(location) => location.rootId !== rootId,
		);
		if (!file.locations.length) {
			file.available = false;
			for (const group of state.groups) {
				group.fileIds = group.fileIds.filter((id) => id !== file.id);
				for (const assignments of Object.values(group.drafts))
					for (const assignment of Object.values(assignments)) {
						assignment.sourceIds = assignment.sourceIds.filter(
							(id) => id !== file.id,
						);
						if (assignment.selectedSourceId === file.id)
							assignment.selectedSourceId = null;
						delete assignment.sourceMeta[file.id];
					}
			}
			for (const release of releases)
				for (const track of release.tracks) {
					track.sourceIds = (track.sourceIds ?? []).filter(
						(id) => id !== file.id,
					);
					if (track.sourceId === file.id) track.sourceId = null;
				}
		}
	}
	state.inventoryVersion++;
}
