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
import { audioProperties } from "./audioProperties";
import { availabilityRowClass, type CatalogFilter } from "./availabilityRows";
import { artistReleaseHref } from "./artistReleaseRoute";
import { catalogueStyles, catalogueTableWidths } from "./catalogueStyles";
import { useI18n } from "./i18n/I18nProvider";
import {
	libraryRowsMessage,
	type LibraryRowsMessage,
} from "./libraryRowsMessages";
import {
	Button,
	Cell,
	Column,
	Link,
	Row,
	Table,
	TableBody,
	TableHeader,
} from "./ui";

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
	filter?: CatalogFilter;
	groupCheckbox: (
		ids: string[],
		label: string,
		appearance?: "table" | "artwork",
	) => ReactNode;
	expanded: string | null;
	onToggleAlbum: (id: string) => void;
}
const sourceIds = (track: Pick<OriginalTrack, "sourceId" | "sourceIds">) => [
	...new Set([
		...(track.sourceIds ?? []),
		...(track.sourceId ? [track.sourceId] : []),
	]),
];
const MobileField = ({
	label,
	children,
}: {
	label: string;
	children: ReactNode;
}) => (
	<dl className={`${catalogueStyles.mobileCatalogField} m-0`}>
		<dt>{label}</dt>
		<dd>{children}</dd>
	</dl>
);
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
	filter = "all",
	groupCheckbox,
	expanded,
	onToggleAlbum,
}: Props) {
	const { locale } = useI18n();
	const m = (
		key: LibraryRowsMessage,
		values?: Record<string, string | number>,
	) => libraryRowsMessage(locale, key, values);
	if (view === "artists") {
		return (
			<div
				className={catalogueStyles.artistGrid}
				aria-label={m("artistsLibrary")}
			>
				{artists.length ? (
					artists.map((artist) => {
						const own = releases.filter(
							(release) =>
								release.artistId === artist.id ||
								release.credits?.some(
									(credit) => credit.artistId === artist.id,
								),
						);
						const ownTracks = own.flatMap((release) => release.tracks);
						const availableCount = ownTracks.filter(hasAudio).length;
						const publishedCount = ownTracks.filter((track) =>
							Boolean(track.published),
						).length;
						return (
							<article className={catalogueStyles.artistCard} key={artist.id}>
								<div
									className={catalogueStyles.artistArt}
									data-palette={artist.color || "clay"}
								>
									<Link
										href={`#artist/${artist.id}`}
										className={catalogueStyles.artistCardLink}
									>
										<span
											className={catalogueStyles.artistMonogram}
											aria-hidden="true"
										>
											{artist.name.slice(0, 1)}
										</span>
										<span className={catalogueStyles.artistOverlay}>
											<span className={catalogueStyles.artistCopy}>
												<strong>{artist.name}</strong>
												<span>{artist.genre || m("performer")}</span>
											</span>
											<span className={catalogueStyles.artistStats}>
												<span>
													<strong>
														{m("releasesCount", { count: own.length })}
													</strong>
												</span>
												<span>
													<strong>
														{m("positionsCount", { count: ownTracks.length })}
													</strong>
												</span>
												<span>
													<strong>
														{m("audioCount", {
															available: availableCount,
															total: ownTracks.length,
														})}
													</strong>
												</span>
												<span>
													<strong>
														{m("publishedCount", {
															published: publishedCount,
															total: ownTracks.length,
														})}
													</strong>
												</span>
												<span>
													<strong>
														{m("missingEditionsCount", {
															count: missingCounts.get(artist.id) || "—",
														})}
													</strong>
												</span>
											</span>
										</span>
									</Link>
									<div className={catalogueStyles.cardSelection}>
										{groupCheckbox(
											ownTracks.map((track) => track.id),
											m("selectArtistPositions", { artist: artist.name }),
											"artwork",
										)}
									</div>
								</div>
							</article>
						);
					})
				) : (
					<div className={catalogueStyles.empty}>{m("noArtists")}</div>
				)}
			</div>
		);
	}
	const tracks = releases.flatMap((release) =>
		release.tracks.map((track) => ({ release, track })),
	);
	const tableTrackIds = tracks.map(({ track }) => track.id);
	const columns =
		view === "albums"
			? [
					m("select"),
					m("album"),
					m("artist"),
					m("yearEdition"),
					m("positions"),
					m("sources"),
					m("audio"),
					m("publication"),
					m("provider"),
				]
			: [
					m("selection"),
					m("release"),
					m("mediumNumber"),
					m("recording"),
					m("duration"),
					m("source"),
					m("properties"),
					m("filePath"),
					m("publication"),
					m("tags"),
				];
	return (
		<div className={catalogueStyles.tableScroll}>
			<div
				className={catalogueStyles.mobileCatalog}
				aria-label={view === "albums" ? m("albumsTable") : m("tracksTable")}
			>
				<div className={catalogueStyles.mobileCatalogRow}>
					<MobileField label={m("selectAllTablePositions")}>
						{groupCheckbox(tableTrackIds, m("selectAllTablePositions"))}
					</MobileField>
				</div>
				{view === "albums"
					? releases.map((release) => {
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
								<article
									className={catalogueStyles.mobileCatalogRow}
									key={release.id}
								>
									<MobileField label={m("select")}>
										{groupCheckbox(
											release.tracks.map((track) => track.id),
											m("selectAlbumPositions", { album: release.title }),
										)}
									</MobileField>
									<MobileField label={m("album")}>
										<Button
											className={catalogueStyles.expander}
											aria-expanded={expanded === release.id}
											onPress={() => onToggleAlbum(release.id)}
										>
											{release.title || m("untitled")}
										</Button>
										{artistReleaseHref(release, artists) && (
											<a
												className={catalogueStyles.sub}
												href={artistReleaseHref(release, artists)!}
											>
												{m("openRelease")}
											</a>
										)}
									</MobileField>
									<MobileField label={m("artist")}>
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
											m("artistNotSpecified")
										)}
									</MobileField>
									<MobileField label={m("yearEdition")}>
										{release.year || m("dateNotSpecified")} ·{" "}
										{release.country || m("local")} · {release.catalog || "—"}
									</MobileField>
									<MobileField label={m("positions")}>
										{release.tracks.length}
									</MobileField>
									<MobileField label={m("sources")}>{sourceCount}</MobileField>
									<MobileField label={m("audio")}>
										{m("audioSummary", {
											count: release.tracks.filter(hasAudio).length,
										})}
										{provider?.tracks.length
											? ` / ${provider.tracks.length}`
											: ""}
									</MobileField>
									<MobileField label={m("publication")}>
										{m("publishedCount", {
											published,
											total: release.tracks.length,
										})}
										<span className={catalogueStyles.sub}>
											{provider?.tracks.length === release.tracks.length
												? m("completenessKnown")
												: m("completenessUnknown")}
										</span>
									</MobileField>
									<MobileField label={m("provider")}>
										{provider ? "MusicBrainz" : m("localCollection")}
									</MobileField>
									{expanded === release.id && (
										<div className={catalogueStyles.mobileCatalogTracks}>
											{release.tracks.map((track) => {
												const ids = sourceIds(track);
												const source = track.sourceId
													? files[track.sourceId]
													: undefined;
												const pub =
													track.published && typeof track.published === "object"
														? track.published
														: null;
												return (
													<div
														className={catalogueStyles.mobileCatalogGroup}
														key={track.id}
													>
														<MobileField label={m("select")}>
															{groupCheckbox(
																[track.id],
																m("selectTrack", { track: track.title }),
															)}
														</MobileField>
														<MobileField label={m("mediumNumber")}>
															{trackNumberLabel(release, track)}
														</MobileField>
														<MobileField label={m("recording")}>
															<a href={`#track/${track.id}`}>{track.title}</a>
															<span className={catalogueStyles.sub}>
																{originalArtistName(track.artistId, artists)}
																{track.recordingTitle
																	? ` · ${track.recordingTitle}`
																	: ""}
															</span>
															{!track.recordingId && (
																<span className={catalogueStyles.sub}>
																	{m("newRecording")}
																</span>
															)}
														</MobileField>
														<MobileField label={m("duration")}>
															{duration(track.duration)}
														</MobileField>
														<MobileField label={m("source")}>
															{source?.codec ??
																(ids.length
																	? m("noSourceSelected")
																	: m("noFile"))}
															<span className={catalogueStyles.sub}>
																{m("sourceCount", { count: ids.length })} ·{" "}
																{hasAudio(track) ? m("hasAudio") : m("noAudio")}
															</span>
														</MobileField>
														<MobileField label={m("properties")}>
															{audioProperties(source)}
														</MobileField>
														<MobileField label={m("filePath")}>
															{ids
																.map((id) => files[id]?.path || files[id]?.name)
																.filter(Boolean)
																.join(" · ") || "—"}
														</MobileField>
														<MobileField label={m("publication")}>
															{pub
																? `${pub.container} · ${pub.codec}`
																: m("notPublished")}
														</MobileField>
														<MobileField label={m("tags")}>
															<a href={`#tags/${track.id}/track`}>
																{m("tags")}
															</a>
															{pub && (
																<span className={catalogueStyles.sub}>
																	{pub.tags.TITLE?.join(", ") || track.title}
																</span>
															)}
														</MobileField>
													</div>
												);
											})}
										</div>
									)}
								</article>
							);
						})
					: tracks.map(({ release, track }) => {
							const ids = sourceIds(track);
							const selectedFile = track.sourceId
								? files[track.sourceId]
								: undefined;
							const pub =
								track.published && typeof track.published === "object"
									? track.published
									: null;
							return (
								<article
									className={catalogueStyles.mobileCatalogRow}
									key={track.id}
								>
									<MobileField label={m("selection")}>
										{groupCheckbox(
											[track.id],
											m("selectTrack", { track: track.title }),
										)}
									</MobileField>
									<MobileField label={m("release")}>
										{artistReleaseHref(release, artists) ? (
											<a href={artistReleaseHref(release, artists)!}>
												{release.title}
											</a>
										) : (
											release.title
										)}
										<span className={catalogueStyles.sub}>
											{originalArtistName(release.artistId, artists)} ·{" "}
											{release.year}
										</span>
									</MobileField>
									<MobileField label={m("mediumNumber")}>
										{release.media?.[(track.medium || 1) - 1]?.title ||
											track.medium ||
											"—"}{" "}
										· {trackNumberLabel(release, track)}
									</MobileField>
									<MobileField label={m("recording")}>
										<a href={`#track/${track.id}`}>{track.title}</a>
										<span className={catalogueStyles.sub}>
											{originalArtistName(track.artistId, artists)}
											{track.recordingTitle ? ` · ${track.recordingTitle}` : ""}
										</span>
									</MobileField>
									<MobileField label={m("duration")}>
										{duration(track.duration)}
									</MobileField>
									<MobileField label={m("source")}>
										{selectedFile?.codec ??
											(ids.length ? m("noSourceSelected") : m("noFile"))}
										<span className={catalogueStyles.sub}>
											{m("sourceCount", { count: ids.length })} ·{" "}
											{hasAudio(track) ? m("hasAudio") : m("noAudio")}
										</span>
									</MobileField>
									<MobileField label={m("properties")}>
										{audioProperties(selectedFile)}
									</MobileField>
									<MobileField label={m("filePath")}>
										{ids
											.map((id) => files[id]?.path || files[id]?.name)
											.filter(Boolean)
											.join(" · ") || "—"}
									</MobileField>
									<MobileField label={m("publication")}>
										{pub
											? `${pub.container} · ${pub.codec}`
											: m("notPublished")}
									</MobileField>
									<MobileField label={m("tags")}>
										<a href={`#tags/${track.id}/track`}>{m("tags")}</a>
										{pub && (
											<span className={catalogueStyles.sub}>
												{pub.tags.TITLE?.join(", ") || track.title}
											</span>
										)}
									</MobileField>
								</article>
							);
						})}
			</div>
			<div className="max-[599px]:hidden">
				<Table
					key={view}
					aria-label={view === "albums" ? m("albumsTable") : m("tracksTable")}
					className={`${catalogueStyles.table} ${catalogueTableWidths[view]}`}
				>
					<TableHeader>
						{columns.map((column, index) => (
							<Column key={column} isRowHeader={index === 1}>
								{index === 0
									? groupCheckbox(tableTrackIds, m("selectAllTablePositions"))
									: column}
							</Column>
						))}
					</TableHeader>
					{view === "albums" ? (
						<TableBody
							items={releases}
							dependencies={[
								releases,
								artists,
								groupCheckbox,
								hasAudio,
								files,
								expanded,
								filter,
							]}
							renderEmptyState={() => (
								<div className={catalogueStyles.empty}>{m("noResults")}</div>
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
												m("selectAlbumPositions", { album: release.title }),
											)}
										</Cell>
										<Cell>
											<Button
												className={catalogueStyles.expander}
												aria-expanded={expanded === release.id}
												onPress={() => onToggleAlbum(release.id)}
											>
												{release.title || m("untitled")}
											</Button>
											<span className={catalogueStyles.sub}>
												{artistReleaseHref(release, artists) ? (
													<a
														href={
															artistReleaseHref(release, artists) ?? undefined
														}
													>
														{m("openRelease")}
													</a>
												) : (
													m("artistNotSpecified")
												)}
											</span>
											{expanded === release.id && (
												<div className={catalogueStyles.inlineTracks}>
													<table className={catalogueStyles.inlineTable}>
														<thead>
															<tr>
																{[
																	m("select"),
																	m("mediumNumber"),
																	m("recording"),
																	m("duration"),
																	m("source"),
																	m("properties"),
																	m("filePath"),
																	m("publication"),
																	m("tags"),
																].map((heading, headingIndex) => (
																	<th
																		key={heading}
																		scope="col"
																		className={
																			headingIndex === 0
																				? catalogueStyles.selectionColumn
																				: undefined
																		}
																	>
																		{headingIndex === 0
																			? groupCheckbox(
																					release.tracks.map(
																						(track) => track.id,
																					),
																					m("selectAllReleasePositions"),
																				)
																			: heading}
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
																		className={`${availabilityRowClass(
																			track as OriginalTrack,
																			filter,
																			hasAudio,
																		)} ${catalogueStyles.filterRows}`}
																	>
																		<td
																			className={
																				catalogueStyles.selectionColumn
																			}
																		>
																			{groupCheckbox(
																				[track.id],
																				m("selectTrack", {
																					track: track.title,
																				}),
																			)}
																		</td>
																		<td>{trackNumberLabel(release, track)}</td>
																		<td>
																			<a href={`#track/${track.id}`}>
																				{track.title}
																			</a>
																			<span className={catalogueStyles.sub}>
																				{originalArtistName(
																					track.artistId,
																					artists,
																				)}
																				{track.recordingTitle
																					? ` · ${track.recordingTitle}`
																					: ""}
																			</span>
																			{track.recordingId ? null : (
																				<span className={catalogueStyles.sub}>
																					{m("newRecording")}
																				</span>
																			)}
																		</td>
																		<td>{duration(track.duration)}</td>
																		<td>
																			{source?.codec ??
																				(ids.length
																					? m("noSourceSelected")
																					: m("noFile"))}
																			<span className={catalogueStyles.sub}>
																				{m("sourceCount", {
																					count: ids.length,
																				})}{" "}
																				·{" "}
																				{hasAudio(track)
																					? m("hasAudio")
																					: m("noAudio")}
																			</span>
																		</td>
																		<td>{audioProperties(source)}</td>
																		<td>
																			{ids
																				.map(
																					(id) =>
																						files[id]?.path || files[id]?.name,
																				)
																				.filter(Boolean)
																				.map((path) => (
																					<span
																						className={catalogueStyles.path}
																						key={path}
																					>
																						{path}
																					</span>
																				))}
																		</td>
																		<td>
																			{pub
																				? `${pub.container} · ${pub.codec}`
																				: m("notPublished")}
																		</td>
																		<td>
																			<a href={`#tags/${track.id}/track`}>
																				{m("tags")}
																			</a>
																			{pub && (
																				<span className={catalogueStyles.sub}>
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
												m("artistNotSpecified")
											)}
										</Cell>
										<Cell>
											{release.year || m("dateNotSpecified")}
											<span className={catalogueStyles.sub}>
												{release.country || m("local")} ·{" "}
												{release.catalog || "—"}
											</span>
										</Cell>
										<Cell>{release.tracks.length}</Cell>
										<Cell>{sourceCount}</Cell>
										<Cell>
											{m("audioSummary", {
												count: release.tracks.filter(hasAudio).length,
											})}
											{provider?.tracks.length
												? ` / ${provider.tracks.length}`
												: ""}
										</Cell>
										<Cell>
											{m("publishedCount", {
												published,
												total: release.tracks.length,
											})}
											<span className={catalogueStyles.sub}>
												{provider?.tracks.length === release.tracks.length
													? m("completenessKnown")
													: m("completenessUnknown")}
											</span>
										</Cell>
										<Cell>
											{provider ? "MusicBrainz" : m("localCollection")}
										</Cell>
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
								filter,
							]}
							renderEmptyState={() => (
								<div className={catalogueStyles.empty}>{m("noResults")}</div>
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
										className={`${availabilityRowClass(
											track as OriginalTrack,
											filter,
											hasAudio,
										)} ${catalogueStyles.filterRows}`}
									>
										<Cell>
											{groupCheckbox(
												[track.id],
												m("selectTrack", { track: track.title }),
											)}
										</Cell>
										<Cell>
											{artistReleaseHref(release, artists) ? (
												<a
													href={
														artistReleaseHref(release, artists) ?? undefined
													}
												>
													{release.title}
												</a>
											) : (
												release.title
											)}
											<span className={catalogueStyles.sub}>
												{originalArtistName(release.artistId, artists)} ·{" "}
												{release.year}
											</span>
										</Cell>
										<Cell>
											{release.media?.[(track.medium || 1) - 1]?.title ||
												track.medium ||
												"—"}
											<span className={catalogueStyles.sub}>
												{trackNumberLabel(release, track)}
											</span>
										</Cell>
										<Cell>
											<a href={`#track/${track.id}`}>{track.title}</a>
											<span className={catalogueStyles.sub}>
												{originalArtistName(track.artistId, artists)}
												{track.recordingTitle
													? ` · ${track.recordingTitle}`
													: ""}
											</span>
										</Cell>
										<Cell>{duration(track.duration)}</Cell>
										<Cell>
											{selectedFile?.codec ??
												(ids.length ? m("noSourceSelected") : m("noFile"))}
											<span className={catalogueStyles.sub}>
												{m("sourceCount", { count: ids.length })} ·{" "}
												{hasAudio(track) ? m("hasAudio") : m("noAudio")}
											</span>
										</Cell>
										<Cell>{audioProperties(selectedFile)}</Cell>
										<Cell>
											{ids
												.map((id) => files[id]?.path || files[id]?.name)
												.filter(Boolean)
												.map((path) => (
													<span className={catalogueStyles.path} key={path}>
														{path}
													</span>
												))}
										</Cell>
										<Cell>
											{pub
												? `${pub.container} · ${pub.codec}`
												: m("notPublished")}
										</Cell>
										<Cell>
											<a href={`#tags/${track.id}/track`}>{m("tags")}</a>
											{pub && (
												<span className={catalogueStyles.sub}>
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
		</div>
	);
}
