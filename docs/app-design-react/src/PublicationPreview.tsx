import { useState } from "react";
import { useI18n } from "./i18n";
import { PublicationFlow } from "./PublicationFlow";
import { publicationArtistNotices } from "./publicationArtistNotices";
import { upgradeAudioOnly } from "./trackActionModel";
import {
	catalogData,
	effectivePublicationTags,
	originalFiles,
	originalReleases,
	originalArtistName,
	metadataChanged,
	publicationMetadataIssueMessages,
	type OriginalRelease,
	type SourceFiles,
	type CatalogArtist,
} from "./catalogData";
import {
	isMetadataPreviewFresh,
	type MetadataPreviewToken,
} from "./previewModel";
import {
	metadataSnapshotDiff,
	publicationMetadataDiff,
} from "./publicationMetadataDiff";
import type { PublicationInspection } from "./PublicationInspector";
import {
	formatPublicationMessage,
	publicationMessage,
} from "./publicationMessages";

import {
	choosePublicationOutput,
	fixturePublicationDestination,
} from "./publicationOutput";

type PreviewAction =
	| "Создать"
	| "Обновить"
	| "Без изменений"
	| "Невозможно обработать"
	| "Пропустить";

export type PublicationPreviewMode = "metadata" | "audioUpgrade";

const actionMessageKey: Record<
	PreviewAction,
	"create" | "update" | "unchangedAction" | "unprocessable" | "skip"
> = {
	Создать: "create",
	Обновить: "update",
	"Без изменений": "unchangedAction",
	"Невозможно обработать": "unprocessable",
	Пропустить: "skip",
};

function displayReason(locale: "en" | "ru", reason: string) {
	if (reason.startsWith("Некорректные метаданные: "))
		return formatPublicationMessage(locale, "invalidMetadata", {
			fields: reason.slice("Некорректные метаданные: ".length),
		});
	const keys: Record<
		string,
		| "selectedSourceCreate"
		| "metadataOnlyUpdate"
		| "metadataSame"
		| "noFileChosen"
		| "fileUnavailable"
	> = {
		"Создание из выбранного доступного источника": "selectedSourceCreate",
		"Обновятся эффективные метаданные; опубликованный звук сохраняется":
			"metadataOnlyUpdate",
		"Эффективные метаданные совпадают со снимком публикации": "metadataSame",
		"Файл не выбран; другой источник не выбирается": "noFileChosen",
		"Выбранный файл недоступен для этой операции; другой источник не выбирается":
			"fileUnavailable",
	};
	const key = keys[reason];
	return key ? publicationMessage(locale, key) : reason;
}

