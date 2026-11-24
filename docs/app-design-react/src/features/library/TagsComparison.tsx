import { useEffect, useState } from "react";
import {
	type CatalogArtist,
	desiredTags,
	type OriginalRelease,
	type OriginalTrack,
	originalArtists,
	originalProviders,
	type SourceFiles,
} from "./catalogData";
import { useI18n } from "../../i18n/I18nProvider";
import {
	comparisonControls,
	comparisonNote,
	metadataPanel,
	objectActions,
} from "../../components/styles/matchingStyles";
import {
	LibraryTagEvidenceComparison,
	libraryTagComparisonCopy,
} from "./LibraryTagComparisonTable";
import {
	libraryTagComparisonFields,
	type MetadataField,
} from "./libraryTagComparisonFields";
import {
	type MetadataValues,
	projectMetadataValues,
} from "../../metadata/projection";
import {
	formatTrackMessage,
	type TrackMessage,
	trackMessage,
} from "./trackMessages";
import {
	Button,
	Label,
	ListBox,
	ListBoxItem,
	Popover,
	Select,
	SelectValue,
	Tab,
	TabList,
	Tabs,
} from "../../components/ui";

export type TagScope = "artist" | "release" | "track";

const scopeMessage: Record<TagScope, TrackMessage> = {
	artist: "scopeArtist",
	release: "scopeRelease",
	track: "scopeTrack",
};

const artistGroups = new Set(["recording artists", "release artists"]);
const fieldForTab = (tab: TagScope, field: MetadataField) => {
	if (tab === "artist") return artistGroups.has(field.group);
	if (tab === "release") return field.scope === "release";
	return (
		field.scope === "track" ||
		field.scope === "audio" ||
		(field.scope === "recording" && !artistGroups.has(field.group))
	);
};

function explicitOverrides(
	release: OriginalRelease,
	track: OriginalTrack,
	fileId: string | null,
) {
	const values: MetadataValues = {
		...(release.comparisonOverrides ?? {}),
		...(fileId ? (track.audioComparisonOverrides?.[fileId] ?? {}) : {}),
		...(track.recordingComparisonOverrides ?? {}),
		...(track.positionComparisonOverrides ?? {}),
	};
	const tags = projectMetadataValues(values).tags;
	return Object.keys(tags).length ? tags : undefined;
}

