import { useEffect, useState } from "react";
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
} from "./ui";
import {
	desiredTags,
	originalProviders,
	type CatalogArtist,
	type OriginalRelease,
	type SourceFiles,
} from "./catalogData";
import {
	comparisonControls,
	comparisonDifference,
	comparisonNote,
	comparisonRow,
	comparisonRowHeading,
	metadataPanel,
	objectActions,
	previewTable,
	previewTableCell,
	previewTableHeading,
	previewTableWrap,
} from "./matchingStyles";
import { navidromeFields, metadataFieldLabel } from "./metadata/registry";
import { useI18n } from "./i18n/I18nProvider";
import {
	formatTrackMessage,
	trackMessage,
	type TrackMessage,
} from "./trackMessages";

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
				providerTags?.[key] ?? null,
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
						<Label>{trackMessage(locale, "sourceFile")}</Label>
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
			<div className={previewTableWrap}>
				<table className={`${previewTable} min-w-[720px]`}>
					<thead>
						<tr>
							{[
								trackMessage(locale, "field"),
								trackMessage(locale, "sourceFile"),
								trackMessage(locale, "providerCache"),
								trackMessage(locale, "localLibrary"),
								trackMessage(locale, "currentPublicationColumn"),
							].map((heading) => (
								<th className={previewTableHeading} scope="col" key={heading}>
									{heading}
								</th>
							))}
						</tr>
					</thead>
					<tbody>
						{rows.map(({ key, label, values, different }) => (
							<tr className={comparisonRow} key={key}>
								<th
									scope="row"
									className={`${comparisonRowHeading} ${different ? comparisonDifference : ""}`}
								>
									{label} · {key}
								</th>
								{values.map((value, index) => (
									<td className={previewTableCell} key={`${key}:${index}`}>
										{value === null ? (
											<span className="block text-[11px] text-[var(--muted)]">
												{index === 1
													? trackMessage(locale, "noProviderLink")
													: trackMessage(locale, "notPublished")}
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
				<Checkbox isSelected={differencesOnly} onChange={setDifferencesOnly}>
					<span className="checkbox" />
					{trackMessage(locale, "onlyDifferences")}
				</Checkbox>
				{onPreview && (
					<Button onPress={() => onPreview([track.id])}>
						{trackMessage(locale, "previewPublication")}
					</Button>
				)}
			</div>
		</section>
	);
}
