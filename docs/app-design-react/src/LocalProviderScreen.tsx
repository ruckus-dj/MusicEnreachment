import { useEffect, useRef, useState, type FormEvent } from "react";
import {
	Button,
	Checkbox,
	Input,
	Label,
	ListBox,
	ListBoxItem,
	Popover,
	SearchField,
	Select,
	SelectValue,
	Tab,
	TabList,
	TabPanel,
	Tabs,
	TextField,
} from "./ui";
import { formStyles as styles } from "./formStyles";
import { demoGenres } from "./GenreLibrary";
import { GenrePicker, type GenreReference } from "./metadata/GenrePicker";
import { MetadataEditor } from "./metadata/MetadataEditor";
import type { MetadataCredit } from "./metadata/model";
import type { MetadataValues } from "./metadata/projection";
import { validateMetadataValues } from "./metadata/projection";
import { fieldsForScope } from "./metadata/registry";
import {
	createLocalProviderState,
	formatLocalDuration,
	parseLocalDuration,
	type LocalProviderRecord,
	type LocalProviderState,
	type LocalProviderTrack,
	type LocalProviderType,
} from "./localProviderModel";

export type LocalProviderScreenProps = {
	state?: LocalProviderState;
	onState?: (state: LocalProviderState) => void;
	onMatchRelease?: (release: LocalProviderRecord) => void;
	genresLoaded?: boolean;
	genreOptions?: readonly { id: string; name: string }[];
	onLoadGenres?: () => void | Promise<void>;
};

const typeInfo: Record<
	LocalProviderType,
	{ label: string; entity: string; create: string; search: string }
> = {
	artists: {
		label: "Артисты",
		entity: "Артист",
		create: "Создать артиста",
		search: "Поиск в артистах",
	},
	releases: {
		label: "Релизы",
		entity: "Релиз",
		create: "Создать релиз",
		search: "Поиск в релизах",
	},
	recordings: {
		label: "Композиции",
		entity: "Композиция",
		create: "Создать композицию",
		search: "Поиск в композициях",
	},
};

function ids() {
	return crypto.randomUUID();
}
function artistName(state: LocalProviderState, id: string) {
	return (
		state.items.artists.find((artist) => artist.id === id)?.name ||
		"Артист не указан"
	);
}
function cloneState(state: LocalProviderState) {
	return structuredClone(state);
}
function splitList(value: string) {
	return value
		.split(",")
		.map((item) => item.trim())
		.filter(Boolean);
}

const localAdvisory = (value: string) =>
	value === "true" ? "1" : value === "false" ? "2" : value;
const localExplicit = (value: string) =>
	["1", "4"].includes(value) ? "true" : value === "2" ? "false" : "";

