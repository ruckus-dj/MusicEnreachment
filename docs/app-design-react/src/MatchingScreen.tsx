import { useState } from "react";
import type { FormEvent } from "react";
import {
	Button,
	Checkbox,
	Dialog,
	Heading,
	Modal,
} from "react-aria-components";
import type { OriginalTrack } from "./catalogData";
import {
	applyPlan,
	artistName,
	assignFile,
	createIncomingState,
	findProviderTrack,
	findTarget,
	getDraft,
	incomingFixtures,
	normalized,
	planRows,
	providerReleases,
	providerTags,
	selectedSourceId,
	sourceIds,
	tagFieldGroups,
	unassignFile,
	usableFile,
	type Assignment,
	type IncomingFiles,
	type IncomingReleases,
	type IncomingPlan,
	type IncomingState,
	type SourceFile,
} from "./incomingModel";

export type MatchingScreenProps = {
	groupId?: string;
	files?: IncomingFiles;
	releases?: IncomingReleases;
	state?: IncomingState;
	onState?: (state: IncomingState) => void;
	onReleases?: (releases: IncomingReleases) => void;
};

const duration = (seconds: unknown) =>
	Number.isFinite(Number(seconds))
		? `${Math.floor(Number(seconds) / 60)}:${String(Math.round(Number(seconds) % 60)).padStart(2, "0")}`
		: "—";
const tagValue = (tags: Record<string, string[]> | undefined, key: string) =>
	tags?.[key]?.length ? tags[key].join("; ") : "—";
