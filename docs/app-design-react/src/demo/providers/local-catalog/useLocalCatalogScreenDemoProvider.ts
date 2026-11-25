import {
	type ComponentProps,
	type Dispatch,
	type SetStateAction,
	useState,
} from "react";
import { useI18n } from "../../../app/i18n/host";
import { useHashLocation } from "../../../app/navigation/hashLocation";
import type { GenrePickerCatalog } from "../../../ui/features/library/GenrePicker";
import type {
	LocalCatalogReleaseEditorActor,
	LocalCatalogScreenViewProps,
} from "../../../ui/features/local-catalog/LocalCatalogScreenView";
import { ownCatalogMessage } from "../../../ui/features/local-catalog/ownCatalogMessages";
import type { LocalReleaseGenreSelectionAdapter } from "../../../ui/features/local-release/LocalReleaseGenreCatalogOutlet";
import {
	formatLocalDuration,
	type LocalRecordingDraft,
	type LocalReleaseArtist,
	type LocalReleaseDraft,
	type LocalReleaseRecording,
} from "../../../ui/features/local-release/localReleaseFormModel";
import type { Locale } from "../../../ui/i18n/types";
import {
	genreCatalog as demoGenreCatalog,
	resolveCatalogGenreName,
} from "../genres/catalogue";
import { useGenrePickerDemoProvider } from "../library/metadata/useGenrePickerDemoProvider";
import { DemoReleaseGenreCatalogProvider } from "./DemoReleaseGenreCatalogProvider";
import type {
	LocalCatalogArtistForm,
	LocalCatalogRecordingForm,
} from "./LocalCatalogRecordEditors";
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
	releaseRecordToFormValues,
} from "./localCatalogModel";
import {
	defaultLocalCatalogRoute,
	localCatalogRouteHref,
	parseLocalCatalogRoute,
} from "./localCatalogRoute";
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
			actor: LocalCatalogReleaseEditorActor;
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

type LocalCatalogEditorDraft =
	| { kind: "create"; type: LocalCatalogType }
	| { kind: "edit"; type: LocalCatalogType; id: string };
type LocalCatalogEditorState = LocalCatalogEditorDraft & { hash: string };
type LocalCatalogHistoryState = { localCatalogDetail?: string };

function hashURL(href: string) {
	return `${window.location.pathname}${window.location.search}${href}`;
}

function publishHashChange() {
	window.dispatchEvent(new HashChangeEvent("hashchange"));
}

function pushCatalogHash(
	href: string,
	state: LocalCatalogHistoryState | null = null,
) {
	if (window.location.hash === href) return;
	window.history.pushState(state, "", hashURL(href));
	publishHashChange();
}

function replaceCatalogHash(href: string) {
	if (window.location.hash === href) return;
	window.history.replaceState(null, "", hashURL(href));
	publishHashChange();
}

