import type { ReactNode } from "react";
import type {
	CatalogArtist,
	OriginalRelease,
	OriginalTrack,
	SourceFiles,
} from "./catalogData";
import {
	originalArtistName,
	originalProviders,
	trackNumberLabel,
} from "./catalogData";
import {
	availabilityRowClass,
	availabilityRowHint,
	type AvailabilityFilter,
} from "./availabilityRows";
import { Button, Cell, Column, Row, Table, TableBody, TableHeader } from "./ui";

type View = "artists" | "albums" | "tracks";
type CatalogTrack = Omit<OriginalTrack, "published"> & {
	published: boolean | OriginalTrack["published"];
};
type Release = Omit<OriginalRelease, "tracks"> & { tracks: CatalogTrack[] };
interface Props {
	view: View;
	artists: CatalogArtist[];
	releases: Release[];
	missingCounts: Map<string, number>;
	hasAudio: (track: Release["tracks"][number]) => boolean;
	files: SourceFiles;
	availability?: AvailabilityFilter;
	publication?: "all" | "published" | "unpublished";
	groupCheckbox: (ids: string[], label: string) => ReactNode;
	expanded: string | null;
	onToggleAlbum: (id: string) => void;
}
const sourceIds = (track: Pick<OriginalTrack, "sourceId" | "sourceIds">) => [
	...new Set([
		...(track.sourceIds ?? []),
		...(track.sourceId ? [track.sourceId] : []),
	]),
];
const duration = (seconds: number | null) =>
	seconds === null
		? "—"
		: `${Math.floor(seconds / 60)}:${String(Math.round(seconds % 60)).padStart(2, "0")}`;