export function MatchingScreen({
	groupId,
	files: givenFiles,
	releases: givenReleases,
	state: givenState,
	onState,
	onReleases,
}: MatchingScreenProps) {
	const [localState, setLocalState] = useState(createIncomingState);
	const [confirming, setConfirming] = useState(false);
	const [localReleases, setLocalReleases] = useState<IncomingReleases>(() =>
		structuredClone(incomingFixtures.releases),
	);
	const [providerQuery, setProviderQuery] = useState("");
	const [recordingQuery, setRecordingQuery] = useState("");
	const state = givenState ?? localState;
	const files = givenFiles ?? incomingFixtures.files;
	const releases = givenReleases ?? localReleases;
	const activeGroupId = groupId ?? state.groupId;
	const group = state.groups.find((item) => item.id === activeGroupId);
	if (!group)
		return (
			<main className="demo-shell">
				<h1>Входящая группа не найдена</h1>
				<a href="#incoming">К входящим</a>
			</main>
		);
	state.groupId = group.id;
	const target = findTarget(group, releases);
	if (!target)
		return (
			<main className="demo-shell">
				<h1>Цель сопоставления не найдена</h1>
				<a href="#incoming">К входящим</a>
			</main>
		);
	const tracks = target.tracks;
	const assignments = getDraft(group, files, providerReleases, releases);
	const commitState = () => {
		const next = structuredClone(state);
		givenState ? onState?.(next) : setLocalState(next);
	};
	const setReleases = (next: IncomingReleases) =>
		givenReleases ? onReleases?.(next) : setLocalReleases(next);
	const clearPreview = () => {
		state.plan = null;
		state.confirmDowngrade = false;
	};
	const selectedTrackId = tracks.some(
		(track) => track.id === state.selectedTarget,
	)
		? state.selectedTarget
		: String(tracks[0]?.id ?? "");
	const inspected =
		tracks.find((track) => track.id === selectedTrackId) ?? tracks[0];
	const assignment = assignments[String(inspected?.id)];
	const sourceIdsForTrack = sourceIds(assignment);
	const selectedFile = files[selectedSourceId(assignment) ?? ""];
	const providerTrack = findProviderTrack(assignment?.providerTrackId ?? null);
	const existingRelease =
		group.mode === "local"
			? releases.find((release) => release.id === group.releaseId)
			: releases.find((release) => release.providerId === target.id);
	const localTrack = existingRelease?.tracks.find(
		(track) => track.position === inspected?.position,
	);
	const used = new Set(
		Object.values(assignments).flatMap((item) => item.sourceIds),
	);
	const pool = group.fileIds.filter(
		(id) => files[id]?.available && !used.has(id),
	);
	const candidateList = providerReleases.filter((release) => {
		const query = normalized(providerQuery || target.title);
		return query
			.split(" ")
			.filter(Boolean)
			.every((term) =>
				normalized(
					`${release.title} ${artistName(String(release.artistId))} ${release.catalog} ${release.id} ${release.year}`,
				).includes(term),
			);
	});
	const allProviderTracks = providerReleases.flatMap((release) =>
		release.tracks.map((track) => ({ release, track })),
	);
	const matchingRecordings = (state.recordingResults ?? []).filter(
		({ release, track }) => {
			const query = normalized(recordingQuery || state.recordingQuery);
			return query
				.split(" ")
				.filter(Boolean)
				.every((term) =>
					normalized(
						`${track.title} ${artistName(String(track.artistId))} ${release.title}`,
					).includes(term),
				);
		},
	);
	const mutateAssignment = (
		callback: (draft: Record<string, Assignment>) => void,
	) => {
		callback(assignments);
		clearPreview();
		commitState();
	};
	const addSource = (fileId: string, trackId: string) => {
		const didAssign = assignFile(group, trackId, fileId, files, releases);
		state.selectedFile = null;
		state.selectedTarget = trackId;
		state.notice = didAssign
			? `К позиции добавлен вариант ${files[fileId].name}. Связь не меняет выбранный источник или публикацию.`
			: `${files[fileId]?.name ?? fileId} уже связан с этой позицией или недоступен; дополнительная связь не добавлена.`;
		clearPreview();
		commitState();
	};
	const searchProvider = (event: FormEvent<HTMLFormElement>) => {
		event.preventDefault();
	};
	const setCandidate = (id: string) => {
		group.mode = "provider";
		group.candidateId = id;
		state.selectedFile = null;
		state.selectedTarget = null;
		state.recordingTarget = null;
		state.recordingResults = null;
		state.notice =
			"Выбрано другое издание. У каждого издания собственный черновик назначений; публикации не изменены.";
		clearPreview();
		commitState();
	};
	const sourceSelection = (trackId: string, fileId: string) => {
		const item = assignments[trackId];
		if (!item) return;
		item.selectedSourceId = fileId;
		const selected = files[fileId];
		item.method = item.sourceMeta[fileId]?.method ?? item.method;
		item.confidence = selected?.confidence ?? null;
		clearPreview();
		commitState();
	};
	const openPreview = () => {
		state.plan = {
			mode: group.mode,
			groupId: group.id,
			target: structuredClone(target),
			localReleaseId: (existingRelease?.id as string | undefined) ?? null,
			assignments: structuredClone(assignments),
			inventoryVersion: state.inventoryVersion,
		} satisfies IncomingPlan;
		state.confirmDowngrade = false;
		commitState();
	};
	const preview = state.plan?.groupId === group.id ? state.plan : null;
	const previewRows = preview ? planRows(preview, files, releases) : [];
	const activeRows = previewRows.filter(
		(row) => row.assignment?.included && row.file,
	);
	const changedRows = activeRows.filter(
		(row) => row.action !== "Без изменений",
	);
	const linkedRows = previewRows.filter((row) => {
		const persisted = new Set(
			(row.track?.sourceIds as string[] | undefined) ??
				(row.track?.sourceId ? [String(row.track.sourceId)] : []),
		);
		return sourceIds(row.assignment).some(
			(id) => usableFile(files[id]) && !persisted.has(id),
		);
	});
	const stale = Boolean(
		preview && preview.inventoryVersion !== state.inventoryVersion,
	);
	const hasDowngrade = changedRows.some((row) => row.downgrade);
	const confirmPublish = () => {
		if (!preview || stale || (hasDowngrade && !state.confirmDowngrade)) return;
		setConfirming(false);
		if (preview.inventoryVersion !== state.inventoryVersion) {
			state.notice =
				"Предпросмотр устарел из-за изменения инвентаря; действие не выполнено.";
			commitState();
			return;
		}
		const nextReleases = structuredClone(releases);
		const changed = applyPlan(preview, files, nextReleases);
		state.notice = changed
			? `Демо: ${changed} позиций опубликовано.${linkedRows.length ? ` Связи ${linkedRows.length} позиций сохранены.` : ""} Невыбранные позиции и неразмеченные файлы не изменены.`
			: `Демо: связи ${linkedRows.length} позиций сохранены. Аудиофайлы и публикации не изменены.`;
		setReleases(nextReleases);
		state.plan = null;
		state.confirmDowngrade = false;
		commitState();
	};
	const tagFields = tagFieldGroups()[state.tagScope];
	const sourceTags = selectedFile?.tags ?? {};
	const remoteTags = providerTrack
		? providerTags(providerTrack.release, providerTrack.track)
		: {};
	const localTags =
		existingRelease && localTrack
			? providerTags(existingRelease, localTrack)
			: group.mode === "local"
				? providerTags(target, inspected)
				: {};
	const publication = localTrack?.published ?? undefined;
	const publicationTags = publication?.tags ?? {};
	const tagRows = tagFields.filter(
		(key) =>
			!state.differencesOnly ||
			new Set(
				[sourceTags, remoteTags, localTags, publicationTags].map((tags) =>
					JSON.stringify(tags[key] ?? []),
				),
			).size > 1,
	);
	const bindRecording = (recordingId: string) => {
		const item = assignments[String(state.recordingTarget)];
		if (!item) {
			state.notice = "Сначала назначьте аудиофайл этой локальной позиции.";
			commitState();
			return;
		}
		item.providerTrackId = recordingId;
		item.method = "Вручную · композиция";
		if (item.selectedSourceId && item.sourceMeta[item.selectedSourceId])
			item.sourceMeta[item.selectedSourceId].method = item.method;
		state.selectedTarget = state.recordingTarget;
		state.recordingTarget = null;
		state.notice =
			"Связь с композицией добавлена в черновик. Название и порядок локального сборника сохранены.";
		clearPreview();
		commitState();
	};
	const fileButton = (id: string, compact = false) => {
		const file = files[id];
		if (!file) return null;
		return (
			<button
				key={id}
				type="button"
				className={`incoming-file ${state.selectedFile === id ? "selected" : ""}`}
				draggable
				onDragStart={(event) => {
					event.dataTransfer.setData("text/plain", id);
					event.dataTransfer.effectAllowed = "copy";
				}}
				onClick={() => {
					state.selectedFile = state.selectedFile === id ? null : id;
					commitState();
				}}
				aria-pressed={state.selectedFile === id}
			>
				<strong>{file.name}</strong>
				<span>
					{file.codec} · {duration(file.duration)}
					{compact ? "" : ` · ${file.path}`}
				</span>
			</button>
		);
	};
	return (
		<main className="demo-shell matching-screen" onKeyDown={(event) => { if (event.key === "Escape" && state.selectedFile) { state.selectedFile = null; commitState(); } }}>
			<header>
				<p className="demo-eyebrow">
					<a href="#incoming">Все входящие</a> · Только чтение: /mnt/inbox/
					{group.id}
				</p>
				<h1>Разбор: {group.title}</h1>
				<p>
					{group.mode === "local"
						? "Локальные позиции; композиции можно брать из разных альбомов."
						: "Выбранное издание → его позиции. Наши файлы находятся отдельно справа."}
				</p>
			</header>
			{state.notice && (
				<p role="status" className="demo-notice">
					{state.notice}
				</p>
			)}
			<div className="incoming-summary">
				<strong>
					{Object.keys(assignments).length}/{tracks.length} назначено
				</strong>
				<span>{pool.length} файлов свободно</span>
				<span>
					{group.mode === "local"
						? "Локальный порядок"
						: `${target.year ?? ""} · ${target.country ?? ""} · ${target.catalog ?? ""}`}
				</span>
			</div>
			<div className="matching-layout">
				<section className="matching-candidates">
					<h2>Найти издание</h2>
					<form onSubmit={searchProvider}>
						<label htmlFor="provider-query">Поиск изданий · текстом</label>
						<div className="matching-search">
							<input
								id="provider-query"
								value={providerQuery}
								onChange={(event) =>
									setProviderQuery(event.currentTarget.value)
								}
								placeholder="Артист, альбом, каталог…"
							/>
							<Button type="submit">Найти</Button>
						</div>
					</form>
					<p>Локальные демонстрационные данные MusicBrainz, не реальный API.</p>
					<div className="candidate-list">
						{candidateList.map((candidate) => (
							<Button
								key={String(candidate.id)}
								className={`candidate-button ${group.mode === "provider" && group.candidateId === candidate.id ? "selected" : ""}`}
								aria-pressed={
									group.mode === "provider" &&
									group.candidateId === candidate.id
								}
								onPress={() => setCandidate(String(candidate.id))}
							>
								<strong>{String(candidate.title)}</strong>
								<span>
									{artistName(String(candidate.artistId))} ·{" "}
									{String(candidate.year)} · {String(candidate.country)}
								</span>
								<span>
									{candidate.tracks.length} позиций ·{" "}
									{String(candidate.catalog)}
								</span>
								<code>{String(candidate.id)}</code>
							</Button>
						))}
					</div>
					<Button
						className="demo-button"
						aria-pressed={group.mode === "local"}
						onPress={() => {
							group.mode = "local";
							state.selectedTarget = null;
							state.selectedFile = null;
							state.recordingTarget = null;
							clearPreview();
							commitState();
						}}
					>
						Локальный релиз / сборник
					</Button>
				</section>
				<section className="matching-target">
					<h2>
						{String(target.title)} · {tracks.length} позиций
					</h2>
					<div className="matching-table-wrap">
						<table>
							<thead>
								<tr>
									<th>✓</th>
									<th>№</th>
									<th>Композиция</th>
									<th>Длит.</th>
									<th>Наш файл / источники</th>
									<th>Оценка / доказательства</th>
								</tr>
							</thead>
							<tbody>
								{tracks.map((track) => {
									const id = String(track.id);
									const item = assignments[id];
									const ids = sourceIds(item);
									const currentId = selectedSourceId(item);
									const file = files[currentId ?? ""];
									const bound =
										group.mode === "local"
											? findProviderTrack(item?.providerTrackId ?? null)
											: null;
									return (
										<tr
											key={id}
											className={id === selectedTrackId ? "inspected" : ""}
											onDragOver={(event) => event.preventDefault()}
											onDrop={(event) => {
												event.preventDefault();
												addSource(event.dataTransfer.getData("text/plain"), id);
											}}
										>
											<td>
												<Checkbox
													aria-label={`Публиковать ${String(track.title)}`}
													isSelected={Boolean(
														item?.included && usableFile(file),
													)}
													isDisabled={!usableFile(file)}
													onChange={(checked) => {
														if (item) item.included = checked;
														clearPreview();
														commitState();
													}}
												>
													<span aria-hidden="true">
														{item?.included && usableFile(file) ? "✓" : ""}
													</span>
												</Checkbox>
											</td>
											<td>{String(track.position).padStart(2, "0")}</td>
											<td>
												<button
													className="text-button"
													onClick={() => {
														state.selectedTarget = id;
														state.inspector = "tags";
														commitState();
													}}
												>
													{String(track.title)}
												</button>
												<span className="incoming-sub">
													{artistName(String(track.artistId))}
													{group.mode === "local"
														? ` · ${bound?.release.title ?? "Без связи с провайдером"}`
														: ""}
												</span>
											</td>
											<td>{duration(track.duration)}</td>
											<td>
												{ids.length ? (
													<div className="linked-sources">
														{ids.map((sourceId) => (
															<div key={sourceId} className="linked-source">
																<label>
																	<input
																		type="radio"
																		name={`selected-${id}`}
																		value={sourceId}
																		checked={currentId === sourceId}
																		onChange={() =>
																			sourceSelection(id, sourceId)
																		}
																		aria-label={`Выбрать ${files[sourceId]?.name ?? sourceId} для публикации`}
																	/>
																	{fileButton(sourceId, true)}
																</label>
																<Button
																	aria-label={`Снять связь ${files[sourceId]?.name ?? sourceId} с ${String(track.title)}`}
																	onPress={() =>
																		mutateAssignment((draft) =>
																			unassignFile(draft, id, sourceId),
																		)
																	}
																>
																	×
																</Button>
															</div>
														))}
													</div>
												) : (
													<span>Перетащите файл сюда</span>
												)}
												{state.selectedFile && (
													<Button
														onPress={() =>
															addSource(state.selectedFile as string, id)
														}
													>
														Добавить вариант
													</Button>
												)}
											</td>
											<td>
												{file ? (
													<>
														<span>
															{item?.method.startsWith("Вручную")
																? "Вручную"
																: file.confidence === "high"
																	? "Высокий*"
																	: "Низкий*"}
														</span>
														<Button
															className="demo-button"
															onPress={() => {
																state.selectedTarget = id;
																state.inspector = "evidence";
																commitState();
															}}
														>
															Доказательства
														</Button>
													</>
												) : (
													"Нет файла"
												)}
												{group.mode === "local" && (
													<Button
														onPress={() => {
															state.recordingTarget = id;
															state.recordingQuery = String(track.title);
															state.recordingResults = allProviderTracks;
															setRecordingQuery(String(track.title));
															commitState();
														}}
													>
														Найти композицию
													</Button>
												)}
											</td>
										</tr>
									);
								})}
							</tbody>
						</table>
					</div>
					<p>
						* Политика matching-rapidfuzz-v1: порог ≥0.70. Этот макет числовую
						уверенность не рассчитывает; уровни — демонстрационные.
					</p>
				</section>
				<section className="matching-pool">
					<h2>Наши файлы · {pool.length} свободно</h2>
					{pool.length ? (
						pool.map((id) => fileButton(id))
					) : (
						<p>Все файлы назначены.</p>
					)}
					<p>
						Выберите файл → «Добавить вариант» или перетащите на позицию. Это не
						загрузка файлов с компьютера.
					</p>
				</section>
			</div>
			{state.recordingTarget && (
				<section className="matching-recording">
					<h2>Поиск композиции</h2>
					<form
						onSubmit={(event) => {
							event.preventDefault();
							state.recordingQuery = recordingQuery;
							state.recordingResults = allProviderTracks;
							commitState();
						}}
					>
						<label htmlFor="recording-query">Название / артист</label>
						<input
							id="recording-query"
							value={recordingQuery}
							onChange={(event) => setRecordingQuery(event.currentTarget.value)}
						/>
						<Button type="submit">Найти композиции</Button>
						<Button
							onPress={() => {
								state.recordingTarget = null;
								commitState();
							}}
						>
							Закрыть
						</Button>
					</form>
					<p>
						Связывается только композиция. Название сборника, его год, порядок и
						локальные позиции не заменяются чужим альбомом.
					</p>
					<ul>
						{matchingRecordings.map(({ release, track }) => (
							<li key={String(track.id)}>
								{String(track.title)} · {String(release.title)} ·{" "}
								{duration(track.duration)}{" "}
								<Button onPress={() => bindRecording(String(track.id))}>
									Связать композицию
								</Button>
							</li>
						))}
					</ul>
				</section>
			)}
			<section className="matching-inspector">
				<h2>Инспектор · {String(inspected.title)}</h2>
				<div className="inspector-tabs">
					<Button
						aria-pressed={state.inspector === "tags"}
						onPress={() => {
							state.inspector = "tags";
							commitState();
						}}
					>
						Сравнение тегов
					</Button>
					<Button
						aria-pressed={state.inspector === "evidence"}
						onPress={() => {
							state.inspector = "evidence";
							commitState();
						}}
					>
						Почему такое совпадение
					</Button>
				</div>
				{state.inspector === "tags" ? (
					<>
						<div className="tag-controls">
							<div aria-label="Область тегов">
								{(["artist", "release", "track"] as const).map((scope) => (
									<Button
										key={scope}
										aria-pressed={state.tagScope === scope}
										onPress={() => {
											state.tagScope = scope;
											commitState();
										}}
									>
										{scope === "artist"
											? "Артист"
											: scope === "release"
												? "Альбом"
												: "Трек"}
									</Button>
								))}
							</div>
							<Checkbox
								isSelected={state.differencesOnly}
								onChange={(checked) => {
									state.differencesOnly = checked;
									commitState();
								}}
							>
								Только отличия
							</Checkbox>
						</div>
						<div className="matching-table-wrap">
							<table>
								<thead>
									<tr>
										<th>Поле</th>
										<th>Исходный файл</th>
										<th>Провайдер · кэш</th>
										<th>Локальная медиатека</th>
										<th>Сейчас в публикации</th>
									</tr>
								</thead>
								<tbody>
									{tagRows.map((key) => (
										<tr key={key}>
											<th>{key}</th>
											<td>{tagValue(sourceTags, key)}</td>
											<td>
												{providerTrack
													? tagValue(remoteTags, key)
													: "Нет связи"}
											</td>
											<td>{tagValue(localTags, key)}</td>
											<td>
												{publication
													? tagValue(publicationTags, key)
													: "Не опубликован"}
											</td>
										</tr>
									))}
								</tbody>
							</table>
						</div>
						<p>
							{selectedFile?.path ?? "Исходник не выбран"} ·{" "}
							{publication
								? `Публикация: ${String(publication.path)} · чтение ${String(publication.readAt)}`
								: "Публикация отсутствует; текущие метаданные не подставляются."}
						</p>
					</>
				) : (
					<Evidence
						file={selectedFile}
						track={inspected}
						assignment={assignment}
					/>
				)}
			</section>
			<div className="matching-commit">
				<strong>
					{
						Object.values(assignments).filter(
							(item) =>
								item.included &&
								usableFile(files[selectedSourceId(item) ?? ""]),
						).length
					}{" "}
					позиций выбрано для публикации
				</strong>
				<span>
					{tracks.length - Object.keys(assignments).length} пустых позиций не
					блокируют публикацию. {pool.length} файлов останутся неразмеченными во
					входящих.
				</span>
				<Button
					className="demo-button"
					isDisabled={
						!Object.values(assignments).some(
							(item) =>
								item.included &&
								usableFile(files[selectedSourceId(item) ?? ""]),
						)
					}
					onPress={openPreview}
				>
					Предпросмотр публикации
				</Button>
			</div>
			{preview && (
				<section
					className="matching-preview"
					aria-label="Предпросмотр публикации"
				>
					<h2>Предпросмотр: {String(preview.target.title)}</h2>
					<p>
						Контекстный предпросмотр операции. Исходники сохранятся; невыбранные
						публикации и неразмеченные файлы не изменяются.
					</p>
					{stale && (
						<p role="alert">
							Инвентарь источников изменился. Создайте новый предпросмотр перед
							подтверждением.
						</p>
					)}
					<p>
						{activeRows.length} / {previewRows.length} выбрано ·{" "}
						{changedRows.length} изменений · {linkedRows.length} позиций:
						сохранить связи ·{" "}
						{group.fileIds.length -
							new Set(activeRows.map((row) => row.file?.id)).size}{" "}
						файлов останутся во входящих · MKA · перепаковка без перекодирования
					</p>
					<div className="matching-table-wrap">
						<table>
							<thead>
								<tr>
									<th>Включить</th>
									<th>№ / трек</th>
									<th>Источник</th>
									<th>Сейчас опубликовано</th>
									<th>Будет записано</th>
									<th>Действие</th>
								</tr>
							</thead>
							<tbody>
								{previewRows.map((row) => (
									<tr key={String(row.targetTrack.id)}>
										<td>
											<Checkbox
												aria-label={`Включить ${String(row.targetTrack.title)}`}
												isSelected={Boolean(
													row.assignment?.included && row.file,
												)}
												isDisabled={!row.file}
												onChange={(checked) => {
													if (row.assignment) row.assignment.included = checked;
													commitState();
												}}
											>
												<span>{row.assignment?.included ? "✓" : ""}</span>
											</Checkbox>
										</td>
										<td>
											{String(row.targetTrack.position)} ·{" "}
											{String(row.targetTrack.title)}
										</td>
										<td>
											{row.file
												? `${row.file.name} · ${row.file.codec}`
												: "Нет файла"}
										</td>
										<td>
											{row.previous
												? `${String(row.previous.codec)} · ${tagValue(row.previous.tags as Record<string, string[]>, "TITLE")}`
												: "Нет публикации"}
										</td>
										<td>
											{tagValue(row.nextTags, "TITLE")} ·{" "}
											{tagValue(row.nextTags, "ALBUM")}
										</td>
										<td>
											{row.action}
											{row.downgrade ? " · Понижение качества" : ""}
										</td>
									</tr>
								))}
							</tbody>
						</table>
					</div>
					{hasDowngrade && (
						<Checkbox
							isSelected={state.confirmDowngrade}
							onChange={(checked) => {
								state.confirmDowngrade = checked;
								commitState();
							}}
						>
							Подтверждаю замену сжатия без потерь на сжатие с потерями
						</Checkbox>
					)}
					<div className="incoming-actions">
						<Button
							isDisabled={
								stale ||
								(!changedRows.length && !linkedRows.length) ||
								(hasDowngrade && !state.confirmDowngrade)
							}
							onPress={() => setConfirming(true)}
						>
							Подтвердить публикацию
						</Button>
						<Button
							onPress={() => {
								state.plan = null;
								commitState();
							}}
						>
							Вернуться к объекту
						</Button>
					</div>
				</section>
			)}
			<Modal isOpen={confirming} onOpenChange={setConfirming} isDismissable>
				<Dialog aria-labelledby="incoming-confirm-title">
					<Heading slot="title" id="incoming-confirm-title">
						{changedRows.length
							? `Опубликовать ${changedRows.length} позиций?`
							: `Сохранить связи ${linkedRows.length} позиций?`}
					</Heading>
					<p>
						Невыбранные позиции, публикации и исходники сохраняются.
						Демонстрация не выполняет файловые операции.
					</p>
					{stale && (
						<p role="alert">
							Инвентарь источников изменился. Пересмотрите предпросмотр.
						</p>
					)}
					<Button onPress={() => setConfirming(false)}>Отмена</Button>
					<Button
						isDisabled={stale || (hasDowngrade && !state.confirmDowngrade)}
						onPress={confirmPublish}
					>
						Подтвердить
					</Button>
				</Dialog>
			</Modal>
		</main>
	);
}

