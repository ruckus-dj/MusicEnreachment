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

const trackSourceIds = (
	track: Pick<OriginalTrack, "sourceId" | "sourceIds">,
) => [
	...new Set([
		...(track.sourceIds ?? []),
		...(track.sourceId ? [track.sourceId] : []),
	]),
];

function duration(seconds: number | null) {
	return seconds === null
		? "—"
		: `${Math.floor(seconds / 60)}:${String(Math.round(seconds % 60)).padStart(2, "0")}`;
}

function Empty({ children }: { children: string }) {
	return <div className="empty">{children}</div>;
}

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
	if (view === "artists") {
		return (
			<div className="demo-catalog-table-wrap">
				<table className="demo-catalog-table">
					<thead>
						<tr>
							<th scope="col">Выбор</th>
							<th scope="col">Артист</th>
							<th scope="col">Издания</th>
							<th scope="col">Позиции</th>
							<th scope="col">Есть аудио</th>
							<th scope="col">Опубликовано</th>
							<th scope="col">Альбомы без издания</th>
						</tr>
					</thead>
					<tbody>
						{artists.length ? (
							artists.map((artist) => {
								const artistReleases = releases.filter(
									(release) =>
										release.artistId === artist.id ||
										release.credits?.some(
											(credit) => credit.artistId === artist.id,
										),
								);
								const artistTracks = artistReleases.flatMap(
									(release) => release.tracks,
								);
								const missing = missingCounts.get(artist.id) ?? 0;
								return (
									<tr key={artist.id}>
										<td>
											{groupCheckbox(
												artistTracks.map((track) => track.id),
												`Выбрать позиции артиста ${artist.name}`,
											)}
										</td>
										<td>
											<a href={`#artist/${artist.id}`}>{artist.name}</a>
											{artist.genre && (
												<span className="sub">{artist.genre}</span>
											)}
										</td>
										<td>{artistReleases.length}</td>
										<td>{artistTracks.length}</td>
										<td>
											{artistTracks.filter(hasAudio).length} /{" "}
											{artistTracks.length}
										</td>
										<td>
											{
												artistTracks.filter((track) => Boolean(track.published))
													.length
											}{" "}
											/ {artistTracks.length}
										</td>
										<td>{missing || "—"}</td>
									</tr>
								);
							})
						) : (
							<tr>
								<td colSpan={7}>
									<Empty>Артистов не найдено.</Empty>
								</td>
							</tr>
						)}
					</tbody>
				</table>
			</div>
		);
	}
	if (view === "albums") {
		return (
			<div className="demo-catalog-table-wrap">
				<table className="demo-catalog-table">
					<thead>
						<tr>
							<th scope="col">Выбор</th>
							<th scope="col">Альбом</th>
							<th scope="col">Артист</th>
							<th scope="col">Год / издание</th>
							<th scope="col">Позиции</th>
							<th scope="col">Есть исходники</th>
							<th scope="col">Есть аудио</th>
							<th scope="col">Опубликовано</th>
							<th scope="col">Провайдер</th>
						</tr>
					</thead>
					<tbody>
						{releases.length ? (
							releases.map((release) => {
								const provider = release.providerId
									? originalProvider(release.providerId)
									: undefined;
								const published = release.tracks.filter((track) =>
									Boolean(track.published),
								).length;
								const known = Boolean(
									provider?.tracks.length &&
										provider.tracks.length === release.tracks.length,
								);
								const audio = release.tracks.filter(hasAudio).length;
								const sourceCount = release.tracks.filter(
									(track) => trackSourceIds(track).length > 0,
								).length;
								return [
									<tr key={release.id}>
										<td>
											{groupCheckbox(
												release.tracks.map((track) => track.id),
												`Выбрать позиции альбома ${release.title}`,
											)}
										</td>
										<td>
											<button
												className="demo-catalog-expander"
												type="button"
												aria-expanded={expanded === release.id}
												onClick={() => onToggleAlbum(release.id)}
											>
												{release.title || "Без названия"}
											</button>
											<span className="sub">
												<a href={`#release/${release.id}`}>Открыть альбом</a>
											</span>
										</td>
										<td>
											{release.credits?.length ? (
												release.credits.map((credit, index) => (
													<span key={`${credit.artistId ?? "credit"}-${index}`}>
														{index > 0 && ", "}
														{credit.artistId ? (
															<a href={`#artist/${credit.artistId}`}>
																{credit.credit || credit.name}
															</a>
														) : (
															credit.credit || credit.name || "Не указан"
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
										</td>
										<td>
											{release.year || "Дата не указана"} ·{" "}
											{release.country || "Локальный"}
											<span className="sub">{release.catalog || "—"}</span>
										</td>
										<td>{release.tracks.length}</td>
										<td>{sourceCount}</td>
										<td>
											{audio} с аудио
											{provider?.tracks.length
												? ` / ${provider.tracks.length}`
												: ""}
										</td>
										<td>
											{known
												? `${published}/${provider?.tracks.length} · ${published === provider?.tracks.length ? "Полностью опубликовано" : published ? "Частично опубликовано" : "Не опубликовано"}`
												: `${published} опубликовано · полнота неизвестна`}
										</td>
										<td>{provider ? "MusicBrainz" : "Локальный сборник"}</td>
									</tr>,
									expanded === release.id && (
										<tr key={`${release.id}-tracks`}>
											<td colSpan={9}>
												<LibraryCatalogRows
													view="tracks"
													artists={artists}
													releases={[release]}
													missingCounts={missingCounts}
													hasAudio={hasAudio}
													files={files}
													availability={availability}
													publication={publication}
													groupCheckbox={groupCheckbox}
													expanded={expanded}
													onToggleAlbum={onToggleAlbum}
												/>
											</td>
										</tr>
									),
								];
							})
						) : (
							<tr>
								<td colSpan={9}>
									<Empty>Нет результатов. Измените запрос или фильтр.</Empty>
								</td>
							</tr>
						)}
					</tbody>
				</table>
			</div>
		);
	}
	return (
		<div className="demo-catalog-table-wrap">
			<table className="demo-catalog-table demo-track-table">
				<thead>
					<tr>
						<th scope="col">Выбор</th>
						<th scope="col">Издание</th>
						<th scope="col">Носитель / №</th>
						<th scope="col">Композиция</th>
						<th scope="col">Длительность</th>
						<th scope="col">Источник</th>
						<th scope="col">Параметры</th>
						<th scope="col">Файл / путь</th>
						<th scope="col">Публикация</th>
						<th scope="col">Теги</th>
					</tr>
				</thead>
				<tbody>
					{tracks.length ? (
						tracks.map(({ release, track }) => {
							const ids = trackSourceIds(track);
							const selectedFile = track.sourceId
								? files[track.sourceId]
								: undefined;
							const fileNames = ids
								.map((id) => files[id]?.name)
								.filter(Boolean)
								.join(", ");
							const pub =
								track.published && typeof track.published === "object"
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
									<td>{groupCheckbox([track.id], `Выбрать ${track.title}`)}</td>
									<td>
										<a href={`#release/${release.id}`}>{release.title}</a>
										<span className="sub">
											{originalArtistName(release.artistId, artists)} ·{" "}
											{release.year}
										</span>
									</td>
									<td>
										{release.media?.[(track.medium || 1) - 1]?.title ||
											track.medium ||
											"—"}
										<span className="sub">
											{trackNumberLabel(release, track)}
										</span>
									</td>
									<td>
										<a href={`#track/${track.id}`}>{track.title}</a>
										{availabilityRowHint(
											track as OriginalTrack,
											availability,
											hasAudio,
										) && (
											<span className="sub">
												{availabilityRowHint(
													track as OriginalTrack,
													availability,
													hasAudio,
												)}
											</span>
										)}
										<span className="sub">
											{originalArtistName(track.artistId, artists)}
											{track.recordingTitle ? ` · ${track.recordingTitle}` : ""}
											{track.localRecordingId
												? " · новая композиция (демо)"
												: ""}
										</span>
									</td>
									<td>{duration(track.duration)}</td>
									<td>
										{selectedFile
											? selectedFile.codec
											: ids.length
												? "Источник не выбран"
												: "Нет файла"}
										<span className="sub">
											{ids.length} исходников ·{" "}
											{hasAudio(track) ? "есть аудио" : "нет аудио"}
										</span>
									</td>
									<td>
										{selectedFile
											? selectedFile.codec === "FLAC"
												? "24 / 48 кГц"
												: "320 кбит/с"
											: "—"}
									</td>
									<td>
										<span className="sub">
											{fileNames || selectedFile?.name || "—"}
										</span>
										{selectedFile && (
											<span className="sub mono">{selectedFile.path}</span>
										)}
									</td>
									<td>
										{pub
											? `${pub.container} · ${pub.codec}`
											: "Не опубликовано"}
										<span className="sub">
											{pub ? pub.path : "Не опубликовано"}
										</span>
									</td>
									<td>
										<a href={`#tags/${track.id}/track`}>Теги</a>
										{pub && (
											<span className="sub">
												{pub.tags.TITLE?.join(", ") || track.title}
											</span>
										)}
									</td>
								</tr>
							);
						})
					) : (
						<tr>
							<td colSpan={10}>
								<Empty>Нет результатов. Измените запрос или фильтр.</Empty>
							</td>
						</tr>
					)}
				</tbody>
			</table>
		</div>
	);
}

// Provider fixture identity is intentionally resolved by the provider id, not inferred locally.
function originalProvider(id: string) {
	return originalProviders.find((release) => release.id === id);
}