export function LocalProviderScreen({
	state: givenState,
	onState,
	onMatchRelease,
	genresLoaded = false,
	genreOptions,
	onLoadGenres,
}: LocalProviderScreenProps) {
	const [localState, setLocalState] = useState(createLocalProviderState);
	const [submitError, setSubmitError] = useState("");
	const state = givenState ?? localState;
	const isForm = state.editing || state.creating;
	const focusRef = useRef<HTMLInputElement>(null);
	const formRef = useRef<HTMLFormElement>(null);
	const durationRef = useRef<HTMLInputElement>(null);
	const trackDurationRefs = useRef<Array<HTMLInputElement | null>>([]);
	const detailHeadingRef = useRef<HTMLHeadingElement>(null);
	const update = (next: LocalProviderState) =>
		givenState ? onState?.(next) : setLocalState(next);
	const config = typeInfo[state.type];
	const selected =
		state.items[state.type].find((item) => item.id === state.selectedId) ??
		null;
	useEffect(() => {
		if (state.notice.startsWith("Демо:") && selected) {
			detailHeadingRef.current?.focus({ preventScroll: true });
		}
	}, [selected, state.notice]);
	const localItems = state.items[state.type].filter(
		(item) => item.provider === "Local",
	);
	const queryTerms = state.query
		.normalize("NFKC")
		.toLocaleLowerCase()
		.trim()
		.split(/\s+/)
		.filter(Boolean);
	const rows = localItems.filter((item) => {
		const haystack =
			`${item.name ?? ""} ${item.title ?? ""} ${item.release_group_title ?? ""} ${item.key}`
				.normalize("NFKC")
				.toLocaleLowerCase();
		return queryTerms.every((term) => haystack.includes(term));
	});
	const value = state.draft;
	const displayedValue = value ?? selected;
	const genreOptionsById = new Map(
		(genreOptions ?? demoGenres).map((genre) => [genre.id, genre]),
	);
	const genreReferences = (
		record: LocalProviderRecord | null | undefined,
	): GenreReference[] =>
		(record?.genres ?? []).map((id) => ({
			id,
			name:
				genreOptionsById.get(id)?.name ?? record?.genreNamesById?.[id] ?? id,
		}));
	const currentGenres = genreReferences(displayedValue);
	const legacyMetadataKeys: Record<string, string> =
		state.type === "releases"
			? {
					title: "album",
					release_date: "releasedate",
					label: "recordlabel",
					catalog_number: "catalognumber",
					country_code: "releasecountry",
					status: "releasestatus",
					barcode: "barcode",
					script: "script",
				}
			: state.type === "recordings"
				? { title: "title", isrcs: "isrc", explicit: "explicitstatus" }
				: {};
	const legacyMetadataValues = Object.fromEntries(
		Object.entries(legacyMetadataKeys).flatMap(([name, key]) => {
			const raw = displayedValue?.[name as keyof LocalProviderRecord];
			return raw === undefined
				? []
				: [
						[
							key,
							key === "isrc"
								? splitList(String(raw))
								: raw === ""
									? []
									: [
											key === "explicitstatus"
												? localAdvisory(String(raw))
												: String(raw),
										],
						],
					];
		}),
	) as MetadataValues;
	const updateDraft = (change: Partial<LocalProviderRecord>) => {
		setSubmitError("");
		const next = cloneState(state);
		next.draft = { ...(next.draft ?? ({} as LocalProviderRecord)), ...change };
		update(next);
	};
	const saveMetadata = (
		scope: "release" | "recording",
		metadataValues: MetadataValues,
		details?: {
			credits?: MetadataCredit[];
			genres?: { id?: string; name?: string }[];
		},
	) => {
		const genres = (details?.genres ?? currentGenres).map((genre) => ({
			...genre,
			name: genre.name ?? genre.id ?? "",
		}));
		const scopedKeys = new Set(fieldsForScope(scope).map((field) => field.key));
		updateDraft({
			...Object.fromEntries(
				Object.entries(legacyMetadataKeys).flatMap(([name, key]) =>
					Object.hasOwn(metadataValues, key)
						? [
								[
									name,
									key === "isrc"
										? metadataValues[key].join(", ")
										: key === "explicitstatus"
											? localExplicit(metadataValues[key]?.[0] ?? "")
											: (metadataValues[key]?.[0] ?? ""),
								],
							]
						: [],
				),
			),
			metadataValues: {
				...Object.fromEntries(
					Object.entries(value?.metadataValues ?? {}).filter(
						([key]) => !scopedKeys.has(key),
					),
				),
				...metadataValues,
			},
			...(scope === "release"
				? {
						title: Object.hasOwn(metadataValues, "album")
							? (metadataValues.album?.[0] ?? "")
							: value?.title,
						release_date: Object.hasOwn(metadataValues, "releasedate")
							? (metadataValues.releasedate?.[0] ?? "")
							: value?.release_date,
						label: Object.hasOwn(metadataValues, "recordlabel")
							? (metadataValues.recordlabel?.[0] ?? "")
							: value?.label,
					}
				: {
						title: Object.hasOwn(metadataValues, "title")
							? (metadataValues.title?.[0] ?? "")
							: value?.title,
						isrcs: Object.hasOwn(metadataValues, "isrc")
							? (metadataValues.isrc ?? []).join(", ")
							: value?.isrcs,
					}),
			credits:
				details?.credits?.map((credit) => ({
					...credit,
				})) ??
				value?.credits ??
				[],
			...(details?.credits
				? {
						artistIds: details.credits
							.map((credit) => credit.artistId)
							.filter(Boolean),
					}
				: {}),
			genres: genres.map((genre) => genre.id ?? genre.name),
			genreNamesById: Object.fromEntries(
				genres.map((genre) => [genre.id ?? genre.name, genre.name]),
			),
		});
	};
	const metadataEditor = (scope: "release" | "recording") => (
		<MetadataEditor
			label={scope === "release" ? "Метаданные издания" : "Метаданные записи"}
			scope={scope}
			values={Object.fromEntries(
				Object.entries({
					...legacyMetadataValues,
					...displayedValue?.metadataValues,
				}).filter(([key]) =>
					fieldsForScope(scope).some((field) => field.key === key),
				),
			)}
			onSave={(values, details) => saveMetadata(scope, values, details)}
			credits={
				displayedValue?.credits?.map((credit) => ({
					...credit,
				})) ??
				displayedValue?.artistIds?.map((artistId) => ({
					artistId,
					creditName: artistName(state, artistId),
					joinPhrase: "",
				}))
			}
			artists={state.items.artists.map((artist) => ({
				id: artist.id,
				name: artist.name ?? "",
				sortName: artist.sortName,
			}))}
			genres={currentGenres}
			editing={state.editing || state.creating}
			hideActions
			readOnly={displayedValue?.provider !== "Local"}
			onDraftChange={(values, details) => saveMetadata(scope, values, details)}
			genresLoaded={genresLoaded}
			onLoadGenres={onLoadGenres}
		/>
	);
	const field = (
		label: string,
		name: keyof LocalProviderRecord,
		required = false,
		initial = "",
		readOnly = false,
	) => (
		<TextField
			className={styles.localField}
			isRequired={required}
			name={name}
			value={String(displayedValue?.[name] ?? initial)}
			isReadOnly={readOnly || !isForm}
			onChange={(nextValue) => {
				const metadataKey = legacyMetadataKeys[name];
				updateDraft({
					[name]: nextValue,
					...(metadataKey
						? {
								metadataValues: {
									...displayedValue?.metadataValues,
									[metadataKey]:
										metadataKey === "isrc" ? splitList(nextValue) : [nextValue],
								},
							}
						: {}),
				});
			}}
		>
			<Label>
				{label}{" "}
				{required ? (
					<span className={styles.required}>Обязательно</span>
				) : (
					<span className={styles.muted}>необязательно</span>
				)}
			</Label>
			<Input
				className="read-only:border-transparent read-only:bg-transparent"
				ref={
					name === "name" || name === "release_group_title" || name === "title"
						? focusRef
						: undefined
				}
			/>
		</TextField>
	);
	const genrePicker = (
		<GenrePicker
			value={currentGenres}
			readOnly={!isForm}
			onChange={(genres) =>
				updateDraft({
					genres: genres.map((genre) => genre.id ?? genre.name),
					genreNamesById: Object.fromEntries(
						genres.map((genre) => [genre.id ?? genre.name, genre.name]),
					),
				})
			}
			loaded={genresLoaded}
			onLoad={onLoadGenres}
			options={genreOptions}
		/>
	);
	const artistLinks = (
		<fieldset className={styles.genrePicker}>
			<legend>
				{state.type === "releases"
					? "Артисты релиза · ссылки на существующих артистов"
					: "Исполнители · ссылки на существующих артистов"}
			</legend>
			{state.items.artists.map((artist) => (
				<div className={styles.localOption} key={artist.id}>
					<Checkbox
						isSelected={displayedValue?.artistIds?.includes(artist.id) ?? false}
						onChange={(event) =>
							updateDraft({
								artistIds: event
									? [...(displayedValue?.artistIds ?? []), artist.id]
									: (displayedValue?.artistIds ?? []).filter(
											(id) => id !== artist.id,
										),
							})
						}
					>
						{artist.name}
					</Checkbox>
					<span className={styles.muted}>
						· {artist.provider}
						{artist.provider !== "Local"
							? " · внешняя ссылка, только чтение"
							: ""}{" "}
						· <code>{artist.key}</code>
					</span>
				</div>
			))}
			<small>
				Выбор сохраняет принадлежность провайдеру и исходный ключ; копия записи
				не создаётся.
			</small>
		</fieldset>
	);
	const tracks = displayedValue?.tracks ?? [];
	const changeTrack = (index: number, change: Partial<LocalProviderTrack>) =>
		updateDraft({
			tracks: tracks.map((track, i) =>
				i === index ? { ...track, ...change } : track,
			),
		});
	const trackRows = (
		<div className={styles.localTracklist}>
			<div className={styles.localTracklistHeading}>
				<strong>Носитель 1 · позиции</strong>
				<Button
					onPress={() =>
						updateDraft({
							tracks: [
								...tracks,
								{
									id: ids(),
									recordingId: "",
									titleOverride: "",
									durationOverrideMs: "",
									durationInput: "",
								},
							],
						})
					}
				>
					Добавить позицию
				</Button>
			</div>
			{tracks.length ? (
				tracks.map((track, index) => {
					const recording = state.items.recordings.find(
						(candidate) => candidate.id === track.recordingId,
					);
					return (
						<fieldset className={styles.fieldset} key={track.id}>
							<legend>Позиция {index + 1}</legend>
							<div className={styles.localTrackActions}>
								<Button
									isDisabled={index === 0}
									aria-label={`Переместить позицию ${index + 1} выше`}
									onPress={() => {
										const next = [...tracks];
										[next[index - 1], next[index]] = [
											next[index],
											next[index - 1],
										];
										updateDraft({ tracks: next });
									}}
								>
									↑
								</Button>
								<Button
									isDisabled={index === tracks.length - 1}
									aria-label={`Переместить позицию ${index + 1} ниже`}
									onPress={() => {
										const next = [...tracks];
										[next[index + 1], next[index]] = [
											next[index],
											next[index + 1],
										];
										updateDraft({ tracks: next });
									}}
								>
									↓
								</Button>
								<Button
									aria-label={`Удалить позицию ${index + 1}`}
									onPress={() =>
										updateDraft({
											tracks: tracks.filter((_, i) => i !== index),
										})
									}
								>
									Удалить
								</Button>
							</div>
							<label className={styles.localField}>
								Существующая композиция
								<Select
									aria-label="Существующая композиция"
									selectedKey={track.recordingId || null}
									onSelectionChange={(key) =>
										changeTrack(index, { recordingId: String(key ?? "") })
									}
								>
									<Button>
										<SelectValue />
									</Button>
									<Popover>
										<ListBox>
											{state.items.recordings.map((candidate) => (
												<ListBoxItem key={candidate.id} id={candidate.id}>
													{candidate.title} · {candidate.provider} ·{" "}
													{candidate.key}
												</ListBoxItem>
											))}
										</ListBox>
									</Popover>
								</Select>
							</label>
							<p className={styles.localTrackDefault}>
								По умолчанию:{" "}
								<strong>{recording?.title || "Композиция не выбрана"}</strong> ·{" "}
								{formatLocalDuration(recording?.duration_ms) ||
									"длительность не указана"}{" "}
								·{" "}
								{recording?.artistIds
									?.map((id) => artistName(state, id))
									.join(", ") || "артисты не указаны"}
							</p>
							<div className={styles.localFields}>
								<TextField
									className={styles.localField}
									value={track.titleOverride}
									onChange={(titleOverride) =>
										changeTrack(index, { titleOverride })
									}
								>
									<Label>Название в этой позиции · своё значение</Label>
									<Input />
								</TextField>
								<TextField
									className={styles.localField}
									value={
										track.durationInput ??
										formatLocalDuration(track.durationOverrideMs)
									}
									onChange={(durationInput) =>
										changeTrack(index, { durationInput })
									}
								>
									<Label>Длительность · своё значение</Label>
									<Input
										ref={(element) => {
											trackDurationRefs.current[index] = element;
										}}
										aria-describedby={`track-duration-error-${index}`}
										aria-invalid={
											Number.isNaN(
												parseLocalDuration(
													track.durationInput ??
														formatLocalDuration(track.durationOverrideMs),
												),
											) || undefined
										}
									/>
								</TextField>
							</div>
							<small>
								Номер позиции определяется порядком строки. Длительность: целые
								секунды или М:СС.
							</small>
							<p
								id={`track-duration-error-${index}`}
								className={`${styles.localNote} ${styles.localError}`}
								role="alert"
								hidden={
									!Number.isNaN(
										parseLocalDuration(
											track.durationInput ??
												formatLocalDuration(track.durationOverrideMs),
										),
									)
								}
							>
								Проверьте своё значение длительности: используйте целые секунды
								или М:СС с секундами от 00 до 59.
							</p>
						</fieldset>
					);
				})
			) : (
				<p className={styles.localEmpty}>
					Позиции не добавлены. Пустой трек-лист допустим.
				</p>
			)}
		</div>
	);
	const startCreate = () => {
		const next = cloneState(state);
		next.selectedId = null;
		next.creating = true;
		next.editing = false;
		next.notice = "";
		next.draft = {
			id: "",
			key: "",
			provider: "Local",
			artistIds: [],
			genres: [],
			tracks: [],
			secondaryTypes: [],
		};
		update(next);
	};
	const submit = (event: FormEvent<HTMLFormElement>) => {
		event.preventDefault();
		if (!value) return;
		if (state.type === "releases" || state.type === "recordings") {
			const scope = state.type === "releases" ? "release" : "recording";
			const metadataValues = Object.fromEntries(
				Object.entries(value.metadataValues ?? {}).filter(([key]) =>
					fieldsForScope(scope).some((field) => field.key === key),
				),
			) as MetadataValues;
			const metadataErrors = validateMetadataValues(metadataValues, scope);
			if (metadataErrors.length) {
				setSubmitError(metadataErrors.join(" "));
				return;
			}
		}
		const submittedValue = structuredClone(value);
		if (state.type === "recordings") {
			const durationInput = event.currentTarget.elements.namedItem(
				"duration",
			) as HTMLInputElement;
			const duration = parseLocalDuration(durationInput.value);
			if (Number.isNaN(duration)) {
				durationRef.current?.focus({ preventScroll: true });
				return;
			}
			submittedValue.duration_ms = duration;
			delete submittedValue.durationInput;
		}
		if (state.type === "releases") {
			for (let i = 0; i < tracks.length; i++) {
				const duration = parseLocalDuration(
					tracks[i].durationInput ??
						formatLocalDuration(tracks[i].durationOverrideMs),
				);
				if (Number.isNaN(duration)) {
					trackDurationRefs.current[i]?.focus({ preventScroll: true });
					return;
				}
				submittedValue.tracks![i].durationOverrideMs = duration;
				delete submittedValue.tracks![i].durationInput;
			}
		}
		const next = cloneState(state);
		const draft = {
			...submittedValue,
			id: submittedValue.id || ids(),
			key: submittedValue.key || ids(),
			provider: "Local",
		};
		draft.genres = [...(draft.genres ?? [])];
		draft.genreNamesById = Object.fromEntries(
			(draft.genres ?? [])
				.map((id) => [
					id,
					genreOptionsById.get(id)?.name ?? draft.genreNamesById?.[id],
				])
				.filter(([, name]) => name),
		);
		if (state.type === "releases")
			draft.secondaryTypes =
				typeof draft.secondaryTypes === "string"
					? splitList(draft.secondaryTypes)
					: draft.secondaryTypes;
		if (state.creating) next.items[state.type].unshift(draft);
		else
			next.items[state.type] = next.items[state.type].map((item) =>
				item.id === draft.id ? draft : item,
			);
		next.selectedId = draft.id;
		next.draft = null;
		next.creating = false;
		next.editing = false;
		next.notice = `Демо: ${state.creating ? "запись создана" : "изменения сохранены"} в памяти страницы. API и серверная часть не вызываются.`;
		update(next);
	};
	const startEdit = () => {
		if (!selected || selected.provider !== "Local") return;
		const next = cloneState(state);
		next.editing = true;
		next.creating = false;
		next.draft = structuredClone(selected);
		update(next);
	};
	const cancel = () => {
		const next = cloneState(state);
		next.editing = false;
		next.creating = false;
		next.draft = null;
		update(next);
	};
	const detailTitle = selected
		? state.type === "artists"
			? selected.name
			: selected.title
		: "";
	const genreNames = (item: LocalProviderRecord) =>
		(item.genres ?? [])
			.map((id) => genreOptionsById.get(id)?.name ?? item.genreNamesById?.[id])
			.filter(Boolean)
			.join(", ") || "—";
	let detailFacts: Array<[string, string]> = [];
	if (selected && state.type === "artists")
		detailFacts = [
			[
				"Связанные релизы",
				state.items.releases
					.filter((release) => release.artistIds?.includes(selected.id))
					.map((release) => release.title ?? "")
					.join(", ") || "—",
			],
		];
	if (selected && state.type === "releases")
		detailFacts = [
			["Группа релизов", selected.release_group_title || "—"],
			[
				"Артисты",
				selected.artistIds?.map((id) => artistName(state, id)).join(", ") ||
					"—",
			],
			["Основной тип", selected.primaryType || "—"],
			[
				"Дополнительные типы",
				(Array.isArray(selected.secondaryTypes)
					? selected.secondaryTypes.join(", ")
					: selected.secondaryTypes) || "—",
			],
			["Язык", selected.language || "—"],
			["Позиции · носитель 1", String(selected.tracks?.length || 0)],
			["Дата", selected.release_date || "—"],
			["Страна", selected.country_code || "—"],
			["Лейбл", selected.label || "—"],
			["Каталожный номер", selected.catalog_number || "—"],
		];
	if (selected && state.type === "recordings")
		detailFacts = [
			[
				"Артисты",
				selected.artistIds?.map((id) => artistName(state, id)).join(", ") ||
					"—",
			],
			["Длительность", formatLocalDuration(selected.duration_ms) || "—"],
			["ISRC", selected.isrcs || "—"],
		];
	return (
		<main className="mx-auto max-w-none p-3">
			<header className={styles.pageHeader}>
				<p className="m-0 text-xs">Ручные записи провайдера Local</p>
				<h1 className={styles.pageTitle}>Собственный каталог</h1>
				<p className={styles.pageDescription}>
					Собственный каталог — ручные записи провайдера Local, а не форма
					создания альбома медиатеки. Поиск не создаёт и не объединяет записи;
					сначала проверьте существующие провайдеры.
				</p>
				<span className="inline-flex rounded-sm bg-[var(--raised)] px-2 py-1 text-[10px] tracking-[.06em] text-[var(--muted)]">
					МАКЕТ · В ПАМЯТИ
				</span>
			</header>
			<Tabs
				className={styles.localTabs}
				aria-label="Тип записей собственного каталога"
				selectedKey={state.type}
				onSelectionChange={(key) => {
					const type = String(key) as LocalProviderType;
					const next = cloneState(state);
					next.type = type;
					next.query = "";
					next.selectedId =
						next.items[type].find((item) => item.provider === "Local")?.id ??
						null;
					next.editing = false;
					next.creating = false;
					next.draft = null;
					update(next);
				}}
			>
				<TabList className={styles.localTabList}>
					{(Object.keys(typeInfo) as LocalProviderType[]).map((type) => (
						<Tab className={styles.localTab} key={type} id={type}>
							{typeInfo[type].label}
							<span>
								{
									state.items[type].filter((item) => item.provider === "Local")
										.length
								}
							</span>
						</Tab>
					))}
				</TabList>
				<TabPanel id={state.type} className="pt-2">
					<div className={styles.localWorkspace}>
						<section
							className={`${styles.panel} overflow-hidden rounded-lg p-0`}
						>
							<div className={styles.panelHeading}>
								<h2>{config.label}</h2>
								<span className={styles.muted}>
									{localItems.length}{" "}
									{localItems.length === 1 ? "запись" : "записей"}
								</span>
							</div>
							<SearchField
								className={styles.localSearch}
								aria-label={config.search}
								value={state.query}
								onChange={(query) => {
									const next = cloneState(state);
									next.query = query;
									const terms = query
										.normalize("NFKC")
										.toLocaleLowerCase()
										.trim()
										.split(/\s+/)
										.filter(Boolean);
									const matching = localItems.filter((item) => {
										const text =
											`${item.name ?? ""} ${item.title ?? ""} ${item.release_group_title ?? ""} ${item.key}`
												.normalize("NFKC")
												.toLocaleLowerCase();
										return terms.every((term) => text.includes(term));
									});
									if (!matching.some((item) => item.id === next.selectedId))
										next.selectedId = matching[0]?.id ?? null;
									update(next);
								}}
							>
								<Label>{config.search}</Label>
								<Input placeholder="Имя, название или ключ провайдера" />
							</SearchField>
							<div className={styles.localListActions}>
								<Button
									className={styles.genreLibraryButton}
									onPress={startCreate}
								>
									{config.create}
								</Button>
							</div>
							<div id="local-provider-list">
								{rows.length ? (
									rows.map((item) => (
										<Button
											key={item.id}
											className={`${styles.localRow} ${item.id === state.selectedId ? styles.localRowSelected : ""}`}
											onPress={() => {
												const next = cloneState(state);
												next.selectedId = item.id;
												next.editing = false;
												next.creating = false;
												next.draft = null;
												next.notice = "";
												update(next);
											}}
										>
											<span>
												<strong>
													{state.type === "artists" ? item.name : item.title}
												</strong>
												<small>
													{state.type === "releases"
														? `${item.release_group_title} · ${item.artistIds?.map((id) => artistName(state, id)).join(", ") || "Артисты не указаны"}`
														: state.type === "recordings"
															? item.artistIds
																	?.map((id) => artistName(state, id))
																	.join(", ") || "Артисты не указаны"
															: `${genreNames(item)} · ${state.items.releases.filter((release) => release.artistIds?.includes(item.id)).length} связанных релизов`}
												</small>
											</span>
											<span className="shrink-0 text-xs text-[var(--muted)]">
												{item.provider}
											</span>
										</Button>
									))
								) : (
									<div className={styles.localEmpty}>
										Записей не найдено. Поиск ничего не создаёт автоматически.
									</div>
								)}
							</div>
						</section>
						<section
							className={`${styles.panel} overflow-hidden rounded-lg p-0`}
						>
							<div className={styles.panelHeading}>
								<h2>Запись</h2>
								<span className={styles.muted}>
									Текущие данные · без истории
								</span>
							</div>
							{(selected || state.creating) && displayedValue ? (
								<form
									ref={formRef}
									className={styles.localForm}
									onSubmit={submit}
									onKeyDown={(event) => {
										if (isForm && event.key === "Escape") {
											event.preventDefault();
											cancel();
										}
									}}
								>
									<div className={styles.localFormHeading}>
										<div>
											<h2 ref={detailHeadingRef} tabIndex={-1}>
												{state.creating ? config.create : detailTitle}
											</h2>
											<p className={styles.muted}>
												{displayedValue.provider} · {config.entity} · Ключ
												провайдера: {displayedValue.key || "будет создан"}
											</p>
										</div>
										<div className={styles.localDetailActions}>
											{selected?.provider === "Local" &&
												state.type === "releases" &&
												onMatchRelease && (
													<Button
														type="button"
														onPress={() => onMatchRelease(selected)}
													>
														Сопоставить входящие
													</Button>
												)}
											{(state.creating || selected?.provider === "Local") && (
												<Button
													className="w-[132px] shrink-0"
													type="button"
													onPress={() =>
														isForm
															? formRef.current?.requestSubmit()
															: startEdit()
													}
												>
													{state.creating
														? config.create
														: isForm
															? "Сохранить"
															: "Редактировать"}
												</Button>
											)}
											<Button
												className={`w-[132px] shrink-0 ${isForm ? "visible" : "invisible"}`}
												isDisabled={!isForm}
												aria-hidden={!isForm}
												type="button"
												onPress={cancel}
											>
												Отменить
											</Button>
										</div>
									</div>
									<p className={styles.muted}>
										{isForm
											? "Только демонстрация в памяти. Обязательные поля отмечены."
											: "Текущие данные · без истории"}
									</p>
									<fieldset
										disabled={!isForm}
										className="m-0 grid min-w-0 gap-3 border-0 p-0 [&_input:disabled]:border-transparent [&_input:disabled]:bg-transparent [&_select:disabled]:border-transparent [&_select:disabled]:bg-transparent"
									>
										{state.type === "artists" && (
											<>
												{field("Имя", "name", true)}
												{field("Имя для сортировки", "sortName")}
												{field("Уточнение", "disambiguation")}
												{genrePicker}
												{field("Теги · через запятую", "tags")}
											</>
										)}
										{state.type === "releases" && (
											<>
												{field(
													"Название группы релизов",
													"release_group_title",
													true,
												)}
												{field("Название издания", "title", true)}
												<div className={styles.localFields}>
													{field("Основной тип группы релизов", "primaryType")}
													<TextField
														className={styles.localField}
														value={
															Array.isArray(displayedValue.secondaryTypes)
																? displayedValue.secondaryTypes.join(", ")
																: (displayedValue.secondaryTypes ?? "")
														}
														onChange={(secondaryTypes) =>
															updateDraft({ secondaryTypes })
														}
													>
														<Label>Дополнительные типы · через запятую</Label>
														<Input />
													</TextField>
													{field("Язык", "language")}
													{field("Страна", "country_code")}
													{field("Дата", "release_date")}
													{field("Статус", "status")}
													{field("Упаковка", "packaging")}
													{field("Лейбл", "label")}
													{field("Каталожный номер", "catalog_number")}
													{field("Штрихкод", "barcode")}
													{field("Уточнение", "disambiguation")}
												</div>
												<small>
													Жанры релиза относятся только к релизу и не
													наследуются артистами или композициями.
												</small>
												{artistLinks}
												{trackRows}
												{metadataEditor("release")}
											</>
										)}
										{state.type === "recordings" && (
											<>
												{field("Название композиции", "title", true)}
												<TextField
													className={styles.localField}
													value={String(
														displayedValue.durationInput ??
															formatLocalDuration(displayedValue.duration_ms),
													)}
													onChange={(durationInput) =>
														updateDraft({ durationInput })
													}
												>
													<Label>
														Длительность{" "}
														<span className={styles.muted}>необязательно</span>
													</Label>
													<Input
														ref={durationRef}
														name="duration"
														aria-describedby="local-duration-error"
														aria-invalid={Boolean(
															displayedValue.durationInput !== undefined &&
																Number.isNaN(
																	parseLocalDuration(
																		displayedValue.durationInput,
																	),
																),
														)}
													/>
												</TextField>
												<small>
													Целые секунды или М:СС. Отображается как М:СС.
												</small>
												<p
													id="local-duration-error"
													className={`${styles.localNote} ${styles.localError}`}
													role="alert"
													hidden={
														!Number.isNaN(
															parseLocalDuration(
																String(
																	displayedValue.durationInput ??
																		formatLocalDuration(
																			displayedValue.duration_ms,
																		),
																),
															),
														)
													}
												>
													Проверьте длительность: используйте целые секунды или
													М:СС с секундами от 00 до 59.
												</p>
												{field("Уточнение", "disambiguation")}
												<label className={styles.localField}>
													Ненормативная лексика{" "}
													<span className={styles.muted}>необязательно</span>
													<Select
														aria-label="Ненормативная лексика"
														selectedKey={displayedValue.explicit ?? ""}
														onSelectionChange={(key) =>
															updateDraft({
																explicit: String(key ?? ""),
																metadataValues: {
																	...displayedValue.metadataValues,
																	explicitstatus: key
																		? [localAdvisory(String(key))]
																		: [],
																},
															})
														}
													>
														<Button>
															<SelectValue />
														</Button>
														<Popover>
															<ListBox>
																<ListBoxItem id="">Не указано</ListBoxItem>
																<ListBoxItem id="true">Да</ListBoxItem>
																<ListBoxItem id="false">Нет</ListBoxItem>
															</ListBox>
														</Popover>
													</Select>
												</label>
												{field("ISRC · через запятую", "isrcs")}
												{field("Теги · через запятую", "tags")}
												{artistLinks}
												{metadataEditor("recording")}
											</>
										)}
									</fieldset>
									<p
										className={`${styles.localNote} ${styles.localError}`}
										role="alert"
										aria-live="assertive"
									>
										{submitError}
									</p>
									{selected && (
										<dl className={styles.localFacts}>
											{detailFacts.map(([label, text]) => (
												<div key={label}>
													<dt>{label}</dt>
													<dd>{text}</dd>
												</div>
											))}
											<div>
												<dt>Ключ провайдера</dt>
												<dd>
													<code>{selected.key}</code>
												</dd>
											</div>
										</dl>
									)}
									<div className={styles.localNote}>
										Редактирование метаданных само по себе не запускает
										публикацию. Удаление и поведение зависимостей этим экраном
										не определяются.
									</div>
								</form>
							) : (
								<div className={styles.localEmpty}>
									Выберите запись или явно создайте новую.
								</div>
							)}
						</section>
					</div>
				</TabPanel>
			</Tabs>
			<div className={styles.localNotice} role="status" aria-live="polite">
				{state.notice ||
					"Демонстрационные изменения сохраняются в памяти этой страницы."}
			</div>
		</main>
	);
}
