import {
	type ComponentProps,
	type Dispatch,
	type SetStateAction,
	useState,
} from "react";
import { useI18n } from "../../../app/i18n/host";
import type { LocalCatalogScreenViewProps } from "../../../ui/features/local-catalog/LocalCatalogScreenView";
import {
	formatLocalDuration,
	type LocalRecordingDraft,
	type LocalReleaseArtist,
	type LocalReleaseDraft,
	type LocalReleaseRecording,
} from "../../../ui/features/local-release/localReleaseFormModel";
import type { Locale } from "../../../ui/i18n/types";
import type {
	LocalCatalogArtistForm,
	LocalCatalogRecordingForm,
} from "./LocalCatalogRecordEditors";
import type { LocalCatalogReleaseEditor } from "./LocalCatalogReleaseEditor";
import { localCatalogDetailsViewProps } from "./localCatalogDetailsView";
import {
	type LocalCatalogMessage,
	localCatalogMessage,
} from "./localCatalogMessage";
import {
	artistDraftToRecord,
	filterLocalCatalogRecords,
	type LocalArtistFormDraft,
	type LocalCatalogType,
	type LocalCatalogView,
	type LocalRecordingFormDraft,
	localCatalogArtistNames,
	localCatalogFindArtists,
	localCatalogFindRecordings,
	localCatalogGenreNames,
	localCatalogPageCount,
	localCatalogPageSlice,
	localCatalogRecordingTitle,
	localCatalogRecordingWithCredits,
	localCatalogRecords,
	localCatalogReleaseTitle,
	localCatalogTypes,
	recordingDraftToRecord,
	releaseDraftToRecord,
} from "./localCatalogModel";
import type {
	LocalProviderRecord,
	LocalProviderState,
} from "./localProviderModel";

export type LocalCatalogScreenProps = {
	state: LocalProviderState;
	onState: Dispatch<SetStateAction<LocalProviderState>>;
	genresLoaded?: boolean;
	genreOptions?: readonly { id: string; name: string }[];
	onLoadGenres?: () => void | Promise<void>;
};

/** Prefabricated props for one independent editor actor, minus the facade locale. */
export type LocalCatalogEditorBinding =
	| {
			kind: "releases";
			props: Omit<ComponentProps<typeof LocalCatalogReleaseEditor>, "locale">;
	  }
	| {
			kind: "recordings";
			props: Omit<ComponentProps<typeof LocalCatalogRecordingForm>, "locale">;
	  }
	| {
			kind: "artists";
			props: Omit<ComponentProps<typeof LocalCatalogArtistForm>, "locale">;
	  };

export type LocalCatalogScreenModel = Omit<
	LocalCatalogScreenViewProps,
	"editor"
> & {
	locale: Locale;
	editor: LocalCatalogEditorBinding | null;
};

const tabMessage: Record<LocalCatalogType, LocalCatalogMessage> = {
	releases: "releases",
	recordings: "recordings",
	artists: "artists",
};
const createMessage: Record<LocalCatalogType, LocalCatalogMessage> = {
	releases: "createRelease",
	recordings: "createRecording",
	artists: "createArtist",
};
const emptyMessage: Record<LocalCatalogType, LocalCatalogMessage> = {
	releases: "emptyReleases",
	recordings: "emptyRecordings",
	artists: "emptyArtists",
};

/**
 * Local catalog demo provider: owns tab/filter/page/view/notice state, fixture
 * records, model lookups, record commits and routing, then hands the pure view
 * render-ready rows, models and events. It renders no JSX.
 */
