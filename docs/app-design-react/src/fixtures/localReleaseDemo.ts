import type { CatalogArtist } from "../catalogData";
import type { ReleaseCheckHit } from "../features/local-release/LocalReleaseEditor";
import type {
	IncomingFiles,
	IncomingGroup,
	IncomingReleases,
	IncomingState,
} from "../incomingModel";
import type {
	LocalProviderRecord,
	LocalProviderState,
	LocalProviderType,
} from "../localProviderModel";

const normalize = (value: string) =>
	value.normalize("NFC").trim().replace(/\s+/gu, " ").toLowerCase();

function commonTag(group: IncomingGroup, files: IncomingFiles, key: string) {
	const distinct = new Map<string, string>();
	for (const id of group.fileIds) {
		for (const value of files[id]?.tags?.[key] ?? []) {
			if (value.trim()) distinct.set(normalize(value), value.trim());
		}
	}
	return distinct.size === 1 ? distinct.values().next().value : undefined;
}

export function localReleaseSuggestions(
	group: IncomingGroup,
	files: IncomingFiles,
	artists: readonly CatalogArtist[],
) {
	return {
		artist:
			commonTag(group, files, "ALBUMARTIST") ??
			artists.find((artist) => artist.id === group.artistId)?.name ??
			"",
		title: commonTag(group, files, "ALBUM") ?? group.title,
	};
}

export function checkDemoReleases(
	providers: IncomingReleases,
	artists: readonly CatalogArtist[],
	artistQuery: string,
	titleQuery: string,
): ReleaseCheckHit[] {
	const title = normalize(titleQuery);
	const artist = normalize(artistQuery);
	if (!title) return [];
	return providers.flatMap((release) => {
		const name =
			release.credits
				?.map((credit) => credit.credit || credit.name)
				.join(" ") ||
			artists.find((candidate) => candidate.id === release.artistId)?.name ||
			"";
		if (
			!normalize(release.title).includes(title) ||
			(artist && !normalize(name).includes(artist))
		)
			return [];
		return [
			{
				id: release.id,
				title: release.title,
				artist: name,
				provider: release.provider ?? "MusicBrainz",
				details: [release.year, release.country, release.catalog]
					.filter(Boolean)
					.join(" · "),
			},
		];
	});
}

export function findDemoRecordings(
	recordings: readonly LocalProviderRecord[],
	query: string,
) {
	const title = normalize(query);
	return title
		? recordings.filter((item) => normalize(item.title ?? "").includes(title))
		: [];
}

export function newDemoLocalRecord(
	data: Omit<LocalProviderRecord, "id" | "key" | "provider">,
): LocalProviderRecord {
	const key = crypto.randomUUID();
	return { ...data, id: `local-${key}`, key, provider: "Local" };
}

export function appendDemoLocalRecord(
	state: LocalProviderState,
	kind: LocalProviderType,
	record: LocalProviderRecord,
): LocalProviderState {
	return {
		...state,
		items: { ...state.items, [kind]: [...state.items[kind], record] },
	};
}

export function selectDemoRelease(
	state: IncomingState,
	groupId: string,
	release: Pick<ReleaseCheckHit, "id" | "title">,
	isNew = false,
): IncomingState {
	if (!state.groups.some((group) => group.id === groupId)) return state;
	return {
		...state,
		groups: state.groups.map((group) =>
			group.id === groupId
				? {
						...group,
						mode: "provider",
						candidateId: release.id,
						drafts: isNew
							? { ...group.drafts, [release.id]: {} }
							: group.drafts,
					}
				: group,
		),
		groupId,
		selectedFile: null,
		selectedTarget: null,
		recordingTarget: null,
		recordingResults: null,
		plan: state.plan?.groupId === groupId ? null : state.plan,
		confirmDowngrade:
			state.plan?.groupId === groupId ? false : state.confirmDowngrade,
		notice: "",
		noticeDescriptor: null,
		uiNotice: isNew
			? {
					namespace: "library",
					id: "localReleaseSelected",
					params: { title: release.title },
				}
			: null,
	};
}