export function TagsComparison({
	releases,
	providers = originalProviders,
	files,
	scope,
	initialTrackId,
	initialFileId,
	onPreview,
	onSelectFile,
	allowScopeChange = false,
	fixedContext = false,
}: {
	releases: OriginalRelease[];
	artists?: CatalogArtist[];
	providers?: OriginalRelease[];
	files: SourceFiles;
	scope: TagScope;
	initialTrackId?: string;
	initialFileId?: string | null;
	onPreview?: (ids: string[]) => void;
	onSelectFile?: (trackId: string, fileId: string) => void;
	allowScopeChange?: boolean;
	fixedContext?: boolean;
}) {
	const { locale } = useI18n();
	const tracks = releases.flatMap((release) =>
		release.tracks.map((track) => ({ release, track })),
	);
	const [trackId, setTrackId] = useState(
		initialTrackId ?? tracks[0]?.track.id ?? "",
	);
	const [fileOverride, setFileOverride] = useState<string | null | undefined>();
	const [differencesOnly, setDifferencesOnly] = useState(false);
	const [showAll, setShowAll] = useState(false);
	const [activeScope, setActiveScope] = useState<TagScope>(scope);
	useEffect(() => setActiveScope(scope), [scope]);
	useEffect(() => {
		if (fixedContext && initialTrackId) setTrackId(initialTrackId);
	}, [fixedContext, initialTrackId]);
	const selected =
		tracks.find(({ track }) => track.id === trackId) ??
		(initialTrackId ? undefined : tracks[0]);
	if (!selected) return <p>{trackMessage(locale, "noPositions")}</p>;
	const { release, track } = selected;
	const linkedFileIds = [
		...new Set([
			...(track.sourceIds ?? []),
			...(track.sourceId ? [track.sourceId] : []),
		]),
	];
	const fileId = fixedContext
		? initialFileId === undefined
			? track.sourceId
			: initialFileId
		: fileOverride === undefined
			? track.sourceId
			: fileOverride && linkedFileIds.includes(fileOverride)
				? fileOverride
				: null;
	const file = fileId ? files[fileId] : undefined;
	const unscoped = fixedContext || showAll;
	const knownFields = libraryTagComparisonFields(
		locale,
		unscoped ? undefined : (field) => fieldForTab(activeScope, field),
	);
	const knownKeys = new Set(knownFields.map(({ key }) => key));
	const scopedTags = (tags: Record<string, string[]> | undefined) =>
		tags && !unscoped
			? Object.fromEntries(
					Object.entries(tags).filter(([key]) => knownKeys.has(key)),
				)
			: tags;
	const boundRelease = providers.find(
		(candidate) =>
			candidate.id === release.providerId ||
			(candidate.providerId && candidate.providerId === release.providerId),
	);
	const providerTrack = track.providerTrackId
		? boundRelease?.tracks.find(
				(candidate) => candidate.id === track.providerTrackId,
			)
		: undefined;
	const providerName =
		boundRelease?.provider ??
		(track.providerTrackId ? release.provider || "MusicBrainz" : undefined);
	const provider = !track.providerTrackId
		? { status: "unlinked" as const }
		: providerTrack && boundRelease
			? {
					name: providerName,
					status: "saved" as const,
					tags:
						scopedTags(
							desiredTags(boundRelease, providerTrack, originalArtists, null),
						) ?? {},
				}
			: { name: providerName, status: "unavailable" as const };
	const sourceTags = scopedTags(file?.tags);
	const publicationTags = scopedTags(track.published?.tags);
	const overrides = scopedTags(explicitOverrides(release, track, fileId));
	return (
		<section
			className={metadataPanel}
			aria-label={formatTrackMessage(locale, "tagsFor", {
				scope: trackMessage(locale, scopeMessage[activeScope]),
			})}
		>
			{fixedContext ? (
				<header className="grid gap-1 border-b border-[var(--line)] pb-2">
					<h3 className="m-0 text-sm font-semibold">
						{trackMessage(locale, "tagsTitle")}
					</h3>
					<p className="m-0 text-xs text-[var(--muted)]">
						{track.title} · {trackMessage(locale, "fixedFile")}:{" "}
						{file?.name ?? trackMessage(locale, "noSourceSelected")}
						{file &&
							` · ${file.codec} · ${Math.floor(file.duration / 60)}:${String(Math.round(file.duration % 60)).padStart(2, "0")}`}
					</p>
				</header>
			) : (
				<div className={comparisonControls}>
					<Select
						aria-label={trackMessage(locale, "comparisonPosition")}
						selectedKey={track.id}
						onSelectionChange={(key) => {
							setTrackId(String(key));
							setFileOverride(undefined);
						}}
					>
						<Label>{trackMessage(locale, "position")}</Label>
						<Button>
							<SelectValue />
							<span aria-hidden="true">⌄</span>
						</Button>
						<Popover>
							<ListBox>
								{tracks.map(({ release: itemRelease, track: item }) => (
									<ListBoxItem key={item.id} id={item.id}>
										{String(item.position).padStart(2, "0")} · {item.title} ·{" "}
										{itemRelease.title}
									</ListBoxItem>
								))}
							</ListBox>
						</Popover>
					</Select>
					<Select
						aria-label={trackMessage(locale, "comparisonSourceFile")}
						selectedKey={fileId ?? ""}
						onSelectionChange={(key) => {
							const value = String(key);
							setFileOverride(value || null);
							if (value) onSelectFile?.(track.id, value);
						}}
					>
						<Label>{libraryTagComparisonCopy[locale].source}</Label>
						<Button>
							<SelectValue />
							<span aria-hidden="true">⌄</span>
						</Button>
						<Popover>
							<ListBox>
								<ListBoxItem id="">
									{trackMessage(locale, "noSourceSelected")}
								</ListBoxItem>
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
				</div>
			)}
			{allowScopeChange && !fixedContext && (
				<Tabs
					selectedKey={activeScope}
					onSelectionChange={(key) => setActiveScope(key as TagScope)}
				>
					<TabList aria-label={trackMessage(locale, "comparisonContext")}>
						{(
							[
								["artist", trackMessage(locale, "scopeArtist")],
								["release", trackMessage(locale, "scopeRelease")],
								["track", trackMessage(locale, "scopeTrack")],
							] as const
						).map(([key, label]) => (
							<Tab key={key} id={key}>
								{label}
							</Tab>
						))}
					</TabList>
				</Tabs>
			)}
			<LibraryTagEvidenceComparison
				locale={locale}
				source={sourceTags}
				current={publicationTags}
				provider={provider}
				overrides={overrides}
				knownFields={knownFields}
				showAll={showAll}
				onShowAllChange={setShowAll}
				differencesOnly={differencesOnly}
				onDifferencesChange={setDifferencesOnly}
				showDifferencesControl={Boolean(sourceTags && publicationTags)}
			/>
			<p className={comparisonNote}>
				{file?.path ?? trackMessage(locale, "noSourceSelected")}
				<br />
				{track.published
					? formatTrackMessage(locale, "publicationPath", {
							path: track.published.path,
							readAt: track.published.readAt,
						})
					: trackMessage(locale, "publicationAbsentNote")}
			</p>
			{onPreview && (
				<div className={objectActions}>
					<Button onPress={() => onPreview([track.id])}>
						{trackMessage(locale, "previewPublication")}
					</Button>
				</div>
			)}
		</section>
	);
}