export function useLocalCatalogScreenDemoProvider({
	state,
	onState,
	genresLoaded = false,
	genreOptions,
	onLoadGenres,
}: LocalCatalogScreenProps): LocalCatalogScreenModel {
	const { locale } = useI18n();
	const m = (
		key: LocalCatalogMessage,
		values?: Record<string, string | number>,
	) => localCatalogMessage(locale, key, values);
	const [type, setType] = useState<LocalCatalogType>("releases");
	const [query, setQuery] = useState("");
	const [page, setPage] = useState(1);
	const [view, setView] = useState<LocalCatalogView>({ kind: "list" });
	const [notice, setNotice] = useState<"created" | "updated" | null>(null);
	const records = localCatalogRecords(state, type);
	const filtered = filterLocalCatalogRecords(state, type, records, query);
	const totalPages = localCatalogPageCount(filtered.length);
	const currentPage = Math.min(Math.max(1, page), totalPages);
	const pageRecords = localCatalogPageSlice(filtered, currentPage);
	const allArtists = state.items.artists;
	const allRecordings = state.items.recordings;

	const artistOptions: LocalReleaseArtist[] = allArtists.map((artist) => ({
		id: artist.id,
		name: artist.name ?? "",
		provider: artist.provider,
	}));
	const recordingOptions: LocalReleaseRecording[] = allRecordings.map(
		(recording) => localCatalogRecordingWithCredits(recording, allArtists),
	);

	const commitRecord = (
		kind: LocalCatalogType,
		record: LocalProviderRecord,
		created: boolean,
	) => {
		onState((current) => ({
			...current,
			items: {
				...current.items,
				[kind]: created
					? [record, ...current.items[kind]]
					: current.items[kind].map((item) =>
							item.id === record.id ? record : item,
						),
			},
			selectedId: record.id,
		}));
		setNotice(created ? "created" : "updated");
	};

	const appendRecording = (
		data: LocalRecordingDraft,
	): LocalReleaseRecording => {
		const record: LocalProviderRecord = {
			...recordingDraftToRecord(
				{
					title: data.title,
					durationInput: formatLocalDuration(data.duration_ms),
					disambiguation: "",
					explicit: "",
					isrcs: "",
					tags: "",
					genres: [],
					artistIds: data.credits?.map((credit) => credit.artistId) ?? [],
				},
				undefined,
				state.items.artists,
			),
			duration_ms: data.duration_ms ?? null,
			credits: data.credits,
		};
		onState((current) => ({
			...current,
			items: {
				...current.items,
				recordings: [record, ...current.items.recordings],
			},
		}));
		return record;
	};

	const appendArtist = (data: { name: string }): LocalReleaseArtist => {
		const record = artistDraftToRecord(
			{
				name: data.name,
				sortName: "",
				disambiguation: "",
				tags: "",
				genres: [],
			},
			undefined,
		);
		onState((current) => ({
			...current,
			items: {
				...current.items,
				artists: [record, ...current.items.artists],
			},
		}));
		return {
			id: record.id,
			name: record.name ?? "",
			provider: record.provider,
		};
	};

	const viewRecord = (current: LocalCatalogView) => {
		if (current.kind === "list" || current.kind === "create") return undefined;
		return state.items[current.type].find((item) => item.id === current.id);
	};

	const navigate = (next: LocalCatalogView) => {
		setNotice(null);
		setView(next);
	};
	const listHref = () => navigate({ kind: "list" });
	const detailView = (
		kind: LocalCatalogType,
		id: string,
	): LocalCatalogView => ({
		kind: "details",
		type: kind,
		id,
	});
	const editorView = (
		kind: LocalCatalogType,
		id: string,
	): LocalCatalogView => ({
		kind: "edit",
		type: kind,
		id,
	});

	const backFromEditor = (current: LocalCatalogView) => {
		if (current.kind === "edit") {
			setView(detailView(current.type, current.id));
		} else {
			setView({ kind: "list" });
		}
	};

	let details: LocalCatalogScreenViewProps["details"] = null;
	if (view.kind === "details") {
		const record = viewRecord(view);
		if (record) {
			details = localCatalogDetailsViewProps({
				locale,
				state,
				type: view.type,
				record,
				onBack: listHref,
				onEdit: () => navigate(editorView(view.type, record.id)),
			});
		}
	}

	let editor: LocalCatalogEditorBinding | null = null;
	if (view.kind === "create" || view.kind === "edit") {
		const record = view.kind === "edit" ? viewRecord(view) : undefined;
		if (view.kind !== "edit" || record) {
			const kind = view.type;
			const back = () => backFromEditor(view);
			if (kind === "releases") {
				editor = {
					kind,
					props: {
						record,
						artists: artistOptions,
						recordings: recordingOptions,
						findRecordings: async (value) =>
							localCatalogFindRecordings(
								state.items.recordings,
								state.items.artists,
								value,
							),
						findArtists: async (value) =>
							localCatalogFindArtists(state.items.artists, value),
						saveRecording: appendRecording,
						saveArtist: appendArtist,
						genresLoaded,
						genreOptions,
						onLoadGenres,
						onSave: (draft: LocalReleaseDraft) => {
							const saved = releaseDraftToRecord(
								draft,
								record,
								state.items.artists,
							);
							commitRecord("releases", saved, !record);
							setView(detailView("releases", saved.id));
						},
						onBack: back,
					},
				};
			} else if (kind === "recordings") {
				editor = {
					kind,
					props: {
						record,
						artists: state.items.artists,
						genresLoaded,
						onLoadGenres,
						genreOptions,
						onSave: (draft: LocalRecordingFormDraft) => {
							const saved = recordingDraftToRecord(
								draft,
								record,
								state.items.artists,
							);
							commitRecord("recordings", saved, !record);
							setView(detailView("recordings", saved.id));
						},
						onBack: back,
					},
				};
			} else {
				editor = {
					kind,
					props: {
						record,
						genresLoaded,
						onLoadGenres,
						genreOptions,
						onSave: (draft: LocalArtistFormDraft) => {
							const saved = artistDraftToRecord(draft, record);
							commitRecord("artists", saved, !record);
							setView(detailView("artists", saved.id));
						},
						onBack: back,
					},
				};
			}
		}
	}

	const rowTitle = (record: LocalProviderRecord) =>
		type === "artists"
			? record.name || "—"
			: type === "recordings"
				? localCatalogRecordingTitle(record) || "—"
				: localCatalogReleaseTitle(record) || "—";
	const rowDetails = (record: LocalProviderRecord) => {
		if (type === "artists") {
			const genres = localCatalogGenreNames(record).join(", ");
			const linked = state.items.releases.filter((release) =>
				release.artistIds?.includes(record.id),
			).length;
			return [genres, m("linkedReleases", { count: linked })]
				.filter(Boolean)
				.join(" · ");
		}
		const artists =
			record.credits
				?.map((credit) => `${credit.creditName}${credit.joinPhrase ?? ""}`)
				.join("")
				.trim() || localCatalogArtistNames(state, record.artistIds).join(", ");
		const artistCredit = artists ? m("artistCredit", { artists }) : "";
		if (type === "recordings") {
			return [artistCredit, formatLocalDuration(record.duration_ms)]
				.filter(Boolean)
				.join(" · ");
		}
		return [
			artistCredit,
			m("trackCount", { count: record.tracks?.length ?? 0 }),
			record.disambiguation,
			record.release_date,
			record.country_code,
			record.format,
			[record.label, record.catalog_number].filter(Boolean).join(" / "),
		]
			.filter(Boolean)
			.join(" · ");
	};
	const changeTab = (key: string | number) => {
		const next = localCatalogTypes.find((kind) => kind === key);
		if (!next) return;
		setType(next);
		setPage(1);
		setView({ kind: "list" });
	};
	const changeFilter = (value: string) => {
		setQuery(value);
		setPage(1);
	};

	return {
		locale,
		list: {
			locale,
			copy: {
				title: m("title"),
				description: m("description"),
				tabListLabel: m("tabListLabel"),
				filterLabel: m("filterLabel"),
				filterPlaceholder: m("filterPlaceholder"),
				createLabel: m(createMessage[type]),
				emptyLabel: m(emptyMessage[type]),
				loading: m("loading"),
				retry: m("retry"),
				noMatches: m("noMatches"),
				clearFilter: m("clearFilter"),
			},
			tabs: localCatalogTypes.map((kind) => ({
				id: kind,
				label: m(tabMessage[kind]),
				count: localCatalogRecords(state, kind).length,
			})),
			selectedTab: type,
			onTabChange: changeTab,
			filter: query,
			hasFilter: Boolean(records.length && query.trim()),
			onFilterChange: changeFilter,
			onClearFilter: () => changeFilter(""),
			onCreate: () => navigate({ kind: "create", type }),
			loading: false,
			loaded: true,
			error: null,
			page: {
				total: filtered.length,
				rows: pageRecords.map((record) => ({
					id: record.id,
					title: rowTitle(record),
					detail: rowDetails(record),
					onOpen: () => navigate(detailView(type, record.id)),
				})),
			},
			pageNumber: currentPage,
			pageCount: totalPages,
			onRetry: () => {},
			onPageChange: setPage,
		},
		notice: notice ? m(notice) : "",
		details,
		editor,
	};
}
