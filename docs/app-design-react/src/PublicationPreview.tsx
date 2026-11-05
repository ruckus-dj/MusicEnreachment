import { useState } from "react";
import { useI18n } from "./i18n";
import { Button, Dialog, Heading, Modal } from "./ui";
import { dialogFooter } from "./matchingStyles";
import {
	catalogData,
	effectivePublicationTags,
	originalFiles,
	originalReleases,
	originalArtistName,
	metadataChanged,
	publicationMetadataIssueMessages,
	publicationSortTagCollisions,
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
import { PublicationInspector } from "./PublicationInspector";
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
	destination = fixturePublicationDestination,
	token,
	inventoryVersion,
	scopeKey,
	onBack,
	releases = originalReleases,
	onApply,
	files = originalFiles,
	artists,
	returnRoute = "library",
	returnLabel,
}: {
	destination?: import("./publicationOutput").PublicationDestination;
	token: MetadataPreviewToken;
	inventoryVersion: number;
	scopeKey: string;
	onBack: () => void;
	releases?: OriginalRelease[];
	onApply?: (token: MetadataPreviewToken) => boolean;
	files?: SourceFiles;
	artists?: CatalogArtist[];
	returnRoute?: string;
	returnLabel?: string;
}) {
	const { locale } = useI18n();
	const [confirming, setConfirming] = useState(false);
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
				const metadataIssues = publicationMetadataIssueMessages(
					release,
					track,
					locale,
				);
				const sortCollisions = publicationSortTagCollisions(
					release,
					track,
					availableArtists,
				);
				const sortCollisionNotice = [
					...(sortCollisions.recording
						? [publicationMessage(locale, "recordingSortCollision")]
						: []),
					...(sortCollisions.release
						? [publicationMessage(locale, "releaseSortCollision")]
						: []),
				].join(" · ");
				const action: PreviewAction = metadataIssues.length
					? "Невозможно обработать"
					: track.published
						? metadataChanged(release, track, artists)
							? "Обновить"
							: "Без изменений"
						: usable
							? "Создать"
							: "Пропустить";
				const reason =
					action === "Невозможно обработать"
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
					sortCollisionNotice,
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
	const returnLink = `#${returnRoute}`;
	const returnText =
		returnLabel ?? publicationMessage(locale, "returnToLibrary");
	const inspections: PublicationInspection[] = rows.map(
		({ release, track, file, action, reason, sortCollisionNotice }) => {
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
				reason: [displayReason(locale, reason), sortCollisionNotice]
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
					? publicationMetadataDiff(release, track, availableArtists)
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
	return (
		<section
			className="min-w-0 px-[clamp(14px,2vw,28px)] py-5"
			aria-labelledby="preview-title"
		>
			<header className="mb-3.5 flex flex-wrap items-start justify-between gap-3">
				<div>
					<h1 id="preview-title" className="m-0 text-2xl tracking-[-.02em]">
						{publicationMessage(locale, "previewTitle")}
					</h1>
					<p className="mt-[5px] max-w-[760px] text-[var(--muted)]">
						{publicationMessage(locale, "previewSummary")}
					</p>
				</div>
				<p className="mt-[5px] max-w-[760px] text-[var(--muted)]">
					{publicationMessage(locale, "previewContext")}: {returnText}.{" "}
					<a href={returnLink}>{returnText}</a>
				</p>
				<Button onPress={onBack}>{returnText}</Button>
			</header>
			{stale && (
				<p role="alert">{publicationMessage(locale, "stalePreview")}</p>
			)}
			<div
				className="my-2.5 mb-3.5 flex flex-wrap gap-2"
				aria-label={publicationMessage(locale, "actionSummary")}
			>
				{actions.map((action) => (
					<span
						key={action}
						className="rounded border border-[var(--line)] bg-[var(--surface)] px-[9px] py-[5px] text-[11px]"
					>
						{publicationMessage(locale, actionMessageKey[action])}:{" "}
						{rows.filter((row) => row.action === action).length}
					</span>
				))}
			</div>
			<PublicationInspector items={inspections} locale={locale} />
			<div className="mt-3.5 flex flex-wrap items-center justify-between gap-2.5 rounded-[5px] border border-[var(--line)] bg-[var(--raised)] p-3 max-[600px]:items-stretch">
				<p>
					<strong>
						{
							rows.filter((row) => row.action === "Невозможно обработать")
								.length
						}{" "}
						{publicationMessage(locale, "unprocessed")}
					</strong>{" "}
					{publicationMessage(locale, "previewPolicy")}
				</p>
				<div className="flex flex-wrap justify-end gap-2">
					{onApply && (
						<Button
							className="border-[var(--button)]! bg-[var(--button)]! font-semibold text-[var(--button-text)]! hover:brightness-90"
							onPress={() => setConfirming(true)}
							isDisabled={
								stale ||
								!rows.some(
									(row) =>
										row.action === "Создать" || row.action === "Обновить",
								)
							}
						>
							{formatPublicationMessage(locale, "applyCount", {
								create: rows.filter((row) => row.action === "Создать").length,
								update: rows.filter((row) => row.action === "Обновить").length,
							})}
						</Button>
					)}
				</div>
			</div>
			<Modal isOpen={confirming} onOpenChange={setConfirming} isDismissable>
				<Dialog aria-labelledby="apply-preview-title" className="grid gap-2">
					{stale && (
						<p role="alert">{publicationMessage(locale, "stalePreview")}</p>
					)}
					<Heading slot="title" id="apply-preview-title">
						{formatPublicationMessage(locale, "applyTitle", {
							count: rows.filter(
								(row) => row.action === "Создать" || row.action === "Обновить",
							).length,
						})}
					</Heading>
					<p className="my-0 min-w-[240px] flex-1 text-[11px] text-[var(--muted)]">
						{publicationMessage(locale, "newSourceConfirmation")}{" "}
						{publicationMessage(locale, "existingMetadataConfirmation")}{" "}
						{publicationMessage(locale, "prototypeMemoryOnly")}
					</p>
					<div className={`${dialogFooter} flex-wrap`}>
						<Button onPress={() => setConfirming(false)}>
							{publicationMessage(locale, "cancel")}
						</Button>
						<Button
							className="border-[var(--button)]! bg-[var(--button)]! font-semibold text-[var(--button-text)]! hover:brightness-90"
							isDisabled={stale}
							onPress={() => {
								if (
									!isMetadataPreviewFresh(token, inventoryVersion, scopeKey)
								) {
									setConfirming(false);
									return;
								}
								const applicableToken = {
									...token,
									selectedIds: rows
										.filter(
											(row) =>
												row.action === "Создать" || row.action === "Обновить",
										)
										.map((row) => row.track.id),
								};
								if (onApply && !onApply(applicableToken)) {
									setParentRejected(true);
									setConfirming(false);
									return;
								}
								setConfirming(false);
							}}
						>
							{publicationMessage(locale, "confirm")}
						</Button>
					</div>
				</Dialog>
			</Modal>
		</section>
	);
}
