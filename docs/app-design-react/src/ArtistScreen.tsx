import { useEffect, useMemo, useRef, useState } from "react";
import type { TagScope } from "./TagsComparison";
import { ObjectMetadataForm } from "./ObjectMetadataForm";
import { ReleaseMetadataEditor } from "./metadata/ReleaseMetadataEditor";
import { demoGenres } from "./GenreLibrary";
import { ArtistDeletionRequest } from "./ArtistDeletionRequest";
import {
	availabilityRowClass,
	type CatalogFilter as CatalogFilterValue,
} from "./availabilityRows";
import { CatalogFilter } from "./CatalogFilter";
import {
	type CatalogArtist,
	catalogData,
	metadataChanged,
	type OriginalRelease,
	type OriginalTrack,
	originalArtistName,
	originalProviders,
	type SourceFiles,
	trackNumberLabel,
} from "./catalogData";
import { audioProperties } from "./audioProperties";
import type { DeletionOperation } from "./DeletionRequest";
import { DeletionRequest } from "./DeletionRequest";
import { catalogueStyles } from "./catalogueStyles";
import { useI18n } from "./i18n/I18nProvider";
import { catalogueMessage, type CatalogueMessage } from "./catalogueMessages";
import { trackMessage } from "./trackMessages";
import {
	Button,
	Checkbox,
	Disclosure,
	DisclosureGroup,
	DisclosurePanel,
	Heading,
	Input,
	ListBox,
	ListBoxItem,
	Popover,
	SearchField,
	Select,
	SelectValue,
} from "./ui";

const releaseFields = (release: OriginalRelease) =>
	release.credits?.some((credit) => credit.artistId) ?? false;