/**
 * Local catalog demo provider: owns tab/filter/page/notice state, fixture
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
	const hash = useHashLocation();
	const route = parseLocalCatalogRoute(hash) ?? defaultLocalCatalogRoute;
	const [query, setQuery] = useState("");
	const [page, setPage] = useState(1);
	const [notice, setNotice] = useState<"created" | "updated" | null>(null);
	const [editorState, setEditor] = useState<LocalCatalogEditorState>();
	const activeEditor = editorState?.hash === hash ? editorState : undefined;
	const releaseGenreCatalog: GenrePickerCatalog = useGenrePickerDemoProvider({
		value: [],
		onChange: () => undefined,
		loaded: genresLoaded,
		onLoad: onLoadGenres,
		options: genreOptions,
		lifetime: activeEditor
			? `${activeEditor.hash}:${activeEditor.kind}:${activeEditor.type}`
			: "own-catalog-closed",
	});
	const releaseGenreCatalogById = new Map(
		(genreOptions ?? demoGenreCatalog).map((item) => [item.id, item]),
	);
	const releaseGenreSelection: LocalReleaseGenreSelectionAdapter = {
		display: (value) =>
			value.map((item) => {
				const entry = releaseGenreCatalogById.get(item.id);
				const name = entry ? resolveCatalogGenreName(entry) : item.name;
				return { id: item.id, name, source_name: name };
			}),
		adapt: (next, current) => {
			const currentById = new Map(current.map((item) => [item.id, item]));
			return next.map(
				(item) =>
					currentById.get(item.id) ?? {
						id: item.id,
						name: item.name,
						source_name: item.source_name,
					},
			);
		},
	};
	const [visitHash, setVisitHash] = useState(hash);
	if (visitHash !== hash) {
		setVisitHash(hash);
		setEditor(undefined);
		if (!route.id) setNotice(null);
	}
	const [pageType, setPageType] = useState(route.type);
	if (pageType !== route.type) {
		setPageType(route.type);
		setPage(1);
	}
	const type = route.type;
	const records = localCatalogRecords(state, type);
	const filtered = filterLocalCatalogRecords(state, type, records, query);
	const totalPages = localCatalogPageCount(filtered.length);
	const currentPage = Math.min(Math.max(1, page), totalPages);
	const pageRecords = localCatalogPageSlice(filtered, currentPage);
	const detailRecord = route.id
		? state.items[type].find(
				(item) => item.id === route.id && item.provider === "Local",
			)
		: undefined;
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
		setEditor(undefined);
		setNotice(created ? "created" : "updated");
		if (created) {
			const href = localCatalogRouteHref({ type: kind, id: record.id });
			pushCatalogHash(href, { localCatalogDetail: href });
		}
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

	const openDetails = (kind: LocalCatalogType, record: LocalProviderRecord) => {
		setNotice(null);
		const href = localCatalogRouteHref({ type: kind, id: record.id });
		pushCatalogHash(href, { localCatalogDetail: href });
	};

	const closeDetails = () => {
		if (!route.id) return;
		setNotice(null);
		const detailHref = localCatalogRouteHref({
			type: route.type,
			id: route.id,
		});
		const historyState = window.history
			.state as LocalCatalogHistoryState | null;
		if (historyState?.localCatalogDetail === detailHref) {
			window.history.back();
			return;
		}
		replaceCatalogHash(localCatalogRouteHref({ type: route.type }));
	};

	const openEditor = (next: LocalCatalogEditorDraft) => {
		setNotice(null);
		setEditor({ ...next, hash });
	};
	const backFromEditor = () => setEditor(undefined);

	let details: LocalCatalogScreenViewProps["details"] = null;
	if (route.id && !activeEditor) {
		details = detailRecord
			? localCatalogDetailsViewProps({
					locale,
					state,
					type,
					record: detailRecord,
					onBack: closeDetails,
					onEdit: () => openEditor({ kind: "edit", type, id: detailRecord.id }),
				})
			: {
					title: ownCatalogMessage(locale, "recordDetails"),
					eyebrow: ownCatalogMessage(locale, "currentDataNoHistory"),
					backLabel: ownCatalogMessage(locale, "back"),
					editLabel: ownCatalogMessage(locale, "edit"),
					onBack: closeDetails,
					body: {
						kind: "error",
						message: ownCatalogMessage(locale, "detailLoadError"),
						retryLabel: ownCatalogMessage(locale, "retry"),
						// Reading the same synchronous fixture yields the same result.
						onRetry: () => {},
					},
				};
	}

	let editor: LocalCatalogEditorBinding | null = null;
	if (activeEditor) {
		const record =
			activeEditor.kind === "edit" && detailRecord?.id === activeEditor.id
				? detailRecord
				: undefined;
		if (activeEditor.kind !== "edit" || record) {
			const kind = activeEditor.type;
			const back = backFromEditor;
			if (kind === "releases") {
				editor = {
					kind,
					actor: {
						initialValues: record
							? releaseRecordToFormValues(record)
							: undefined,
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
						genreCatalogProvider: DemoReleaseGenreCatalogProvider,
						genreCatalogModel: releaseGenreCatalog,
						genreCatalogSelection: releaseGenreSelection,
						onSave: (draft: LocalReleaseDraft) => {
							const saved = releaseDraftToRecord(
								draft,
								record,
								state.items.artists,
							);
							commitRecord("releases", saved, !record);
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
		if (!next || next === type) return;
		setNotice(null);
		setEditor(undefined);
		pushCatalogHash(localCatalogRouteHref({ type: next }));
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
			onCreate: () => openEditor({ kind: "create", type }),
			loading: false,
			loaded: true,
			error: null,
			page: {
				total: filtered.length,
				rows: pageRecords.map((record) => ({
					id: record.id,
					title: rowTitle(record),
					detail: rowDetails(record),
					onOpen: () => openDetails(type, record),
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
