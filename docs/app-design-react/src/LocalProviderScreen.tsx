import { useEffect, useRef, useState, type FormEvent } from "react";
import { Button } from "react-aria-components";
import {
	createLocalProviderState,
	formatLocalDuration,
	parseLocalDuration,
	type LocalProviderRecord,
	type LocalProviderState,
	type LocalProviderTrack,
	type LocalProviderType,
} from "./localProviderModel";
import { demoGenres } from "./GenreLibrary";

export type LocalProviderScreenProps = {
	state?: LocalProviderState;
	onState?: (state: LocalProviderState) => void;
	genresLoaded?: boolean;
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

export function LocalProviderScreen({
	state: givenState,
	onState,
	genresLoaded = false,
}: LocalProviderScreenProps) {
	const [localState, setLocalState] = useState(createLocalProviderState);
	const [genreQuery, setGenreQuery] = useState("");
	const state = givenState ?? localState;
	const focusRef = useRef<HTMLInputElement>(null);
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
	const currentGenres = value?.genres ?? [];
	const updateDraft = (change: Partial<LocalProviderRecord>) => {
		const next = cloneState(state);
		next.draft = { ...(next.draft ?? ({} as LocalProviderRecord)), ...change };
		update(next);
	};
	const field = (
		label: string,
		name: keyof LocalProviderRecord,
		required = false,
		initial = "",
	) => (
		<label className="field">
			{label}{" "}
			{required ? (
				<span className="required">Обязательно</span>
			) : (
				<span className="muted">необязательно</span>
			)}
			<input
				ref={
					name === "name" || name === "release_group_title" || name === "title"
						? focusRef
						: undefined
				}
				name={name}
				required={required}
				value={String(value?.[name] ?? initial)}
				onChange={(event) => updateDraft({ [name]: event.target.value })}
			/>
		</label>
	);
	const genrePicker = (
		<fieldset className="genre-picker" disabled={!genresLoaded}>
			<legend>Жанры · MusicBrainz</legend>
			{!genresLoaded ? (
				<p className="muted">
					Каталог недоступен. Можно сохранить запись без жанров.
				</p>
			) : (
				<>
					<label className="field">
						Поиск по каталогу
						<input
							type="search"
							placeholder="Начните вводить название"
							value={genreQuery}
							onChange={(event) => setGenreQuery(event.currentTarget.value)}
						/>
					</label>
					<div className="genre-picker-options">
						{demoGenres
							.filter(
								(genre) =>
									!currentGenres.includes(genre.id) &&
									genre.name
										.toLocaleLowerCase()
										.includes(genreQuery.toLocaleLowerCase()),
							)
							.map((genre) => (
								<Button
									key={genre.id}
									data-genre-option
									onPress={() =>
										updateDraft({ genres: [...currentGenres, genre.id] })
									}
								>
									{genre.name}
								</Button>
							))}
					</div>
					<div className="genre-picker-selected">
						{currentGenres.length ? (
							currentGenres.map((id) => (
								<span className="genre-chip" key={id}>
									{demoGenres.find((genre) => genre.id === id)?.name ??
										value?.genreNamesById?.[id] ??
										id}
									<Button
										aria-label={`Убрать ${id}`}
										onPress={() =>
											updateDraft({
												genres: currentGenres.filter(
													(genreId) => genreId !== id,
												),
											})
										}
									>
										×
									</Button>
								</span>
							))
						) : (
							<span className="muted">Жанры не выбраны.</span>
						)}
					</div>
					<small>
						Только канонические ID и названия из загруженного каталога;
						произвольный ввод не добавляет значение.
					</small>
				</>
			)}
		</fieldset>
	);
	const artistLinks = (
		<fieldset className="local-provider-links">
			<legend>
				{state.type === "releases"
					? "Артисты релиза · ссылки на существующих артистов"
					: "Исполнители · ссылки на существующих артистов"}
			</legend>
			{state.items.artists.map((artist) => (
				<label className="local-provider-option" key={artist.id}>
					<input
						type="checkbox"
						checked={value?.artistIds?.includes(artist.id) ?? false}
						onChange={(event) =>
							updateDraft({
								artistIds: event.target.checked
									? [...(value?.artistIds ?? []), artist.id]
									: (value?.artistIds ?? []).filter((id) => id !== artist.id),
							})
						}
					/>
					<span>
						{artist.name}{" "}
						<span className="muted">
							· {artist.provider}
							{artist.provider !== "Local"
								? " · внешняя ссылка, только чтение"
								: ""}{" "}
							· <code>{artist.key}</code>
						</span>
					</span>
				</label>
			))}
			<small>
				Выбор сохраняет принадлежность провайдеру и исходный ключ; копия записи
				не создаётся.
			</small>
		</fieldset>
	);
	const tracks = value?.tracks ?? [];
	const changeTrack = (index: number, change: Partial<LocalProviderTrack>) =>
		updateDraft({
			tracks: tracks.map((track, i) =>
				i === index ? { ...track, ...change } : track,
			),
		});
	const trackRows = (
		<div className="local-provider-tracklist">
			<div className="local-provider-tracklist-heading">
				<strong>Носитель 1 · позиции</strong>
				<Button
					onPress={() =>
						updateDraft({
							tracks: [
								...tracks,
								{
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
						<fieldset
							className="local-provider-track"
							key={`${index}-${track.recordingId}`}
						>
							<legend>Позиция {index + 1}</legend>
							<div className="local-provider-track-actions">
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
							<label className="field">
								Существующая композиция
								<select
									required
									value={track.recordingId}
									onChange={(event) =>
										changeTrack(index, { recordingId: event.target.value })
									}
								>
									<option value="">Выберите композицию…</option>
									{state.items.recordings.map((candidate) => (
										<option key={candidate.id} value={candidate.id}>
											{candidate.title} · {candidate.provider} · {candidate.key}
										</option>
									))}
								</select>
							</label>
							<p className="local-provider-track-default">
								По умолчанию:{" "}
								<strong>{recording?.title || "Композиция не выбрана"}</strong> ·{" "}
								{formatLocalDuration(recording?.duration_ms) ||
									"длительность не указана"}{" "}
								·{" "}
								{recording?.artistIds
									?.map((id) => artistName(state, id))
									.join(", ") || "артисты не указаны"}
							</p>
							<div className="local-provider-fields">
								<label className="field">
									Название в этой позиции · своё значение
									<input
										value={track.titleOverride}
										onChange={(event) =>
											changeTrack(index, { titleOverride: event.target.value })
										}
									/>
								</label>
								<label className="field">
									Длительность · своё значение
									<input
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
										value={
											track.durationInput ??
											formatLocalDuration(track.durationOverrideMs)
										}
										onChange={(event) =>
											changeTrack(index, { durationInput: event.target.value })
										}
									/>
								</label>
							</div>
							<small>
								Номер позиции определяется порядком строки. Длительность: целые
								секунды или М:СС.
							</small>
							<p
								id={`track-duration-error-${index}`}
								className="note error"
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
				<p className="empty">
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
		draft.genres = genresLoaded
			? [...(draft.genres ?? [])].filter((id) =>
					demoGenres.some((genre) => genre.id === id),
				)
			: (selected?.genres ?? []);
		draft.genreNamesById = Object.fromEntries(
			(draft.genres ?? [])
				.map((id) => [
					id,
					demoGenres.find((genre) => genre.id === id)?.name ??
						draft.genreNamesById?.[id],
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
			.map(
				(id) =>
					demoGenres.find((genre) => genre.id === id)?.name ??
					item.genreNamesById?.[id],
			)
			.filter(Boolean)
			.join(", ") || "—";
	let detailFacts: Array<[string, string]> = [];
	if (selected && state.type === "artists")
		detailFacts = [
			["Жанры", genreNames(selected)],
			["Теги", selected.tags || "—"],
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
			["Жанры", genreNames(selected)],
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
			["Жанры", genreNames(selected)],
		];
	const isForm = state.editing || state.creating;
	return (
		<main className="demo-shell local-provider-screen">
			<header>
				<p className="demo-eyebrow">Ручные записи провайдера Local</p>
				<h1>Собственный каталог</h1>
				<p>
					Собственный каталог — ручные записи провайдера Local, а не форма
					создания альбома медиатеки. Поиск не создаёт и не объединяет записи;
					сначала проверьте существующие провайдеры.
				</p>
				<span className="demo-label">МАКЕТ · В ПАМЯТИ</span>
			</header>
			<div
				className="local-provider-tabs"
				role="tablist"
				aria-label="Тип записей собственного каталога"
			>
				{(Object.keys(typeInfo) as LocalProviderType[]).map((type) => (
					<button
						type="button"
						key={type}
						role="tab"
						aria-selected={state.type === type}
						tabIndex={state.type === type ? 0 : -1}
						onKeyDown={(event) => {
							if (
								!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)
							)
								return;
							event.preventDefault();
							const tabs = [
								...event.currentTarget.parentElement!.querySelectorAll<HTMLButtonElement>(
									'[role="tab"]',
								),
							];
							const index = tabs.indexOf(event.currentTarget);
							const next =
								event.key === "Home"
									? 0
									: event.key === "End"
										? tabs.length - 1
										: (index +
												(event.key === "ArrowRight" ? 1 : -1) +
												tabs.length) %
											tabs.length;
							tabs[next].click();
							tabs[next].focus();
						}}
						onClick={() => {
							const next = cloneState(state);
							next.type = type;
							next.query = "";
							next.selectedId =
								next.items[type].find((item) => item.provider === "Local")
									?.id ?? null;
							next.editing = false;
							next.creating = false;
							next.draft = null;
							update(next);
						}}
					>
						{typeInfo[type].label}
						<span>
							{
								state.items[type].filter((item) => item.provider === "Local")
									.length
							}
						</span>
					</button>
				))}
			</div>
			<div className="local-provider-workspace">
				<section className="demo-panel local-provider-catalog">
					<div className="panel-heading">
						<h2>{config.label}</h2>
						<span className="muted">
							{localItems.length}{" "}
							{localItems.length === 1 ? "запись" : "записей"}
						</span>
					</div>
					<label className="local-provider-search">
						{config.search}
						<input
							type="search"
							placeholder="Имя, название или ключ провайдера"
							value={state.query}
							onChange={(event) => {
								const next = cloneState(state);
								const query = event.target.value;
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
						/>
					</label>
					<div className="local-provider-list-actions">
						<Button className="demo-button" onPress={startCreate}>
							{config.create}
						</Button>
					</div>
					<div id="local-provider-list">
						{rows.length ? (
							rows.map((item) => (
								<Button
									key={item.id}
									className={`local-provider-row ${item.id === state.selectedId ? "selected" : ""}`}
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
									<span className="local-provider-origin">{item.provider}</span>
								</Button>
							))
						) : (
							<div className="empty">
								Записей не найдено. Поиск ничего не создаёт автоматически.
							</div>
						)}
					</div>
				</section>
				<section className="demo-panel local-provider-editor">
					<div className="panel-heading">
						<h2>Запись</h2>
						<span className="muted">Текущие данные · без истории</span>
					</div>
					{isForm && value ? (
						<form className="local-provider-form" onSubmit={submit}>
							<div className="local-provider-form-heading">
								<h2>
									{state.creating
										? config.create
										: `Редактировать ${config.entity.toLocaleLowerCase()}`}
								</h2>
								<p className="muted">
									Только демонстрация в памяти. Обязательные поля отмечены.
								</p>
							</div>
							{state.type === "artists" && (
								<>
									{field("Имя", "name", true)}
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
									<div className="local-provider-fields">
										{field("Основной тип группы релизов", "primaryType")}
										<label className="field">
											Дополнительные типы · через запятую
											<input
												value={
													Array.isArray(value.secondaryTypes)
														? value.secondaryTypes.join(", ")
														: (value.secondaryTypes ?? "")
												}
												onChange={(event) =>
													updateDraft({ secondaryTypes: event.target.value })
												}
											/>
										</label>
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
									{genrePicker}
									<small>
										Жанры релиза относятся только к релизу и не наследуются
										артистами или композициями.
									</small>
									{artistLinks}
									{trackRows}
									<p className="muted">Система письма не указывается.</p>
								</>
							)}
							{state.type === "recordings" && (
								<>
									{field("Название композиции", "title", true)}
									<label className="field">
										Длительность <span className="muted">необязательно</span>
										<input
											ref={durationRef}
											name="duration"
											aria-describedby="local-duration-error"
											aria-invalid={Boolean(
												value.durationInput !== undefined &&
													Number.isNaN(parseLocalDuration(value.durationInput)),
											)}
											value={String(
												value.durationInput ??
													formatLocalDuration(value.duration_ms),
											)}
											onChange={(event) =>
												updateDraft({ durationInput: event.target.value })
											}
										/>
									</label>
									<small id="local-duration-hint">
										Целые секунды или М:СС. Отображается как М:СС.
									</small>
									<p
										id="local-duration-error"
										className="note error"
										role="alert"
										hidden={
											!Number.isNaN(
												parseLocalDuration(
													String(
														value.durationInput ??
															formatLocalDuration(value.duration_ms),
													),
												),
											)
										}
									>
										Проверьте длительность: используйте целые секунды или М:СС с
										секундами от 00 до 59.
									</p>
									{field("Уточнение", "disambiguation")}
									<label className="field">
										Ненормативная лексика{" "}
										<span className="muted">необязательно</span>
										<select
											value={value.explicit ?? ""}
											onChange={(event) =>
												updateDraft({ explicit: event.target.value })
											}
										>
											<option value="">Не указано</option>
											<option value="true">Да</option>
											<option value="false">Нет</option>
										</select>
									</label>
									{field("ISRC · через запятую", "isrcs")}
									{genrePicker}
									{field("Теги · через запятую", "tags")}
									{artistLinks}
								</>
							)}
							<div className="demo-object-actions">
								<Button type="button" onPress={cancel}>
									Отмена
								</Button>
								<Button type="submit" className="demo-button">
									{state.creating ? config.create : "Сохранить изменения"}
								</Button>
							</div>
						</form>
					) : selected ? (
						<>
							<div className="local-provider-detail-heading">
								<div>
									<span className="eyebrow">
										{selected.provider} · {config.entity}
									</span>
									<h2 ref={detailHeadingRef} tabIndex={-1}>
										{detailTitle}
									</h2>
								</div>
								{selected.provider === "Local" && (
									<Button onPress={startEdit}>Редактировать</Button>
								)}
							</div>
							<dl className="local-provider-facts">
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
							<div className="note">
								Редактирование метаданных само по себе не запускает публикацию.
								Удаление и поведение зависимостей этим экраном не определяются.
							</div>
						</>
					) : (
						<div className="empty">
							Выберите запись или явно создайте новую.
						</div>
					)}
				</section>
			</div>
			<div className="local-provider-notice" role="status" aria-live="polite">
				{state.notice ||
					"Демонстрационные изменения сохраняются в памяти этой страницы."}
			</div>
		</main>
	);
}