export function ArtistScreen({
	artistId,
	artists,
	releases,
	files,
	query: sharedQuery,
	filter: sharedFilter,
	selected = new Set(),
	onSelect,
	onQueryChange,
	onFilterChange,
	onDeleteArtist,
	onQueueDeletion,
	onUpdateArtist,
	onUpdateRelease,
	initialTags,
	initialReleaseId,
	genresLoaded = false,
	onLoadGenres,
}: {
	artistId: string;
	artists: CatalogArtist[];
	releases: OriginalRelease[];
	files: SourceFiles;
	query?: string;
	filter?: CatalogFilterValue;
	selected?: Set<string>;
	onSelect?: (ids: string[], value: boolean) => void;
	onQueryChange?: (value: string) => void;
	onFilterChange?: (value: CatalogFilterValue) => void;
	onDeleteArtist?: (artistId: string) => void;
	onQueueDeletion?: (operation: DeletionOperation) => void;
	onUpdateArtist?: (artist: CatalogArtist) => void;
	onUpdateRelease?: (release: OriginalRelease) => void;
	initialTags?: { scope: TagScope; releaseId?: string };
	initialReleaseId?: string;
	genresLoaded?: boolean;
	onLoadGenres?: () => void | Promise<void>;
}) {
	const { locale } = useI18n();
	const m = (key: CatalogueMessage, values?: Record<string, string | number>) =>
		catalogueMessage(locale, key, values);
	const [localQuery, setLocalQuery] = useState("");
	const [localFilter, setLocalFilter] = useState<CatalogFilterValue>("all");
	const query = sharedQuery ?? localQuery;
	const filter = sharedFilter ?? localFilter;
	const changeQuery = (value: string) => {
		setLocalQuery(value);
		onQueryChange?.(value);
	};
	const changeFilter = (value: CatalogFilterValue) => {
		setLocalFilter(value);
		onFilterChange?.(value);
	};
	const [localArtistUpdates, setLocalArtistUpdates] = useState<
		Record<string, CatalogArtist>
	>({});
	const [localReleaseUpdates, setLocalReleaseUpdates] = useState<
		Record<string, OriginalRelease>
	>({});
	const artist =
		localArtistUpdates[artistId] ??
		artists.find((item) => item.id === artistId);
	const [absentEdition, setAbsentEdition] = useState<Record<string, string>>(
		{},
	);
	const initialTagsTarget = useRef<HTMLDivElement | null>(null);
	useEffect(() => {
		if (!initialTags?.scope) return;
		const target = initialTagsTarget.current;
		if (!target) return;
		target.focus({ preventScroll: true });
		target.scrollIntoView?.({ block: "start" });
	}, [initialTags?.scope, initialTags?.releaseId]);
	const artistReleases = useMemo(
		() =>
			releases
				.map((release) => localReleaseUpdates[release.id] ?? release)
				.filter(
					(release) =>
						release.artistId === artistId ||
						(releaseFields(release) &&
							release.credits?.some((credit) => credit.artistId === artistId)),
				),
		[artistId, localReleaseUpdates, releases],
	);
	const tracks = artistReleases.flatMap((release) => release.tracks);
	const ids = tracks.map((track) => track.id);
	const hasAudio = (track: OriginalTrack) =>
		Boolean(track.published) ||
		[
			...new Set([
				...(track.sourceIds ?? []),
				...(track.sourceId ? [track.sourceId] : []),
			]),
		].some((id) => files[id]?.available && files[id]?.locations?.length);
	const selectedCount = ids.filter((id) => selected.has(id)).length;
	const providerFor = (release: OriginalRelease) =>
		originalProviders.find((item) => item.id === release.providerId);
	const providerComplete = (release: OriginalRelease) => {
		const provider = providerFor(release);
		return Boolean(
			provider?.tracks.length &&
				provider.tracks.length === release.tracks.length,
		);
	};
	const missingGroups = catalogData.absentGroups as MissingGroup[];
	const queryTerms = query
		.normalize("NFKC")
		.toLocaleLowerCase()
		.split(/\s+/)
		.filter(Boolean);
	const filteredMissingGroups = missingGroups.filter((group) => {
		if (
			group.artistId !== artistId ||
			artistReleases.some(
				(release) =>
					(release as OriginalRelease & { releaseGroupId?: string })
						.releaseGroupId === group.groupId,
			)
		)
			return false;
		if (filter !== "all" && filter !== "missing") return false;
		const searchable =
			`${group.title} ${group.editions.map((edition) => `${edition.year} ${edition.country} ${edition.format} ${edition.disambiguation} ${edition.titles.join(" ")}`).join(" ")}`
				.normalize("NFKC")
				.toLocaleLowerCase();
		return queryTerms.every((term) => searchable.includes(term));
	});
	if (!artist)
		return (
			<header className={catalogueStyles.objectHeader}>
				<div>
					<h1>{m("artistNotFound")}</h1>
					<p>{m("artistLinkMismatch")}</p>
				</div>
				<a href="#library">{m("allLibrary")}</a>
			</header>
		);
	const counts = (predicate: (track: OriginalTrack) => boolean) =>
		tracks.filter(predicate).length;
	const expandedReleaseId =
		initialTags?.scope === "release" ? initialTags.releaseId : initialReleaseId;
	const saveArtist = (values: Record<string, string>, genreIds?: string[]) => {
		const selectedGenres = genreIds
			? [
					...genreIds.map((id) => {
						const known = demoGenres.find((item) => item.id === id);
						return (
							known ?? artist.genres?.find((genre) => genre.id === id) ?? { id }
						);
					}),
					...(artist.genres ?? []).filter((genre) => !genre.id),
				]
			: artist.genres;
		const updated = {
			...artist,
			name: values.name,
			sortName: values.sortName,
			disambiguation: values.disambiguation,
			tags: values.tags
				.split(",")
				.map((tag) => tag.trim())
				.filter(Boolean),
			...(genreIds ? { genres: selectedGenres } : {}),
		};
		if (onUpdateArtist) onUpdateArtist(updated);
		else
			setLocalArtistUpdates((current) => ({
				...current,
				[artist.id]: updated,
			}));
	};
	const saveRelease = (updated: OriginalRelease) => {
		if (onUpdateRelease) onUpdateRelease(updated);
		else
			setLocalReleaseUpdates((current) => ({
				...current,
				[updated.id]: updated,
			}));
	};
	return (
		<section className={catalogueStyles.object}>
			<nav className={catalogueStyles.heroBack} aria-label={m("navigation")}>
				<a href="#library">{m("backToLibrary")}</a>
			</nav>
			<header
				className={`${catalogueStyles.objectHeader} ${catalogueStyles.hero}`}
			>
				<div
					className={catalogueStyles.heroArt}
					data-palette={artist.color || "clay"}
				>
					<span aria-hidden="true">
						{artist.name.trim().slice(0, 1).toLocaleUpperCase()}
					</span>
					<i aria-hidden="true" />
					<div className={catalogueStyles.cardSelection}>
						<Checkbox
							aria-label={m("selectAllArtistPositions", {
								artist: artist.name,
							})}
							className={catalogueStyles.cardCheckbox}
							isSelected={ids.length > 0 && selectedCount === ids.length}
							isIndeterminate={selectedCount > 0 && selectedCount < ids.length}
							isDisabled={!ids.length || !onSelect}
							onChange={(value) => onSelect?.(ids, value)}
						>
							<span
								className={catalogueStyles.cardCheckboxBox}
								aria-hidden="true"
							>
								{ids.length > 0 && selectedCount === ids.length
									? "✓"
									: selectedCount > 0
										? "−"
										: ""}
							</span>
							<span className="sr-only">
								{m("selectAllArtistPositionsShort")}
							</span>
						</Checkbox>
					</div>
				</div>
				<div className={catalogueStyles.heroBody}>
					<div className={catalogueStyles.heroHeading}>
						<p className={catalogueStyles.heroKicker}>{m("artistProfile")}</p>
						<h1>{artist.name}</h1>
						<p className={catalogueStyles.heroGenre}>
							{artist.genres
								?.map((genre) => genre.name)
								.filter(Boolean)
								.join(", ") ||
								artist.genre ||
								m("performer")}
						</p>
					</div>
					<div className={catalogueStyles.heroFoot}>
						<p className={catalogueStyles.heroStats}>
							{artistReleases.length} {m("releasesCount")} · {tracks.length}{" "}
							{m("positionsCount")} ·{" "}
							{counts((track) => Boolean(track.published))}{" "}
							{m("publishedCount")} · {filteredMissingGroups.length}{" "}
							{m("missingReleaseCount")}
						</p>
						<div
							className={`${catalogueStyles.actions} ${catalogueStyles.artistDanger}`}
						>
							{onQueueDeletion ? (
								<ArtistDeletionRequest
									artistId={artist.id}
									artists={artists}
									releases={releases}
									onQueue={onQueueDeletion}
								/>
							) : (
								<Button
									className="border-[color-mix(in_srgb,var(--error)_45%,var(--control))]! bg-transparent! text-[var(--error)]!"
									isDisabled={!onDeleteArtist}
									onPress={() => onDeleteArtist?.(artist.id)}
								>
									{m("deleteArtist")}
								</Button>
							)}
						</div>
					</div>
				</div>
			</header>
			<div
				ref={initialTags?.scope === "artist" ? initialTagsTarget : undefined}
				tabIndex={initialTags?.scope === "artist" ? -1 : undefined}
			>
				<ObjectMetadataForm
					key={artist.id}
					label={m("artistDetails")}
					fields={[
						{ key: "name", label: m("artistName"), value: artist.name },
						{
							key: "sortName",
							label: m("sortName"),
							value: artist.sortName ?? "",
						},
						{
							key: "disambiguation",
							label: m("disambiguation"),
							value: artist.disambiguation ?? "",
						},
						{
							key: "tags",
							label: m("tags"),
							// Comma-separated values are a display-prototype convention only.
							value: artist.tags?.join(", ") ?? "",
						},
					]}
					readOnlyFields={[
						{ key: "id", label: m("artistID"), value: artist.id },
						...(
							(
								artist as CatalogArtist & {
									providerLinks?: {
										providerCode: string;
										providerKey: string;
									}[];
								}
							).providerLinks ?? []
						).map((link, index) => ({
							key: `provider-${link.providerCode}-${index}`,
							label: `ID ${link.providerCode}`,
							value: link.providerKey,
						})),
					]}
					genreField={{
						value:
							artist.genres?.flatMap((genre) => (genre.id ? [genre.id] : [])) ??
							[],
						displayValue:
							artist.genres
								?.map((genre) => genre.name)
								.filter(Boolean)
								.join(", ") ||
							artist.genre ||
							"",
						loaded: genresLoaded,
						onLoad: onLoadGenres,
					}}
					required={["name"]}
					onSave={saveArtist}
				/>
			</div>
			<div className={catalogueStyles.controls}>
				<div className={catalogueStyles.panelControls}>
					<SearchField
						aria-label={m("searchLibrary")}
						value={query}
						onChange={changeQuery}
						className={catalogueStyles.search}
					>
						<Input placeholder={m("searchPlaceholder")} />
					</SearchField>
					<CatalogFilter value={filter} onChange={changeFilter} />
				</div>
			</div>
			<DisclosureGroup
				key={artistId}
				className={catalogueStyles.editions}
				allowsMultipleExpanded
				defaultExpandedKeys={
					expandedReleaseId ? new Set([expandedReleaseId]) : undefined
				}
			>
				{artistReleases.map((release, index) => {
					const durationTotal =
						release.tracks.length > 0 &&
						release.tracks.every((track) => Number.isFinite(track.duration))
							? release.tracks.reduce(
									(sum, track) => sum + (track.duration ?? 0),
									0,
								)
							: null;
					const pubCount = release.tracks.filter(
						(track) => track.published,
					).length;
					const provider = providerFor(release);
					const complete = providerComplete(release);
					const changes = release.tracks.filter((track) =>
						metadataChanged(release, track, artists),
					).length;
					return (
						<div className={catalogueStyles.albumRow} key={release.id}>
							<Disclosure className={catalogueStyles.albumCard} id={release.id}>
								<div className={catalogueStyles.cardSelection}>
									<Checkbox
										aria-label={`${m("selectAllEditionPositions")}: ${release.title} · ${release.year} · ${release.catalog || "—"}`}
										className={catalogueStyles.cardCheckbox}
										isSelected={
											release.tracks.length > 0 &&
											release.tracks.every((track) => selected.has(track.id))
										}
										isIndeterminate={
											release.tracks.some((track) => selected.has(track.id)) &&
											!release.tracks.every((track) => selected.has(track.id))
										}
										isDisabled={!release.tracks.length || !onSelect}
										onChange={(value) =>
											onSelect?.(
												release.tracks.map((track) => track.id),
												value,
											)
										}
									>
										<span
											className={catalogueStyles.cardCheckboxBox}
											aria-hidden="true"
										>
											{release.tracks.length > 0 &&
											release.tracks.every((track) => selected.has(track.id))
												? "✓"
												: release.tracks.some((track) => selected.has(track.id))
													? "−"
													: ""}
										</span>
									</Checkbox>
								</div>
								<div className={catalogueStyles.albumHeader}>
									<Heading>
										<Button
											slot="trigger"
											className={`catalogue-edition-trigger ${catalogueStyles.albumTrigger}`}
											aria-label={`${release.title} · ${release.year} · ${release.catalog || "—"}`}
										>
											<span
												className={`artist-album-cover ${catalogueStyles.albumCover}`}
												data-palette={release.color || artist.color || "clay"}
												aria-hidden="true"
											>
												<span>
													{release.title.trim().slice(0, 1).toLocaleUpperCase()}
												</span>
											</span>
											<span className={catalogueStyles.albumCopy}>
												<strong>{release.title}</strong>
												<span className={catalogueStyles.albumMeta}>
													{durationTotal === null
														? m("durationUnknown")
														: `${Math.floor(durationTotal / 60)}:${String(Math.round(durationTotal % 60)).padStart(2, "0")} ${m("total")}`}
													·{" "}
													{release.provider === "Local"
														? "Local"
														: release.providerId
															? `${release.provider || "MusicBrainz"} · ${release.providerId}`
															: m("localRelease")}
												</span>
												<span className={catalogueStyles.albumMeta}>
													{release.year} · {release.tracks.length}{" "}
													{m("positionsCount")} ·{" "}
													{release.country || m("countryUnspecified")} ·{" "}
													{release.label || m("local")} ·{" "}
													{release.catalog || "—"}
												</span>
												<span className={catalogueStyles.albumMeta}>
													{complete && provider
														? `${pubCount}/${provider.tracks.length} ${m("publishedCount")}`
														: `${pubCount} ${m("publishedCount")} · ${m("providerCompletenessUnknown")}`}
													{changes
														? ` · ${m("metadataChangedFor", { count: changes })}`
														: ""}
												</span>
											</span>
										</Button>
									</Heading>
									{onQueueDeletion && (
										<DeletionRequest
											iconOnly
											triggerLabel={`${m("deleteAlbum")} ${release.title} · ${release.year} · ${release.catalog || "—"}`}
											release={release}
											onQueue={onQueueDeletion}
										/>
									)}
								</div>
								<DisclosurePanel className={catalogueStyles.albumPanel}>
									<div
										ref={
											initialTags?.releaseId === release.id
												? initialTagsTarget
												: undefined
										}
										tabIndex={
											initialTags?.releaseId === release.id ? -1 : undefined
										}
										className="grid min-w-0 gap-3"
									>
										<ReleaseMetadataEditor
											release={release}
											artists={artists}
											genresLoaded={genresLoaded}
											onLoadGenres={onLoadGenres}
											local={!onUpdateRelease}
											onSave={saveRelease}
										/>
									</div>
									<div className={catalogueStyles.previewTable}>
										<table>
											<thead>
												<tr>
													{[
														"selectAllEditionPositions",
														"columnNumber",
														"columnRecording",
														"columnProperties",
														"columnHasAudio",
														"columnSourceLinks",
														"columnPublication",
													].map((label) => (
														<th
															scope="col"
															key={label}
															className={
																label === "selectAllEditionPositions"
																	? catalogueStyles.selectionColumn
																	: undefined
															}
														>
															{label === "selectAllEditionPositions" ? (
																<Checkbox
																	className={catalogueStyles.tableCheckbox}
																	aria-label={m(label as CatalogueMessage)}
																	isSelected={
																		release.tracks.length > 0 &&
																		release.tracks.every((track) =>
																			selected.has(track.id),
																		)
																	}
																	isIndeterminate={
																		release.tracks.some((track) =>
																			selected.has(track.id),
																		) &&
																		!release.tracks.every((track) =>
																			selected.has(track.id),
																		)
																	}
																	isDisabled={
																		!release.tracks.length || !onSelect
																	}
																	onChange={(value) =>
																		onSelect?.(
																			release.tracks.map((track) => track.id),
																			value,
																		)
																	}
																>
																	<span
																		className={catalogueStyles.checkboxBox}
																		aria-hidden="true"
																	>
																		{release.tracks.length > 0 &&
																		release.tracks.every((track) =>
																			selected.has(track.id),
																		)
																			? "✓"
																			: release.tracks.some((track) =>
																						selected.has(track.id),
																					)
																				? "−"
																				: ""}
																	</span>
																</Checkbox>
															) : (
																m(label as CatalogueMessage)
															)}
														</th>
													))}
												</tr>
											</thead>
											<tbody>
												{release.tracks.map((track) => {
													const sourceIds = [
														...new Set([
															...(track.sourceIds ?? []),
															...(track.sourceId ? [track.sourceId] : []),
														]),
													];
													const source = track.sourceId
														? files[track.sourceId]
														: undefined;
													return (
														<tr
															key={track.id}
															className={availabilityRowClass(
																track,
																filter,
																hasAudio,
															)}
														>
															<td className={catalogueStyles.selectionColumn}>
																<Checkbox
																	className={catalogueStyles.tableCheckbox}
																	aria-label={m("selectTrack", {
																		track: track.title,
																	})}
																	isSelected={selected.has(track.id)}
																	isDisabled={!onSelect}
																	onChange={(value) =>
																		onSelect?.([track.id], value)
																	}
																>
																	<span
																		className={catalogueStyles.checkboxBox}
																		aria-hidden="true"
																	>
																		{selected.has(track.id) ? "✓" : ""}
																	</span>
																</Checkbox>
															</td>
															<td className="align-middle">
																{trackNumberLabel(release, track)}
															</td>
															<td>
																<a href={`#track/${track.id}`}>{track.title}</a>
																<span className={catalogueStyles.secondary}>
																	{track.duration === null
																		? "—"
																		: `${Math.floor(track.duration / 60)}:${String(Math.round(track.duration % 60)).padStart(2, "0")}`}
																</span>
																{track.recordingId ? null : (
																	<span className={catalogueStyles.secondary}>
																		{m("newRecording")}
																	</span>
																)}
																{track.observedFile && (
																	<span className={catalogueStyles.secondary}>
																		{track.observedFile.state === "changed" ||
																		track.observedFile.state === "drift"
																			? trackMessage(locale, "fileChanged")
																			: track.observedFile.state ===
																						"missing" &&
																					track.observedFile.rootAvailable ===
																						true
																				? trackMessage(locale, "fileMissing")
																				: track.observedFile.state ===
																							"missing" ||
																						track.observedFile.state ===
																							"output-unavailable"
																					? trackMessage(
																							locale,
																							"publicationRootUnavailable",
																						)
																					: track.observedFile.state ===
																								"available" ||
																							track.observedFile.state ===
																								"unchanged"
																						? trackMessage(
																								locale,
																								"noDeviations",
																							)
																						: trackMessage(
																								locale,
																								"fileStateNotChecked",
																							)}
																	</span>
																)}
															</td>
															<td>{audioProperties(source)}</td>
															<td>{hasAudio(track) ? m("yes") : m("no")}</td>
															<td>{sourceIds.length}</td>
															<td>
																{track.published ? (
																	<>
																		{track.published.container} ·{" "}
																		{track.published.codec}
																		{metadataChanged(
																			release,
																			track,
																			artists,
																		) && (
																			<span
																				className={catalogueStyles.secondary}
																			>
																				{m("metadataChanged")}
																			</span>
																		)}
																	</>
																) : (
																	m("notPublished")
																)}
															</td>
														</tr>
													);
												})}
											</tbody>
										</table>
									</div>
									{!release.tracks.length && <p>{m("positionsNotAdded")}</p>}
								</DisclosurePanel>
							</Disclosure>
						</div>
					);
				})}
			</DisclosureGroup>
			{filteredMissingGroups.length > 0 && (
				<section className={catalogueStyles.artistPanel}>
					<h2>{m("missingAlbums")}</h2>
					<p>{m("missingAlbumsDescription")}</p>
					<div className={catalogueStyles.previewTable}>
						<table>
							<thead>
								<tr>
									{[
										"columnAlbum",
										"columnChooseEdition",
										"columnEditionDetails",
										"columnTracks",
									].map((label) => (
										<th scope="col" key={label}>
											{m(label as CatalogueMessage)}
										</th>
									))}
								</tr>
							</thead>
							<tbody>
								{filteredMissingGroups.map((group) => {
									const chosen = group.editions.find(
										(edition) => edition.id === absentEdition[group.id],
									);
									return (
										<tr key={group.id}>
											<th scope="row">
												{group.title}
												<span className={catalogueStyles.secondary}>
													{originalArtistName(group.artistId, artists)} ·
													{m("albumNotInLibrary")}
												</span>
											</th>
											<td>
												<Select
													aria-label={m("chooseEditionForGroup", {
														group: group.title,
													})}
													selectedKey={absentEdition[group.id] || "none"}
													onSelectionChange={(key) =>
														setAbsentEdition((value) => ({
															...value,
															[group.id]:
																String(key) === "none" ? "" : String(key),
														}))
													}
													className={catalogueStyles.select}
												>
													<Button>
														<SelectValue />
														<span aria-hidden="true">⌄</span>
													</Button>
													<Popover className={catalogueStyles.popover}>
														<ListBox>
															<ListBoxItem id="none">
																{m("chooseSpecificEdition")}
															</ListBoxItem>
															{group.editions.map((edition) => (
																<ListBoxItem key={edition.id} id={edition.id}>
																	{edition.year} · {edition.country} ·{" "}
																	{edition.format} · {edition.titles.length}{" "}
																	{m("positionAbbreviation")} ·{" "}
																	{edition.disambiguation}
																</ListBoxItem>
															))}
														</ListBox>
													</Popover>
												</Select>
											</td>
											<td>
												{chosen
													? `${chosen.year} · ${chosen.country} · ${chosen.format} · ${chosen.disambiguation}`
													: "—"}
											</td>
											<td>
												{chosen
													? chosen.titles.map((title, index) => (
															<div key={`${index}:${title}`}>
																{index + 1}. {title}
															</div>
														))
													: m("chooseEditionNoLibraryChange")}
											</td>
										</tr>
									);
								})}
							</tbody>
						</table>
					</div>
				</section>
			)}
		</section>
	);
}

type MissingGroup = {
	id: string;
	groupId: string;
	artistId: string;
	title: string;
	editions: {
		id: string;
		year: string;
		country: string;
		format: string;
		disambiguation: string;
		titles: string[];
	}[];
};
