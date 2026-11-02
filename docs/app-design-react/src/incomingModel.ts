import fixtureData from "./original-fixtures.json";
import {
	desiredTags,
	originalFiles,
	originalProviders,
	originalReleases,
	type OriginalRelease,
	type OriginalTrack,
	type SourceFile as CatalogSourceFile,
} from "./catalogData";

export type TagMap = Record<string, string[]>;
export type SourceFile = CatalogSourceFile & {
	confidence?: string | null;
	fingerprint?: string;
	providerTrackId?: string | null;
};
export type IncomingFiles = Record<string, SourceFile>;
export type IncomingReleases = OriginalRelease[];
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
	reason: string;
	drafts: Record<string, Record<string, Assignment>>;
};
export type IncomingState = {
	groups: IncomingGroup[];
	groupId: string;
	selectedFile: string | null;
	selectedTarget: string | null;
	inspector: "tags" | "evidence";
	tagScope: "artist" | "release" | "track";
	differencesOnly: boolean;
	providerQuery: string;
	providerResults: IncomingReleases | null;
	recordingQuery: string;
	recordingResults: { release: OriginalRelease; track: OriginalTrack }[] | null;
	recordingTarget: string | null;
	notice: string;
	inventoryVersion: number;
	plan: IncomingPlan | null;
	confirmDowngrade: boolean;
};

const fixtures = fixtureData as unknown as {
	artists: { id: string; name: string }[];
	incomingGroups: IncomingGroup[];
};

export const incomingFixtures = {
	...fixtures,
	files: originalFiles as IncomingFiles,
	releases: originalReleases,
	providerReleases: originalProviders,
};
export const artistName = (artistId: string) =>
	String(
		fixtures.artists.find((artist) => artist.id === artistId)?.name ??
			"Артист не указан",
	);
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
		groups: structuredClone(fixtures.incomingGroups),
		groupId: "north",
		selectedFile: null,
		selectedTarget: null,
		inspector: "tags",
		tagScope: "track",
		differencesOnly: false,
		providerQuery: "Northern Lines",
		providerResults: null,
		recordingQuery: "",
		recordingResults: null,
		recordingTarget: null,
		notice: "",
		inventoryVersion: 0,
		plan: null,
		confirmDowngrade: false,
	};
}

export function findTarget(
	group: IncomingGroup,
	releases: IncomingReleases,
	providers: IncomingReleases = providerReleases,
): OriginalRelease | null {
	if (group.mode === "local")
		return releases.find((release) => release.id === group.releaseId) ?? null;
	return providers.find((release) => release.id === group.candidateId) ?? null;
}

