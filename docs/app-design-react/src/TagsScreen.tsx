import { useState } from "react";
import "./matching-publication.css";
import {
	Button,
	Checkbox,
	Label,
	ListBox,
	ListBoxItem,
	Popover,
	Select,
	SelectValue,
	Tab,
	TabList,
	TabPanel,
	Tabs,
} from "./ui";
import {
	desiredTags,
	originalProviders,
	type CatalogArtist,
	type OriginalRelease,
	type OriginalTrack,
	type SourceFiles,
} from "./catalogData";

const fields = {
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
} as const;
type Scope = keyof typeof fields;

export function TagsScreen({
	trackId,
	scope: initialScope = "track",
	releases,
	artists,
	files,
	onSelectFile,
	onPreview,
}: {
	trackId: string;
	scope?: Scope;
	releases: OriginalRelease[];
	artists: CatalogArtist[];
	files: SourceFiles;
	onSelectFile?: (trackId: string, fileId: string) => void;
	onPreview?: (ids: string[]) => void;
}) {
	const initial = releases
		.flatMap((release) => release.tracks.map((track) => ({ release, track })))
		.find(({ track }) => track.id === trackId);
	const [currentTrackId, setCurrentTrackId] = useState(trackId);
	const [scope, setScope] = useState<Scope>(initialScope);
	const [differencesOnly, setDifferencesOnly] = useState(false);
	const [fileOverride, setFileOverride] = useState<string | null | undefined>(
		undefined,
	);
	const selected =
		releases
			.flatMap((release) => release.tracks.map((track) => ({ release, track })))
			.find(({ track }) => track.id === currentTrackId) ?? initial;
	if (!selected)
		return (
			<section>
				<h1>Теги не найдены</h1>
				<p>Позиция отсутствует в медиатеке.</p>
				<a href="#library">К медиатеке</a>
			</section>
		);
	const { release, track } = selected;
	const linkedFileIds = [
		...new Set([
			...(track.sourceIds ?? []),
			...(track.sourceId ? [track.sourceId] : []),
		]),
	];
	const fileId =
		fileOverride === undefined
			? track.sourceId
			: fileOverride && linkedFileIds.includes(fileOverride)
				? fileOverride
				: null;
	const file = fileId ? files[fileId] : undefined;
	const provider = originalProviders
		.flatMap((item) =>
			item.tracks.map((candidate) => ({ release: item, track: candidate })),
		)
		.find(({ track: candidate }) => candidate.id === track.providerTrackId);
	const sourceTags = file?.tags ?? {};
	const remoteTags = provider
		? desiredTags(provider.release, provider.track, artists)
		: null;
	const localTags = desiredTags(release, track, artists);
	const publicationTags = track.published?.tags ?? {};
	const rows = fields[scope]
		.map((key) => {
			const values = [
				sourceTags[key] ?? [],
				remoteTags?.[key] ?? null,
				localTags[key] ?? [],
				track.published ? (publicationTags[key] ?? []) : null,
			];
			const distinct = new Set(
				values.map((value) => JSON.stringify(value ?? [])),
			);
			return { key, values, different: distinct.size > 1 };
		})
		.filter((row) => !differencesOnly || row.different);
	const scopes: [Scope, string][] = [
		["artist", "Артист"],
		["release", "Альбом"],
		["track", "Трек"],
	];
	return (
		<section>
			<header className="demo-object-header">
				<div>
					<h1>Теги: {track.title}</h1>
					<p>
						Артист и альбом в опубликованной колонке — теги выбранного
						аудиофайла. Несовпадения не скрываются.
					</p>
				</div>
				<a href={`#release/${release.id}`}>К альбому</a>
			</header>
			<section className="demo-panel">
				<h2>Контекст сравнения</h2>
				<div className="demo-object-actions comparison-controls">
					<Select
						aria-label="Файл / позиция"
						selectedKey={track.id}
						onSelectionChange={(key) => {
							setCurrentTrackId(String(key));
							setFileOverride(undefined);
						}}
					>
						<Label>Файл / позиция</Label>
						<Button>
							<SelectValue />
							<span aria-hidden="true">⌄</span>
						</Button>
						<Popover>
							<ListBox>
								{release.tracks.map((item) => (
									<ListBoxItem key={item.id} id={item.id}>
										{String(item.position).padStart(2, "0")} · {item.title} ·{" "}
										{item.published ? "опубликован" : "без публикации"}
									</ListBoxItem>
								))}
							</ListBox>
						</Popover>
					</Select>
					<Select
						aria-label="Исходный файл для сравнения"
						selectedKey={fileId ?? ""}
						onSelectionChange={(key) => {
							const value = String(key);
							setFileOverride(value || null);
							if (value) onSelectFile?.(track.id, value);
						}}
					>
						<Label>Исходный файл для сравнения</Label>
						<Button>
							<SelectValue />
							<span aria-hidden="true">⌄</span>
						</Button>
						<Popover>
							<ListBox>
								<ListBoxItem id="">Исходник не выбран</ListBoxItem>
								{linkedFileIds.map(
									(id) =>
										files[id] && (
											<ListBoxItem key={id} id={id}>
												{files[id].name}
											</ListBoxItem>
										),
								)}
							</ListBox>
						</Popover>
					</Select>
					<span className="demo-secondary">
						Кэш провайдера: 26.09.2026 10:20 · идентификаторы демонстрационные
					</span>
				</div>
				<div className="demo-object-actions">
					<Tabs
						selectedKey={scope}
						onSelectionChange={(key) => setScope(key as Scope)}
					>
						<TabList aria-label="Область тегов">
							{scopes.map(([id, label]) => (
								<Tab key={id} id={id}>
									{label}
								</Tab>
							))}
						</TabList>
						<TabPanel id={scope}>
							<div className="demo-preview-table">
								<table>
									<thead>
										<tr>
											{[
												"Поле",
												"Исходный файл",
												"Провайдер · кэш",
												"Локальная медиатека",
												"Сейчас в публикации",
											].map((heading) => (
												<th scope="col" key={heading}>
													{heading}
												</th>
											))}
										</tr>
									</thead>
									<tbody>
										{rows.map(({ key, values, different }) => (
											<tr key={key}>
												<th
													scope="row"
													className={different ? "difference" : ""}
												>
													{key}
												</th>
												{values.map((value, index) => (
													<td key={`${key}:${index}`}>
														{value === null ? (
															<span className="demo-secondary">
																{index === 1 ? "Нет связи" : "Не опубликован"}
															</span>
														) : value.length ? (
															value.map((item, itemIndex) => (
																<span key={`${itemIndex}:${item}`}>
																	{itemIndex > 0 && <br />}
																	{item}
																</span>
															))
														) : (
															"—"
														)}
													</td>
												))}
											</tr>
										))}
									</tbody>
								</table>
							</div>
							<div className="demo-secondary">
								{file?.path ?? "Исходник не выбран"}
								<br />
								{track.published
									? `Публикация: ${track.published.path} · чтение ${track.published.readAt} (демо)`
									: "Публикация отсутствует; пустая колонка не подменяется текущими метаданными."}
							</div>
						</TabPanel>
					</Tabs>
					<Checkbox isSelected={differencesOnly} onChange={setDifferencesOnly}>
						<span className="checkbox" />
						Только отличия
					</Checkbox>
					{onPreview && (
						<Button onPress={() => onPreview([track.id])}>
							Предпросмотр публикации
						</Button>
					)}
				</div>
			</section>
			<p className="demo-migration-note">
				В колонке публикации показан отдельный снимок чтения существующего
				файла. Изменение локальных тегов не изменяет эту колонку до явной
				публикации.
			</p>
		</section>
	);
}

export type TagsScope = Scope;
export type TagsTrack = OriginalTrack;