function Evidence({
	file,
	track,
	assignment,
}: {
	file?: SourceFile;
	track: OriginalTrack;
	assignment?: Assignment;
}) {
	if (!file)
		return <p>Выберите строку и файл: здесь будут сравнения признаков.</p>;
	const rawTitle = file.tags.TITLE?.join("; ") ?? "";
	const rawArtist = file.tags.ARTIST?.join("; ") ?? "";
	const compare = (source: string, remote: string) =>
		normalized(source) === normalized(remote)
			? "Совпало после нормализации"
			: "Различается";
	return (
		<>
			<p>
				{assignment?.method.startsWith("Вручную")
					? "Назначено оператором"
					: file.confidence === "high"
						? "Высокая уверенность · демонстрационные данные"
						: "Недостаточная уверенность · демонстрационные данные"}
			</p>
			<strong>Числовая оценка: не рассчитана</strong>
			<div className="matching-table-wrap">
				<table>
					<thead>
						<tr>
							<th>Признак</th>
							<th>Исходное значение</th>
							<th>Кандидат</th>
							<th>Результат / отличие</th>
							<th>Вес / вклад</th>
						</tr>
					</thead>
					<tbody>
						<tr>
							<th>Название</th>
							<td>{rawTitle}</td>
							<td>{String(track.title)}</td>
							<td>{compare(rawTitle, String(track.title))}</td>
							<td>Не моделируется</td>
						</tr>
						<tr>
							<th>Артист</th>
							<td>{rawArtist}</td>
							<td>{artistName(String(track.artistId))}</td>
							<td>{compare(rawArtist, artistName(String(track.artistId)))}</td>
							<td>Не моделируется</td>
						</tr>
						<tr>
							<th>Длительность</th>
							<td>{duration(file.duration)}</td>
							<td>{duration(track.duration)}</td>
							<td>{Number(file.duration) - Number(track.duration)} с</td>
							<td>Не моделируется</td>
						</tr>
						<tr>
							<th>Номер позиции</th>
							<td>{tagValue(file.tags, "TRACKNUMBER")}</td>
							<td>{String(track.position)}</td>
							<td>Демонстрационное сравнение</td>
							<td>Не моделируется</td>
						</tr>
						<tr>
							<th>MBID композиции</th>
							<td>{tagValue(file.tags, "MUSICBRAINZ_TRACKID")}</td>
							<td>{String(track.recordingId ?? "Не указан")}</td>
							<td>
								В исходнике нет MBID; совпадение не подтверждено идентификатором
							</td>
							<td>Не моделируется</td>
						</tr>
						<tr>
							<th>Отпечаток</th>
							<td>{file.fingerprint ?? "Нет результата"}</td>
							<td>AcoustID / Chromaprint</td>
							<td>Не используется как доказанное совпадение</td>
							<td>Не моделируется</td>
						</tr>
					</tbody>
				</table>
			</div>
			<p>
				Показаны сравнения демонстрационных значений. Нормализация — визуальная
				демонстрация, не расчёт политики сопоставления.
			</p>
		</>
	);
}