export function getDraft(
	group: IncomingGroup,
	files: IncomingFiles,
	providers: IncomingReleases = providerReleases,
	releases: IncomingReleases = originalReleases,
) {
	const target = findTarget(group, releases, providers);
	if (!target) return {};
	const targetTracks = target.tracks;
	const key = group.mode === "local" ? "local" : String(group.candidateId);
	if (!group.drafts[key]) {
		const assignments: Record<string, Assignment> = {};
		for (const track of targetTracks) {
			const source = group.fileIds
				.map((id) => files[id])
				.find(
					(file) =>
						file &&
						(group.mode === "local"
							? file.id === track.sourceId
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
						? (track.providerTrackId ?? null)
						: String(track.id),
				included: true,
			};
		}
		group.drafts[key] = assignments;
	}
	return group.drafts[key];
}

export function getCurrentDraft(
	state: IncomingState,
	files: IncomingFiles,
	providers: IncomingReleases = providerReleases,
	releases: IncomingReleases = originalReleases,
) {
	const group = state.groups.find((item) => item.id === state.groupId);
	return group ? getDraft(group, files, providers, releases) : {};
}

export function assignFile(
	group: IncomingGroup,
	targetId: string,
	fileId: string,
	files: IncomingFiles,
	releases: IncomingReleases = originalReleases,
) {
	if (!group.fileIds.includes(fileId) || !usableFile(files[fileId]))
		return false;
	const target = findTarget(group, releases);
	if (!target?.tracks.some((track) => track.id === targetId)) return false;
	const assignments = getDraft(group, files, providerReleases, releases);
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

const tagSets: Record<string, string[]> = {
	artist: ["ARTIST", "ARTISTSORT", "MUSICBRAINZ_ARTISTID"],
	release: [
		"ALBUM",
		"ALBUMARTIST",
		"DATE",
		"COUNTRY",
		"LABEL",
		"CATALOGNUMBER",
		"MUSICBRAINZ_ALBUMID",
	],
	track: [
		"TITLE",
		"ARTIST",
		"TRACKNUMBER",
		"DISCNUMBER",
		"GENRE",
		"MUSICBRAINZ_TRACKID",
		"COMMENT",
	],
};
export function providerTags(
	release: OriginalRelease,
	track: OriginalTrack,
): TagMap {
	return desiredTags(release, track);
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
	provider: ReturnType<typeof findProviderTrack>;
	nextTags: TagMap;
	previous?: NonNullable<OriginalTrack["published"]>;
	action: "Пропустить" | "Без изменений" | "Заменить" | "Создать";
	downgrade: boolean;
};
export type IncomingPlan = {
	mode: IncomingGroup["mode"];
	groupId: string;
	target: OriginalRelease;
	localReleaseId: string | null;
	assignments: Record<string, Assignment>;
	inventoryVersion: number;
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
			: releases.find((release) => release.providerId === target.id);
	return {
		mode: group.mode,
		groupId: group.id,
		target: structuredClone(target),
		localReleaseId: local ? String(local.id) : null,
		assignments: structuredClone(getDraft(group, files, providers, releases)),
		inventoryVersion: state.inventoryVersion,
	};
}

export function planRows(
	plan: IncomingPlan,
	files: IncomingFiles,
	releases: IncomingReleases,
	providers: IncomingReleases = providerReleases,
): PlanRow[] {
	const existing = releases.find(
		(release) => release.id === plan.localReleaseId,
	);
	const targetTracks = plan.target.tracks;
	return targetTracks.map((targetTrack) => {
		const assignment = plan.assignments[String(targetTrack.id)];
		const track = existing?.tracks.find(
			(item) => item.position === targetTrack.position,
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
		const nextTags = providerTags(releaseForTags, projected);
		const previous = track?.published ?? undefined;
		const unchanged =
			previous &&
			file &&
			previous.sourceId === file.id &&
			previous.codec === file.codec &&
			JSON.stringify(previous.tags) === JSON.stringify(nextTags);
		return {
			targetTrack,
			assignment,
			track,
			file,
			provider,
			nextTags,
			previous,
			action:
				!assignment?.included || !file
					? "Пропустить"
					: unchanged
						? "Без изменений"
						: previous
							? "Заменить"
							: "Создать",
			downgrade: Boolean(
				previous?.codec === "FLAC" &&
					file?.codec === "MP3" &&
					assignment?.included,
			),
		};
	});
}

export function applyPlan(
	plan: IncomingPlan,
	files: IncomingFiles,
	releases: IncomingReleases,
	providers: IncomingReleases = providerReleases,
	automatic = false,
) {
	let release = releases.find((item) => item.id === plan.localReleaseId);
	if (!release) {
		release = {
			...structuredClone(plan.target),
			id: `import-${plan.target.id}`,
			providerId: plan.mode === "provider" ? plan.target.id : null,
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
	for (const row of planRows(plan, files, releases, providers)) {
		const track = release.tracks.find(
			(item) => item.position === row.targetTrack.position,
		);
		if (!track) continue;
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
			row.action === "Без изменений"
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
			track.published = {
				...row.previous,
				codec: row.file.codec,
				sourceId: row.file.id,
				readAt: "Только что · демо",
			};
			changed++;
			continue;
		}
		const path =
			published?.path ??
			`/srv/music/${artistName(String(release.artistId))}/${release.year} - ${release.title}/${String(row.targetTrack.position).padStart(2, "0")} - ${row.targetTrack.title}.mka`;
		track.published = {
			tags: structuredClone(row.nextTags),
			coverHash: track.coverHash ?? null,
			lyrics: track.lyrics ?? null,
			codec: row.file.codec,
			container: "MKA",
			sourceId: row.file.id,
			readAt: "Только что · демо",
			path,
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
						files[selected]?.confidence === "high" &&
						assignment?.included &&
						method.startsWith("Авто"),
				);
			})
		)
			continue;
		const plan = makeGroupPlan(state, group, files, releases, providers);
		if (!plan) continue;
		count += applyPlan(plan, files, releases, providers, true);
		group.processed = true;
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

export function tagFieldGroups() {
	return tagSets;
}