export function LibraryCatalogRows({
	view,
	artists,
	releases,
	missingCounts,
	hasAudio,
	files,
	availability = "all",
	publication = "all",
	groupCheckbox,
	expanded,
	onToggleAlbum,
}: Props) {
	const tracks = releases.flatMap((release) =>
		release.tracks.map((track) => ({ release, track })),
	);
	const columns =
		view === "artists"
			? [
					"Выбор",
					"Артист",
					"Издания",
					"Позиции",
					"Есть аудио",
					"Опубликовано",
					"Без издания",
				]
			: view === "albums"
				? [
						"Выбор",
						"Альбом",
						"Артист",
						"Год / издание",
						"Позиций",
						"Исходники",
						"Аудио",
						"Публикация",
						"Провайдер",
					]
				: [
						"Выбор",
						"Издание",
						"Носитель / №",
						"Композиция",
						"Длительность",
						"Источник",
						"Параметры",
						"Файл / путь",
						"Публикация",
						"Теги",
					];
	return (
		<div className="catalogue-table-scroll">
			<Table
				aria-label={
					view === "artists"
						? "Артисты медиатеки"
						: view === "albums"
							? "Альбомы медиатеки"
							: "Треки медиатеки"
				}
				className={`catalogue-table catalogue-table--${view}`}
			>
				<TableHeader>
					{columns.map((column, index) => (
						<Column key={column} isRowHeader={index === 1}>
							{column}
						</Column>
					))}
				</TableHeader>
				{view === "artists" ? (
					<TableBody
						items={artists}
						dependencies={[
							artists,
							releases,
							missingCounts,
							groupCheckbox,
							hasAudio,
							files,
						]}
						renderEmptyState={() => (
							<div className="catalogue-empty">Артистов не найдено.</div>
						)}
					>
						{(artist) => {
							const own = releases.filter(
								(release) =>
									release.artistId === artist.id ||
									release.credits?.some(
										(credit) => credit.artistId === artist.id,
									),
							);
							const ownTracks = own.flatMap((release) => release.tracks);
							return (
								<Row id={artist.id}>
									<Cell>
										{groupCheckbox(
											ownTracks.map((track) => track.id),
											`Выбрать позиции артиста ${artist.name}`,
										)}
									</Cell>
									<Cell>
										<a href={`#artist/${artist.id}`}>{artist.name}</a>
										<span className="catalogue-sub">
											{artist.genre || "Исполнитель"}
										</span>
									</Cell>
									<Cell>{own.length}</Cell>
									<Cell>{ownTracks.length}</Cell>
									<Cell>
										{ownTracks.filter(hasAudio).length} / {ownTracks.length}
									</Cell>
									<Cell>
										{
											ownTracks.filter((track) => Boolean(track.published))
												.length
										}{" "}
										/ {ownTracks.length}
									</Cell>
									<Cell>{missingCounts.get(artist.id) || "—"}</Cell>
								</Row>
							);
						}}
					</TableBody>
				) : view === "albums" ? (
					<TableBody
						items={releases}
						dependencies={[
							releases,
							artists,
							groupCheckbox,
							hasAudio,
							files,
							expanded,
							availability,
							publication,
						]}
						renderEmptyState={() => (
							<div className="catalogue-empty">
								Нет результатов. Измените запрос или фильтр.
							</div>
						)}
					>
						{(release) => {
							const provider = originalProviders.find(
								(item) => item.id === release.providerId,
							);
							const published = release.tracks.filter((track) =>
								Boolean(track.published),
							).length;
							const sourceCount = release.tracks.filter(
								(track) => sourceIds(track).length > 0,
							).length;
							return (
								<Row id={release.id}>
									<Cell>
										{groupCheckbox(
											release.tracks.map((track) => track.id),
											`Выбрать позиции альбома ${release.title}`,
										)}
									</Cell>
									<Cell>
										<Button
											className="catalogue-expander"
											aria-expanded={expanded === release.id}
											onPress={() => onToggleAlbum(release.id)}
										>
											{release.title || "Без названия"}
										</Button>
										<span className="catalogue-sub">
											<a href={`#release/${release.id}`}>Открыть издание</a>
										</span>
										{expanded === release.id && (
											<div className="catalogue-inline-tracks">
												<table>
													<thead>
														<tr>
															{[
																"Выбор",
																"№",
																"Композиция",
																"Длительность",
																"Источник",
																"Параметры",
																"Файл / путь",
																"Публикация",
																"Теги",
															].map((heading) => (
																<th key={heading} scope="col">
																	{heading}
																</th>
															))}
														</tr>
													</thead>
													<tbody>
														{release.tracks.map((track) => {
															const ids = sourceIds(track);
															const source = track.sourceId
																? files[track.sourceId]
																: undefined;
															const pub =
																track.published &&
																typeof track.published === "object"
																	? track.published
																	: null;
															return (
																<tr
																	key={track.id}
																	className={availabilityRowClass(
																		track as OriginalTrack,
																		availability,
																		hasAudio,
																	)}
																>
																	<td>
																		{groupCheckbox(
																			[track.id],
																			`Выбрать ${track.title}`,
																		)}
																	</td>
																	<td>{trackNumberLabel(release, track)}</td>
																	<td>
																		<a href={`#track/${track.id}`}>
																			{track.title}
																		</a>
																		<span className="catalogue-sub">
																			{originalArtistName(
																				track.artistId,
																				artists,
																			)}
																			{track.recordingTitle
																				? ` · ${track.recordingTitle}`
																				: ""}
																		</span>
																		{track.recordingId ? null : (
																			<span className="catalogue-sub">
																				Новая запись
																			</span>
																		)}
																	</td>
																	<td>{duration(track.duration)}</td>
																	<td>
																		{source?.codec ??
																			(ids.length
																				? "Источник не выбран"
																				: "Нет файла")}
																		<span className="catalogue-sub">
																			{ids.length} исходников ·{" "}
																			{hasAudio(track)
																				? "есть аудио"
																				: "нет аудио"}
																		</span>
																	</td>
																	<td>
																		{source
																			? `${source.codec === "FLAC" ? "24 бит / 48 кГц" : "320 кбит/с"} · ${source.codec}`
																			: "—"}
																	</td>
																	<td>
																		{ids
																			.map(
																				(id) =>
																					files[id]?.path || files[id]?.name,
																			)
																			.filter(Boolean)
																			.map((path) => (
																				<span
																					className="catalogue-path"
																					key={path}
																				>
																					{path}
																				</span>
																			))}
																	</td>
																	<td>
																		{pub
																			? `${pub.container} · ${pub.codec}`
																			: "Не опубликовано"}
																		<span className="catalogue-sub catalogue-path">
																			{pub?.path || ""}
																		</span>
																	</td>
																	<td>
																		<a href={`#tags/${track.id}/track`}>Теги</a>
																		{pub && (
																			<span className="catalogue-sub">
																				{pub.tags.TITLE?.join(", ") ||
																					track.title}
																			</span>
																		)}
																	</td>
																</tr>
															);
														})}
													</tbody>
												</table>
											</div>
										)}
									</Cell>
									<Cell>
										{release.credits?.length ? (
											release.credits.map((credit, index) => (
												<span key={`${credit.artistId ?? "credit"}-${index}`}>
													{index > 0 && ", "}
													{credit.artistId ? (
														<a href={`#artist/${credit.artistId}`}>
															{credit.credit || credit.name}
														</a>
													) : (
														credit.credit || credit.name
													)}
												</span>
											))
										) : release.artistId ? (
											<a href={`#artist/${release.artistId}`}>
												{originalArtistName(release.artistId, artists)}
											</a>
										) : (
											"Артист не указан"
										)}
									</Cell>
									<Cell>
										{release.year || "Дата не указана"}
										<span className="catalogue-sub">
											{release.country || "Локальный"} ·{" "}
											{release.catalog || "—"}
										</span>
									</Cell>
									<Cell>{release.tracks.length}</Cell>
									<Cell>{sourceCount}</Cell>
									<Cell>
										{release.tracks.filter(hasAudio).length}
										{provider?.tracks.length
											? ` / ${provider.tracks.length}`
											: " с аудио"}
									</Cell>
									<Cell>
										{published} опубликовано
										<span className="catalogue-sub">
											{provider?.tracks.length === release.tracks.length
												? "Полнота известна"
												: "Полнота неизвестна"}
										</span>
									</Cell>
									<Cell>{provider ? "MusicBrainz" : "Локальный сборник"}</Cell>
								</Row>
							);
						}}
					</TableBody>
				) : (
					<TableBody
						items={tracks}
						dependencies={[
							tracks,
							artists,
							groupCheckbox,
							hasAudio,
							files,
							availability,
							publication,
						]}
						renderEmptyState={() => (
							<div className="catalogue-empty">
								Нет результатов. Измените запрос или фильтр.
							</div>
						)}
					>
						{({ release, track }) => {
							const ids = sourceIds(track);
							const selectedFile = track.sourceId
								? files[track.sourceId]
								: undefined;
							const pub =
								track.published && typeof track.published === "object"
									? track.published
									: null;
							return (
								<Row
									id={track.id}
									className={availabilityRowClass(
										track as OriginalTrack,
										availability,
										hasAudio,
									)}
								>
									<Cell>
										{groupCheckbox([track.id], `Выбрать ${track.title}`)}
									</Cell>
									<Cell>
										<a href={`#release/${release.id}`}>{release.title}</a>
										<span className="catalogue-sub">
											{originalArtistName(release.artistId, artists)} ·{" "}
											{release.year}
										</span>
									</Cell>
									<Cell>
										{release.media?.[(track.medium || 1) - 1]?.title ||
											track.medium ||
											"—"}
										<span className="catalogue-sub">
											{trackNumberLabel(release, track)}
										</span>
									</Cell>
									<Cell>
										<a href={`#track/${track.id}`}>{track.title}</a>
										<span className="catalogue-sub">
											{originalArtistName(track.artistId, artists)}
											{track.recordingTitle ? ` · ${track.recordingTitle}` : ""}
										</span>
										{availabilityRowHint(
											track as OriginalTrack,
											availability,
											hasAudio,
										) && (
											<span className="catalogue-sub">
												{availabilityRowHint(
													track as OriginalTrack,
													availability,
													hasAudio,
												)}
											</span>
										)}
									</Cell>
									<Cell>{duration(track.duration)}</Cell>
									<Cell>
										{selectedFile?.codec ??
											(ids.length ? "Источник не выбран" : "Нет файла")}
										<span className="catalogue-sub">
											{ids.length} исходников ·{" "}
											{hasAudio(track) ? "есть аудио" : "нет аудио"}
										</span>
									</Cell>
									<Cell>
										{selectedFile
											? `${selectedFile.codec === "FLAC" ? "24 бит / 48 кГц" : "320 кбит/с"} · ${selectedFile.codec}`
											: "—"}
									</Cell>
									<Cell>
										{ids
											.map((id) => files[id]?.path || files[id]?.name)
											.filter(Boolean)
											.map((path) => (
												<span className="catalogue-path" key={path}>
													{path}
												</span>
											))}
									</Cell>
									<Cell>
										{pub
											? `${pub.container} · ${pub.codec}`
											: "Не опубликовано"}
										<span className="catalogue-sub catalogue-path">
											{pub?.path || ""}
										</span>
									</Cell>
									<Cell>
										<a href={`#tags/${track.id}/track`}>Теги</a>
										{pub && (
											<span className="catalogue-sub">
												{pub.tags.TITLE?.join(", ") || track.title}
											</span>
										)}
									</Cell>
								</Row>
							);
						}}
					</TableBody>
				)}
			</Table>
		</div>
	);
}
