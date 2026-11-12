import { useState } from "react";
import {
	type CatalogArtist,
	desiredTags,
	type OriginalRelease,
	originalProviders,
	type SourceFiles,
	sourceOnlyAudioTagKeys,
} from "./catalogData";
import { type CatalogueMessage, catalogueMessage } from "./catalogueMessages";
import { useI18n } from "./i18n/I18nProvider";
import {
	comparisonControls,
	comparisonDifference,
	comparisonFilter,
	comparisonNote,
	comparisonRow,
	comparisonRowHeading,
	metadataPanel,
	metadataPanelHeading,
	previewTable,
	previewTableCell,
	previewTableHeading,
	previewTableWrap,
} from "./matchingStyles";
import { navidromeFields } from "./metadata/registry";
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

type Scope = "artist" | "release" | "track";
const artistGroups = new Set(["recording artists", "release artists"]);
const fieldsForScope = (scope: Scope) =>
	navidromeFields.filter((field) => {
		if (scope === "artist") return artistGroups.has(field.group);
		if (scope === "release") return field.scope === "release";
		return (
			field.scope === "track" ||
			field.scope === "audio" ||
			(field.scope === "recording" && !artistGroups.has(field.group))
		);
	});

export function ReleaseMetadataComparison({
	release,
	artists,
	files,
	showAllFields = false,
}: {
	release: OriginalRelease;
	artists?: CatalogArtist[];
	files: SourceFiles;
	showAllFields?: boolean;
}) {
	const { locale } = useI18n();
	const m = (key: CatalogueMessage, values?: Record<string, string | number>) =>
		catalogueMessage(locale, key, values);
	const [trackId, setTrackId] = useState(release.tracks[0]?.id ?? "");
	const [scope, setScope] = useState<Scope>("release");
	const [differencesOnly, setDifferencesOnly] = useState(false);
	const [allFields, setAllFields] = useState(false);
	const [fileOverride, setFileOverride] = useState<string | null | undefined>(
		undefined,
	);
	const track =
		release.tracks.find((item) => item.id === trackId) ?? release.tracks[0];
	if (!track) return null;
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
	const providerTags = provider
		? desiredTags(provider.release, provider.track, artists)
		: null;
	const localTags = desiredTags(release, track, artists);
	const publicationTags = track.published?.tags ?? {};
	const rows = (allFields ? navidromeFields : fieldsForScope(scope))
		.map((field) => {
			const key = field.tag;
			const values = [
				sourceTags[key] ?? [],
				providerTags?.[key] ?? null,
				localTags[key] ?? [],
				track.published ? (publicationTags[key] ?? []) : null,
			];
			const distinct = new Set(
				values.map((value) => JSON.stringify(value ?? [])),
			);
			return { key, label: field.label, values, different: distinct.size > 1 };
		})
		.concat(
			(scope === "track" || allFields
				? sourceOnlyAudioTagKeys.filter((key) => Object.hasOwn(sourceTags, key))
				: []
			).map((key) => {
				const values = [
					sourceTags[key] ?? [],
					providerTags?.[key] ?? null,
					localTags[key] ?? [],
					track.published ? (publicationTags[key] ?? []) : null,
				];
				const distinct = new Set(
					values.map((value) => JSON.stringify(value ?? [])),
				);
				return { key, label: key, values, different: distinct.size > 1 };
			}),
		)
		.filter((row) => !differencesOnly || row.different);
	const scopes: [Scope, CatalogueMessage][] = [
		["artist", "artistScope"],
		["release", "releaseScope"],
		["track", "trackScope"],
	];
	return (
		<section className={metadataPanel} aria-label={m("metadataComparison")}>
			<h2 className={metadataPanelHeading}>{m("metadataComparison")}</h2>
			{release.manual && (
				<p>
					{m("typeLabel")}: {release.metadata?.type || "—"} ·{" "}
					{m("artistsLabel")}:{" "}
					{release.credits
						?.map((credit) => credit.credit || credit.name)
						.filter(Boolean)
						.join(", ") || m("notSpecified")}
				</p>
			)}
			<div className={comparisonControls}>
				<Select
					aria-label={m("comparisonPosition")}
					selectedKey={track.id}
					onSelectionChange={(key) => {
						setTrackId(String(key));
						setFileOverride(undefined);
					}}
				>
					<Label>{m("position")}</Label>
					<Button>
						<SelectValue />
						<span aria-hidden="true">⌄</span>
					</Button>
					<Popover>
						<ListBox>
							{release.tracks.map((item) => (
								<ListBoxItem key={item.id} id={item.id}>
									{item.title}
								</ListBoxItem>
							))}
						</ListBox>
					</Popover>
				</Select>
				<Select
					aria-label={m("sourceFileComparison")}
					selectedKey={fileId ?? ""}
					onSelectionChange={(key) => setFileOverride(String(key) || null)}
				>
					<Label>{m("sourceFile")}</Label>
					<Button>
						<SelectValue />
						<span aria-hidden="true">⌄</span>
					</Button>
					<Popover>
						<ListBox>
							<ListBoxItem id="">{m("noSourceSelected")}</ListBoxItem>
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
			<div className={comparisonFilter} aria-label={m("tagScope")}>
				<Tabs
					selectedKey={scope}
					onSelectionChange={(key) => setScope(key as Scope)}
				>
					<TabList aria-label={m("tagScope")}>
						{scopes.map(([id, label]) => (
							<Tab key={id} id={id}>
								{m(label)}
							</Tab>
						))}
					</TabList>
				</Tabs>
				<Checkbox isSelected={differencesOnly} onChange={setDifferencesOnly}>
					<span className="checkbox" />
					{m("differencesOnly")}
				</Checkbox>
				{showAllFields && (
					<Checkbox isSelected={allFields} onChange={setAllFields}>
						<span className="checkbox" />
						{m("allFields")}
					</Checkbox>
				)}
			</div>
			<div className={previewTableWrap}>
				<table className={`${previewTable} min-w-[720px]`}>
					<thead>
						<tr>
							{[
								"columnField",
								"sourceFile",
								"columnProviderCache",
								"columnLocalLibrary",
								"columnCurrentlyPublished",
							].map((heading) => (
								<th className={previewTableHeading} scope="col" key={heading}>
									{m(heading as CatalogueMessage)}
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
													? m("noProviderLink")
													: m("publicationMissing")}
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
				{file?.path ?? m("noSourceChosen")}
				<br />
				{track.published
					? m("publicationPathRead", {
							path: track.published.path,
							readAt: track.published.readAt,
						})
					: m("noPublicationColumn")}
			</p>
			<a href={`#tags/${track.id}/release`}>
				{m("openDetailedReleaseComparison")}
			</a>
		</section>
	);
}
