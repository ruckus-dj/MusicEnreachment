import { useState } from "react";
import type { FormEvent } from "react";
import {
	Button,
	Checkbox,
	Dialog,
	Heading,
	Input,
	Modal,
	Radio,
	RadioGroup,
	Tab,
	TabList,
	TabPanel,
	Tabs,
	TextField,
} from "./ui";
import {
	publicationMetadataIssues,
	type CatalogArtist,
	type OriginalTrack,
} from "./catalogData";
import { MatchingSearchPicker } from "./MatchingSearchPicker";
import { MetadataDiff, metadataSnapshotDiff } from "./publicationMetadataDiff";
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
import {
	comparisonTableWrap,
	dialogFooter,
	previewTable,
	previewTableWrap,
	surfacePanel,
	table,
} from "./matchingStyles";

export type MatchingScreenProps = {
	destination?: import("./publicationOutput").PublicationDestination;
	groupId?: string;
	files?: IncomingFiles;
	releases?: IncomingReleases;
	providers?: IncomingReleases;
	artists?: CatalogArtist[];
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
	destination,
	groupId,
	files: givenFiles,
	releases: givenReleases,
	providers: givenProviders,
	artists: givenArtists,
	state: givenState,
	onState,
	onReleases,
}: MatchingScreenProps) {
	const [localState, setLocalState] = useState(createIncomingState);
	const [confirming, setConfirming] = useState(false);
	const [localReleases, setLocalReleases] = useState<IncomingReleases>(() =>
		structuredClone(incomingFixtures.releases),
	);
	const [recordingQuery, setRecordingQuery] = useState("");
	const state = givenState ?? localState;
	const files = givenFiles ?? incomingFixtures.files;
	const releases = givenReleases ?? localReleases;
	const providers = givenProviders ?? providerReleases;
	const artists = givenArtists ?? incomingFixtures.artists;
	const activeGroupId = groupId ?? state.groupId;
	const group = state.groups.find((item) => item.id === activeGroupId);
	if (!group)
		return (
			<main className="demo-shell mx-auto max-w-[1100px] px-6 py-8">
				<h1>Входящая группа не найдена</h1>
				<a href="#review">К входящим</a>
			</main>
		);
	state.groupId = group.id;
	const target = findTarget(group, releases, providers);
	if (!target)
		return (
			<main className="demo-shell mx-auto max-w-[1100px] px-6 py-8">
				<h1>Цель сопоставления не найдена</h1>
				<a href="#review">К входящим</a>
			</main>
		);
	const tracks = target.tracks;
	const assignments = getDraft(group, files, providers, releases);
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
	const providerTrack = findProviderTrack(
		assignment?.providerTrackId ?? null,
		providers,
	);
	const existingRelease =
		group.mode === "local"
			? releases.find((release) => release.id === group.releaseId)
			: releases.find(
					(release) =>
						release.providerId === target.id ||
						release.providerId === target.providerId,
				);
	const localTrack = existingRelease?.tracks.find(
		(track) => track.position === inspected?.position,
	);
	const used = new Set(
		Object.values(assignments).flatMap((item) => item.sourceIds),
	);
	const pool = group.fileIds.filter(
		(id) => files[id]?.available && !used.has(id),
	);
	const allProviderTracks = providers.flatMap((release) =>
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
						`${track.title} ${artistName(String(track.artistId), artists)} ${release.title}`,
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
		const didAssign = assignFile(
			group,
			trackId,
			fileId,
			files,
			releases,
			providers,
		);
		state.selectedFile = null;
		state.selectedTarget = trackId;
		state.notice = didAssign
			? `К позиции добавлен вариант ${files[fileId].name}. Связь не меняет выбранный источник или публикацию.`
			: `${files[fileId]?.name ?? fileId} уже связан с этой позицией или недоступен; дополнительная связь не добавлена.`;
		clearPreview();
		commitState();
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
	const previewRows = preview
		? planRows(preview, files, releases, providers, artists)
		: [];
	const metadataIssues = (row: (typeof previewRows)[number]) => {
		if (!preview) return [];
		const local = releases.find(
			(release) => release.id === preview.localReleaseId,
		);
		return publicationMetadataIssues(
			preview.mode === "local" ? (local ?? preview.target) : preview.target,
			row.track ?? row.targetTrack,
			selectedSourceId(row.assignment),
		);
	};
	const effectiveMetadata = (row: (typeof previewRows)[number]) => (
		<MetadataDiff
			groups={metadataSnapshotDiff(
				row.previous?.tags ?? {},
				row.nextTags,
				{
					coverHash: row.previous?.coverHash ?? null,
					lyrics: row.previous?.lyrics ?? null,
				},
				{
					coverHash: (row.track ?? row.targetTrack).coverHash ?? null,
					lyrics: (row.track ?? row.targetTrack).lyrics ?? null,
				},
			)}
		/>
	);
	const activeRows = previewRows.filter(
		(row) => row.assignment?.included && row.file,
	);
	const changedRows = activeRows.filter(
		(row) => row.action !== "Без изменений" && !metadataIssues(row).length,
	);
	const linkedRows = previewRows.filter((row) => {
		if (row.assignment?.included && row.file && metadataIssues(row).length)
			return false;
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
		const changed = applyPlan(
			preview,
			files,
			nextReleases,
			providers,
			false,
			artists,
			destination,
		);
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
		? providerTags(providerTrack.release, providerTrack.track, artists)
		: {};
	const localTags =
		existingRelease && localTrack
			? providerTags(existingRelease, localTrack, artists)
			: group.mode === "local"
				? providerTags(target, inspected, artists)
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
			<div
				key={id}
				draggable
				onDragStart={(event) => {
					event.dataTransfer.setData("text/plain", id);
					event.dataTransfer.effectAllowed = "copy";
				}}
			>
				<Button
					type="button"
					className={`incoming-file mb-[5px] grid w-full cursor-grab gap-[3px] rounded border border-[var(--line)] bg-[var(--surface)] p-2 text-left hover:border-[var(--accent)] hover:bg-[var(--accent-soft)] ${state.selectedFile === id ? "selected border-[var(--accent)] bg-[var(--accent-soft)]" : ""}`}
					onPress={() => {
						state.selectedFile = state.selectedFile === id ? null : id;
						commitState();
					}}
					aria-pressed={state.selectedFile === id}
				>
					<strong className="[overflow-wrap:anywhere] text-[11px]">
						{file.name}
					</strong>
					<span className="[overflow-wrap:anywhere] text-[10px] text-[var(--muted)]">
						{file.codec} · {duration(file.duration)}
						{compact ? "" : ` · ${file.path}`}
					</span>
				</Button>
			</div>
		);
	};
	return (
		<main
			className="relative mx-auto w-full max-w-[1800px] px-[clamp(14px,2vw,30px)] pt-5 pb-[110px] text-[var(--ink)] max-[800px]:px-3 max-[800px]:pt-3.5 max-[800px]:pb-[120px]"
			onKeyDown={(event) => {
				if (event.key === "Escape" && state.selectedFile) {
					state.selectedFile = null;
					commitState();
				}
			}}
		>
			<header className="mb-4">
				<p className="demo-eyebrow mb-[5px] text-[11px] text-[var(--muted)]">
					<a href="#review">Все входящие</a> · Только чтение: /mnt/inbox/
					{group.id}
				</p>
				<h1 className="m-0 text-[clamp(22px,2vw,30px)] tracking-[-.025em]">
					Разбор: {group.title}
				</h1>
				<p className="mt-1 text-[var(--muted)]">
					{group.mode === "local"
						? "Локальные позиции; композиции можно брать из разных альбомов."
						: "Выбранное издание → его позиции. Наши файлы находятся отдельно справа."}
				</p>
			</header>
			{state.notice && (
				<p
					role="status"
					className="demo-notice mb-2.5 rounded border border-[var(--line)] bg-[var(--accent-soft)] px-3 py-[9px]"
				>
					{state.notice}
				</p>
			)}
			<div className="incoming-summary mb-3 flex flex-wrap items-center gap-x-5 gap-y-2 rounded-[5px] border border-[var(--line)] bg-[var(--raised)] px-3 py-[9px] text-[var(--muted)]">
				<strong className="text-[var(--ink)]">
					{Object.keys(assignments).length}/{tracks.length} назначено
				</strong>
				<span>{pool.length} файлов свободно</span>
				<span>
					{group.mode === "local"
						? "Локальный порядок"
						: `${target.year ?? ""} · ${target.country ?? ""} · ${target.catalog ?? ""}`}
				</span>
			</div>
			<div className="matching-layout grid grid-cols-[minmax(220px,.72fr)_minmax(450px,2.1fr)_minmax(220px,.78fr)] items-start gap-3 max-[1250px]:grid-cols-[minmax(200px,.72fr)_minmax(0,1.8fr)] max-[800px]:grid-cols-1">
				<section
					className={`matching-candidates ${surfacePanel} sticky top-[10px] max-h-[calc(100dvh_-_24px)] overflow-auto max-[1250px]:row-start-1 max-[1250px]:col-start-1 max-[800px]:static max-[800px]:max-h-none`}
				>
					<h2 className="mb-2.5 mt-0 text-[15px] tracking-[-.01em]">
						Найти издание
					</h2>
					<MatchingSearchPicker
						artists={artists}
						releases={providers}
						selectedReleaseId={
							group.mode === "provider" ? group.candidateId : null
						}
						onSelectRelease={setCandidate}
					/>
					<Button
						className="demo-button max-[520px]:w-full"
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
				<section
					className={`matching-target ${surfacePanel} max-[1250px]:row-span-2 max-[1250px]:col-start-2 max-[800px]:row-auto max-[800px]:col-auto`}
				>
					<h2 className="mb-2.5 mt-0 text-[15px] tracking-[-.01em]">
						{String(target.title)} · {tracks.length} позиций
					</h2>
					<div className={comparisonTableWrap}>
						<table className={table}>
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
											? findProviderTrack(
													item?.providerTrackId ?? null,
													providers,
												)
											: null;
									return (
										<tr
											key={id}
											className={
												id === selectedTrackId
													? "inspected bg-[var(--accent-soft)]"
													: ""
											}
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
													<span className="checkbox" aria-hidden="true">
														{item?.included && usableFile(file) ? "✓" : ""}
													</span>
												</Checkbox>
											</td>
											<td>{String(track.position).padStart(2, "0")}</td>
											<td>
												<Button
													className="min-h-0 border-0 bg-transparent p-0 text-left font-semibold text-[var(--accent)] hover:bg-transparent"
													onPress={() => {
														state.selectedTarget = id;
														state.inspector = "tags";
														commitState();
													}}
												>
													{String(track.title)}
												</Button>
												<span className="block text-[10px] text-[var(--muted)]">
													{artistName(String(track.artistId), artists)}
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
															<div
																key={sourceId}
																className="linked-source border-b border-[var(--line)] py-1.5"
															>
																<RadioGroup
																	aria-label={`Источник для ${String(track.title)}`}
																	value={currentId ?? ""}
																	onChange={(value) =>
																		sourceSelection(id, value)
																	}
																>
																	<Radio value={sourceId}>
																		Выбрать для публикации
																	</Radio>
																</RadioGroup>
																{fileButton(sourceId, true)}
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
				<section
					className={`matching-pool ${surfacePanel} sticky top-[10px] max-h-[calc(100dvh_-_24px)] overflow-auto max-[1250px]:row-start-2 max-[1250px]:col-start-1 max-[800px]:static max-[800px]:row-auto max-[800px]:col-auto max-[800px]:max-h-none`}
				>
					<h2 className="mb-2.5 mt-0 text-[15px] tracking-[-.01em]">
						Наши файлы · {pool.length} свободно
					</h2>
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
				<section className={`matching-recording ${surfacePanel} mt-3`}>
					<h2 className="mb-2.5 mt-0 text-[15px] tracking-[-.01em]">
						Поиск композиции
					</h2>
					<form
						className="flex flex-wrap items-end gap-[7px] [&_.ui-field]:min-w-[190px] [&_.ui-field]:flex-1"
						onSubmit={(event) => {
							event.preventDefault();
							state.recordingQuery = recordingQuery;
							state.recordingResults = allProviderTracks;
							commitState();
						}}
					>
						<label htmlFor="recording-query">Название / артист</label>
						<TextField
							className="ui-field"
							aria-label="Название / артист"
							value={recordingQuery}
							onChange={setRecordingQuery}
						>
							<Input id="recording-query" />
						</TextField>
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
			<section className={`matching-inspector ${surfacePanel} mt-3`}>
				<h2 className="mb-2.5 mt-0 text-[15px] tracking-[-.01em]">
					Инспектор · {String(inspected.title)}
				</h2>
				<Tabs
					className="matching-inspector-tabs min-w-0 [&_.ui-tab-panel]:pt-3"
					selectedKey={state.inspector}
					onSelectionChange={(key) => {
						state.inspector = key as "tags" | "evidence";
						commitState();
					}}
				>
					<TabList aria-label="Инспектор позиции">
						<Tab id="tags">Сравнение тегов</Tab>
						<Tab id="evidence">Почему такое совпадение</Tab>
					</TabList>
					{state.inspector === "tags" ? (
						<TabPanel id="tags">
							<>
								<div className="tag-controls mb-2.5 mt-0 flex flex-wrap items-center gap-2.5">
									<Tabs
										selectedKey={state.tagScope}
										onSelectionChange={(key) => {
											state.tagScope = key as "artist" | "release" | "track";
											commitState();
										}}
									>
										<TabList aria-label="Область тегов">
											<Tab id="artist">Артист</Tab>
											<Tab id="release">Альбом</Tab>
											<Tab id="track">Трек</Tab>
										</TabList>
									</Tabs>
									<Checkbox
										isSelected={state.differencesOnly}
										onChange={(checked) => {
											state.differencesOnly = checked;
											commitState();
										}}
									>
										<span className="checkbox" />
										Только отличия
									</Checkbox>
								</div>
								<div className={comparisonTableWrap}>
									<table className={table}>
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
						</TabPanel>
					) : (
						<TabPanel id="evidence">
							<Evidence
								file={selectedFile}
								track={inspected}
								assignment={assignment}
								providerArtists={artists}
							/>
						</TabPanel>
					)}
				</Tabs>
			</section>
			<div className="matching-commit fixed bottom-3.5 left-[calc(174px_+_18px)] right-[18px] z-[5] m-0 flex flex-wrap items-center justify-between gap-x-5 gap-y-2 rounded-md border border-[var(--line)] bg-[color-mix(in_srgb,var(--surface)_95%,transparent)] px-3.5 py-2.5 shadow-[0_6px_24px_#0002] backdrop-blur-[12px] max-[800px]:right-2 max-[800px]:bottom-2 max-[800px]:left-2 max-[800px]:p-2 max-[520px]:items-stretch">
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
				<span className="min-w-[220px] flex-1 text-[11px] text-[var(--muted)] max-[800px]:hidden">
					{tracks.length - Object.keys(assignments).length} пустых позиций не
					блокируют публикацию. {pool.length} файлов останутся неразмеченными во
					входящих.
				</span>
				<Button
					className="demo-button max-[520px]:w-full"
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
					className={`matching-preview ${surfacePanel} mt-3.5 max-w-[min(1100px,95vw)]`}
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
					<div className={comparisonTableWrap}>
						<table className={previewTable}>
							<thead>
								<tr>
									<th>Включить</th>
									<th>№ / трек</th>
									<th>Источник</th>
									<th>Эффективные метаданные · сейчас → после</th>
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
												<span className="checkbox">
													{row.assignment?.included ? "✓" : ""}
												</span>
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
										<td>{effectiveMetadata(row)}</td>
										<td>
											{row.assignment?.included &&
											row.file &&
											metadataIssues(row).length
												? "Невозможно обработать"
												: row.action}
											{row.downgrade ? " · Понижение качества" : ""}
											{row.assignment?.included &&
												row.file &&
												metadataIssues(row).length > 0 && (
													<p>
														Некорректные метаданные:{" "}
														{metadataIssues(row).join("; ")}
													</p>
												)}
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
							<span className="checkbox" />
							Подтверждаю замену сжатия без потерь на сжатие с потерями
						</Checkbox>
					)}
					<div className="incoming-actions flex flex-wrap justify-end gap-2">
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
				<Dialog
					aria-labelledby="incoming-confirm-title"
					className="grid gap-2.5"
				>
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
					<div className={dialogFooter}>
						<Button onPress={() => setConfirming(false)}>Отмена</Button>
						<Button
							className="border-[var(--button)]! bg-[var(--button)]! font-semibold text-[var(--button-text)]! hover:brightness-90"
							isDisabled={stale || (hasDowngrade && !state.confirmDowngrade)}
							onPress={confirmPublish}
						>
							Подтвердить
						</Button>
					</div>
				</Dialog>
			</Modal>
		</main>
	);
}

function Evidence({
	file,
	track,
	assignment,
	providerArtists,
}: {
	file?: SourceFile;
	track: OriginalTrack;
	assignment?: Assignment;
	providerArtists: CatalogArtist[];
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
			<div className={comparisonTableWrap}>
				<table className={table}>
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
							<td>{artistName(String(track.artistId), providerArtists)}</td>
							<td>
								{compare(
									rawArtist,
									artistName(String(track.artistId), providerArtists),
								)}
							</td>
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
