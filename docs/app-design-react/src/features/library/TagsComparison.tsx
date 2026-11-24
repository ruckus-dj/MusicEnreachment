import { useEffect, useState } from "react";
import {
	type CatalogArtist,
	desiredTags,
	type OriginalRelease,
	originalProviders,
	type SourceFiles,
	sourceOnlyAudioTagKeys,
} from "./catalogData";
import { useI18n } from "../../i18n/I18nProvider";
import {
	comparisonControls,
	comparisonNote,
	metadataPanel,
	objectActions,
} from "../../components/styles/matchingStyles";
import {
	libraryTagComparisonCopy,
	LibraryTagComparisonTable,
	LibraryTagDifferencesControl,
} from "./LibraryTagComparisonTable";
import { metadataFieldLabel, navidromeFields } from "../../metadata/registry";
import {
	formatTrackMessage,
	type TrackMessage,
	trackMessage,
} from "./trackMessages";
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
	Tabs,
} from "../../components/ui";

export type TagScope = "artist" | "release" | "track";

const scopeMessage: Record<TagScope, TrackMessage> = {
	artist: "scopeArtist",
	release: "scopeRelease",
	track: "scopeTrack",
};

const artistGroups = new Set(["recording artists", "release artists"]);
const fieldsForTab = (tab: TagScope) =>
	navidromeFields.filter((field) => {
		if (tab === "artist") return artistGroups.has(field.group);
		if (tab === "release") return field.scope === "release";
		return (
			field.scope === "track" ||
			field.scope === "audio" ||
			(field.scope === "recording" && !artistGroups.has(field.group))
		);
	});

export function TagsComparison({
	releases,
	artists,
	files,
	scope,
	initialTrackId,
	initialFileId,
	onPreview,
	onSelectFile,
	allowScopeChange = false,
	showAllFields = false,
	fixedContext = false,
}: {
	releases: OriginalRelease[];
	artists?: CatalogArtist[];
	files: SourceFiles;
	scope: TagScope;
	initialTrackId?: string;
	initialFileId?: string | null;
	onPreview?: (ids: string[]) => void;
	onSelectFile?: (trackId: string, fileId: string) => void;
	allowScopeChange?: boolean;
	showAllFields?: boolean;
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
	const [allFields, setAllFields] = useState(false);
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
	const provider = originalProviders
		.flatMap((item) =>
			item.tracks.map((candidate) => ({ release: item, track: candidate })),
		)
		.find(({ track: candidate }) => candidate.id === track.providerTrackId);
	const sourceTags = file?.tags ?? {};
	const providerTags = provider
		? desiredTags(provider.release, provider.track, artists)
		: null;
	const localTags = desiredTags(release, track, artists);
	const publicationTags = track.published?.tags ?? {};
	const rows = (allFields ? navidromeFields : fieldsForTab(activeScope))
		.map((field) => {
			const key = field.tag;
			const values = [
				sourceTags[key] ?? [],
				provider ? (providerTags?.[key] ?? []) : null,
				localTags[key] ?? [],
				track.published ? (publicationTags[key] ?? []) : null,
			];
			return {
				key,
				label: metadataFieldLabel(field.key, locale),
				values,
				different:
					new Set(values.map((value) => JSON.stringify(value ?? []))).size > 1,
			};
		})
		.concat(
			(activeScope === "track" || allFields
				? sourceOnlyAudioTagKeys.filter((key) => Object.hasOwn(sourceTags, key))
				: []
			).map((key) => {
				const values = [
					sourceTags[key] ?? [],
					provider ? (providerTags?.[key] ?? []) : null,
					localTags[key] ?? [],
					track.published ? (publicationTags[key] ?? []) : null,
				];
				return {
					key,
					label: key,
					values,
					different:
						new Set(values.map((value) => JSON.stringify(value ?? []))).size >
						1,
				};
			}),
		)
		.filter((row) => !differencesOnly || row.different);
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
			{showAllFields && (
				<Checkbox isSelected={allFields} onChange={setAllFields}>
					<span className="checkbox" />
					{trackMessage(locale, "allFields")}
				</Checkbox>
			)}
			<LibraryTagComparisonTable
				fieldLabel={libraryTagComparisonCopy[locale].field}
				columns={[
					{
						id: "source",
						label: libraryTagComparisonCopy[locale].source,
						unavailable: trackMessage(locale, "notPublished"),
					},
					{
						id: "provider",
						label: trackMessage(locale, "providerCache"),
						unavailable: trackMessage(locale, "noProviderLink"),
					},
					{
						id: "local",
						label: trackMessage(locale, "localLibrary"),
						unavailable: trackMessage(locale, "notPublished"),
					},
					{
						id: "publication",
						label: libraryTagComparisonCopy[locale].current,
						unavailable: trackMessage(locale, "notPublished"),
					},
				]}
				rows={rows}
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
			<div className={objectActions}>
				<LibraryTagDifferencesControl
					locale={locale}
					selected={differencesOnly}
					onChange={setDifferencesOnly}
				/>
				{onPreview && (
					<Button onPress={() => onPreview([track.id])}>
						{trackMessage(locale, "previewPublication")}
					</Button>
				)}
			</div>
		</section>
	);
}