export function PublicationPreview({
	mode = "metadata",
	destination = fixturePublicationDestination,
	token,
	inventoryVersion,
	scopeKey,
	onBack,
	releases = originalReleases,
	onApply,
	files = originalFiles,
	artists,
	returnLabel,
}: {
	mode?: PublicationPreviewMode;
	destination?: import("./publicationOutput").PublicationDestination;
	token: MetadataPreviewToken;
	inventoryVersion: number;
	scopeKey: string;
	onBack: () => void;
	releases?: OriginalRelease[];
	onApply?: (token: MetadataPreviewToken) => boolean;
	files?: SourceFiles;
	artists?: CatalogArtist[];
	returnLabel?: string;
}) {
	const { locale } = useI18n();
	const [parentRejected, setParentRejected] = useState(false);
	const availableArtists = artists ?? catalogData.artists;
	const fresh = isMetadataPreviewFresh(token, inventoryVersion, scopeKey);
	const stale = !fresh || parentRejected;
	const rows = releases.flatMap((release) =>
		release.tracks
			.filter((track) => token.selectedIds.includes(track.id))
			.map((track) => {
				const file = track.sourceId ? files[track.sourceId] : null;
				const usable = file?.available && file.locations?.length ? file : null;
				const metadataIssues =
					mode === "metadata"
						? publicationMetadataIssueMessages(release, track, locale)
						: [];
				const artistNotices =
					mode === "metadata"
						? publicationArtistNotices(release, track, availableArtists, locale)
						: [];
				const action: PreviewAction =
					mode === "audioUpgrade"
						? track.published?.codec === "MP3" && usable?.codec === "FLAC"
							? "Обновить"
							: "Пропустить"
						: metadataIssues.length
							? "Невозможно обработать"
							: track.published
								? metadataChanged(release, track, artists)
									? "Обновить"
									: "Без изменений"
								: usable
									? "Создать"
									: "Пропустить";
				const reason =
					mode === "audioUpgrade"
						? action === "Обновить"
							? `${publicationMessage(locale, "audioUpgradeDescription")} (${track.published!.codec} → ${usable!.codec})`
							: publicationMessage(locale, "audioUpgradeUnavailable")
						: action === "Невозможно обработать"
							? formatPublicationMessage(locale, "invalidMetadata", {
									fields: metadataIssues.join("; "),
								})
							: action === "Создать"
								? "Создание из выбранного доступного источника"
								: action === "Обновить"
									? "Обновятся эффективные метаданные; опубликованный звук сохраняется"
									: action === "Без изменений"
										? "Эффективные метаданные совпадают со снимком публикации"
										: !track.sourceId
											? "Файл не выбран; другой источник не выбирается"
											: "Выбранный файл недоступен для этой операции; другой источник не выбирается";
				return {
					release,
					track,
					file: usable,
					action,
					reason,
					artistNotices,
				};
			}),
	);
	const actions = [
		"Создать",
		"Обновить",
		"Без изменений",
		"Невозможно обработать",
		"Пропустить",
	] as const;
	const returnText =
		returnLabel ?? publicationMessage(locale, "returnToLibrary");
	const inspections: PublicationInspection[] = rows.map(
		({ release, track, file, action, reason, artistNotices }) => {
			const output = track.published
				? { path: track.published.path, container: track.published.container }
				: file
					? choosePublicationOutput(file, {
							...destination,
							artist: originalArtistName(release.artistId, availableArtists),
							year: release.year,
							release: release.title,
							position: track.position,
							title: track.title,
						})
					: null;
			return {
				id: track.id,
				context: `${release.title} · ${release.year} · ${release.catalog || release.country}`,
				position: String(track.position),
				title: track.title,
				action: publicationMessage(locale, actionMessageKey[action]),
				reason: [displayReason(locale, reason), artistNotices.join(" · ")]
					.filter(Boolean)
					.join(" · "),
				source: file
					? `${file.path} · ${file.codec}`
					: track.sourceId
						? publicationMessage(locale, "unavailableSource")
						: publicationMessage(locale, "noSource"),
				outputPath:
					output?.path ?? publicationMessage(locale, "pathUnavailable"),
				container: output?.container ?? publicationMessage(locale, "noFormat"),
				groups: track.published
					? mode === "audioUpgrade"
						? metadataSnapshotDiff(
								track.published.tags,
								upgradeAudioOnly(track, files).published?.tags ??
									track.published.tags,
								{
									coverHash: track.published.coverHash,
									lyrics: track.published.lyrics,
								},
								{
									coverHash: track.published.coverHash,
									lyrics: track.published.lyrics,
								},
							)
						: publicationMetadataDiff(release, track, availableArtists)
					: metadataSnapshotDiff(
							{},
							effectivePublicationTags(release, track, availableArtists),
							{ coverHash: null, lyrics: null },
							{
								coverHash: track.coverHash ?? null,
								lyrics: track.lyrics ?? null,
							},
						),
			};
		},
	);

	const applicableRows = rows.filter(
		(row) => row.action === "Создать" || row.action === "Обновить",
	);
	const apply = () => {
		if (!isMetadataPreviewFresh(token, inventoryVersion, scopeKey)) return;
		const applicableToken = {
			...token,
			selectedIds: applicableRows.map((row) => row.track.id),
		};
		if (onApply && !onApply(applicableToken)) setParentRejected(true);
	};
	return (
		<PublicationFlow
			title={publicationMessage(locale, "previewTitle")}
			description={publicationMessage(
				locale,
				mode === "audioUpgrade" ? "audioUpgradeDescription" : "previewSummary",
			)}
			returnLabel={returnText}
			onBack={onBack}
			stale={stale}
			counts={actions.map((action) => ({
				label: publicationMessage(locale, actionMessageKey[action]),
				count: rows.filter((row) => row.action === action).length,
			}))}
			items={inspections}
			footer={
				<>
					<strong>
						{
							rows.filter((row) => row.action === "Невозможно обработать")
								.length
						}{" "}
						{publicationMessage(locale, "unprocessed")}
					</strong>{" "}
					{publicationMessage(
						locale,
						mode === "audioUpgrade" ? "unselectedUnchanged" : "previewPolicy",
					)}
				</>
			}
			primaryLabel={formatPublicationMessage(locale, "applyCount", {
				create: rows.filter((row) => row.action === "Создать").length,
				update: rows.filter((row) => row.action === "Обновить").length,
			})}
			primaryDisabled={!onApply || applicableRows.length === 0}
			confirmationTitle={formatPublicationMessage(locale, "applyTitle", {
				count: applicableRows.length,
			})}
			confirmationDetails={
				<>
					<p>
						{mode === "audioUpgrade" ? (
							publicationMessage(locale, "audioUpgradeDescription")
						) : (
							<>
								{publicationMessage(locale, "newSourceConfirmation")}{" "}
								{publicationMessage(locale, "existingMetadataConfirmation")}
							</>
						)}
					</p>
					<p>{publicationMessage(locale, "prototypeMemoryOnly")}</p>
				</>
			}
			confirmLabel={publicationMessage(locale, "confirm")}
			onConfirm={apply}
		/>
	);
}
