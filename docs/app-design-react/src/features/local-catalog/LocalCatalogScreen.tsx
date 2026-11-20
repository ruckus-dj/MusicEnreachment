import { type Dispatch, type SetStateAction, useState } from "react";
import { Button } from "react-aria-components";
import { formStyles as styles } from "../../formStyles";
import { useI18n } from "../../i18n/I18nProvider";
import type {
	LocalProviderRecord,
	LocalProviderState,
} from "../../localProviderModel";
import {
	Input,
	Label,
	SearchField,
	Tab,
	TabList,
	TabPanel,
	Tabs,
} from "../../ui";
import type {
	LocalRecordingDraft,
	LocalReleaseArtist,
	LocalReleaseRecording,
} from "../local-release/localReleaseFormModel";
import { formatLocalDuration } from "../local-release/localReleaseFormModel";
import { muted, panel, primary } from "../local-release/localReleaseUi";
import { LocalCatalogDetails } from "./LocalCatalogDetails";
import { LocalCatalogEditorShell } from "./LocalCatalogEditorShell";
import { LocalCatalogPagination } from "./LocalCatalogPagination";
import {
	LocalCatalogArtistForm,
	LocalCatalogRecordingForm,
} from "./LocalCatalogRecordEditors";
import { LocalCatalogReleaseEditor } from "./LocalCatalogReleaseEditor";
import {
	type LocalCatalogMessage,
	localCatalogMessage,
} from "./localCatalogMessage";
import {
	artistDraftToRecord,
	filterLocalCatalogRecords,
	type LocalCatalogType,
	type LocalCatalogView,
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

export type LocalCatalogScreenProps = {
	state: LocalProviderState;
	onState: Dispatch<SetStateAction<LocalProviderState>>;
	genresLoaded?: boolean;
	genreOptions?: readonly { id: string; name: string }[];
	onLoadGenres?: () => void | Promise<void>;
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
export function LocalCatalogScreen({
	state,
	onState,
	genresLoaded = false,
	genreOptions,
	onLoadGenres,
}: LocalCatalogScreenProps) {
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

	const listHref = () => setView({ kind: "list" });
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

	if (view.kind === "details") {
		const record = viewRecord(view);
		if (record) {
			return (
				<LocalCatalogDetails
					locale={locale}
					state={state}
					type={view.type}
					record={record}
					onBack={listHref}
					onEdit={() => setView(editorView(view.type, record.id))}
				/>
			);
		}
	}

	if (view.kind === "create" || view.kind === "edit") {
		const record = view.kind === "edit" ? viewRecord(view) : undefined;
		if (view.kind !== "edit" || record) {
			const kind = view.type;
			const back = () => backFromEditor(view);
			if (kind === "releases") {
				return (
					<LocalCatalogReleaseEditor
						locale={locale}
						record={record}
						artists={artistOptions}
						recordings={recordingOptions}
						findRecordings={async (value) =>
							localCatalogFindRecordings(
								state.items.recordings,
								state.items.artists,
								value,
							)
						}
						findArtists={async (value) =>
							localCatalogFindArtists(state.items.artists, value)
						}
						saveRecording={appendRecording}
						saveArtist={appendArtist}
						genresLoaded={genresLoaded}
						genreOptions={genreOptions}
						onLoadGenres={onLoadGenres}
						onSave={(draft) => {
							const saved = releaseDraftToRecord(
								draft,
								record,
								state.items.artists,
							);
							commitRecord("releases", saved, !record);
							setView(detailView("releases", saved.id));
						}}
						onBack={back}
					/>
				);
			}
			if (kind === "recordings") {
				return (
					<LocalCatalogEditorShell
						eyebrow={m("eyebrow")}
						title={record ? m("editRecording") : m("createRecording")}
						backLabel={m("back")}
						onBack={back}
					>
						<LocalCatalogRecordingForm
							locale={locale}
							record={record}
							artists={state.items.artists}
							genresLoaded={genresLoaded}
							onLoadGenres={onLoadGenres}
							genreOptions={genreOptions}
							onSave={(draft) => {
								const saved = recordingDraftToRecord(
									draft,
									record,
									state.items.artists,
								);
								commitRecord("recordings", saved, !record);
								setView(detailView("recordings", saved.id));
							}}
							onBack={back}
						/>
					</LocalCatalogEditorShell>
				);
			}
			return (
				<LocalCatalogEditorShell
					eyebrow={m("eyebrow")}
					title={record ? m("editArtist") : m("createArtist")}
					backLabel={m("back")}
					onBack={back}
				>
					<LocalCatalogArtistForm
						locale={locale}
						record={record}
						genresLoaded={genresLoaded}
						onLoadGenres={onLoadGenres}
						genreOptions={genreOptions}
						onSave={(draft) => {
							const saved = artistDraftToRecord(draft, record);
							commitRecord("artists", saved, !record);
							setView(detailView("artists", saved.id));
						}}
						onBack={back}
					/>
				</LocalCatalogEditorShell>
			);
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

	return (
		<section className="mx-auto max-w-none p-3">
			<header className={styles.pageHeader}>
				<h1 className={styles.pageTitle}>{m("title")}</h1>
				<p className={styles.pageDescription}>{m("description")}</p>
			</header>
			<Tabs
				className={styles.localTabs}
				selectedKey={type}
				onSelectionChange={changeTab}
			>
				<TabList aria-label={m("tabListLabel")} className={styles.localTabList}>
					{localCatalogTypes.map((kind) => {
						const label = m(tabMessage[kind]);
						const count = localCatalogRecords(state, kind).length;
						return (
							<Tab
								aria-label={`${label}: ${count}`}
								className={`${styles.localTab} inline-flex items-center gap-1.5 whitespace-nowrap max-sm:px-2`}
								key={kind}
								id={kind}
							>
								<span>{label}</span>
								<span
									aria-hidden="true"
									className="inline-flex h-5 min-w-5 items-center justify-center rounded-full border border-[var(--line)] bg-[var(--raised)] px-1 text-xs leading-none font-medium text-[var(--ink)] tabular-nums"
								>
									{count}
								</span>
							</Tab>
						);
					})}
				</TabList>
				<TabPanel id={type} className="pt-2">
					<section className={`${panel} grid gap-2`}>
						<div className="flex flex-wrap items-end justify-between gap-2">
							<SearchField
								className="grid w-full min-w-0 gap-1 sm:max-w-[32rem] sm:flex-1"
								value={query}
								onChange={changeFilter}
							>
								<Label className="text-xs font-medium">
									{m("filterLabel")}
								</Label>
								<Input placeholder={m("filterPlaceholder")} />
							</SearchField>
							<Button
								className={primary}
								onPress={() => setView({ kind: "create", type })}
							>
								{m(createMessage[type])}
							</Button>
						</div>
						{records.length === 0 ? (
							<p className={muted}>{m(emptyMessage[type])}</p>
						) : filtered.length === 0 ? (
							<div className="grid justify-items-start gap-2">
								<p className={muted}>{m("noMatches")}</p>
								<Button onPress={() => changeFilter("")}>
									{m("clearFilter")}
								</Button>
							</div>
						) : (
							<ul className="m-0 list-none divide-y divide-[var(--line)] border-y border-[var(--line)] p-0">
								{pageRecords.map((record) => (
									<li key={record.id}>
										<Button
											className="flex min-h-14 w-full justify-start gap-2 border-0 bg-transparent px-3 py-2 text-left hover:bg-[var(--raised)] focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-[var(--focus)]"
											onPress={() => setView(detailView(type, record.id))}
										>
											<span className="min-w-0">
												<strong className="block wrap-anywhere">
													{rowTitle(record)}
												</strong>
												<small className="block text-[var(--muted)] wrap-anywhere">
													{rowDetails(record)}
												</small>
											</span>
										</Button>
									</li>
								))}
							</ul>
						)}
						<LocalCatalogPagination
							locale={locale}
							page={currentPage}
							totalPages={totalPages}
							totalItems={filtered.length}
							onPageChange={setPage}
						/>
					</section>
				</TabPanel>
			</Tabs>
			{notice ? (
				<p className="mt-3 text-sm text-[var(--ink)]" role="status">
					{m(notice)}
				</p>
			) : null}
		</section>
	);
}
