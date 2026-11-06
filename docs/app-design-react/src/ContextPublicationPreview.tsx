import { useState } from "react";
import { useI18n } from "./i18n";
import type { UiNotice } from "./uiNotice";
import { Button } from "./ui";
import {
	publicationMetadataIssueMessages,
	type CatalogArtist,
	type OriginalRelease,
} from "./catalogData";
import {
	applyPlan,
	type IncomingFiles,
	type IncomingPlan,
	type IncomingReleases,
	planRows,
	selectedSourceId,
	sourceIds,
	usableFile,
} from "./incomingModel";
import { metadataSnapshotDiff } from "./publicationMetadataDiff";
import type { PublicationInspection } from "./PublicationInspector";
import {
	formatPublicationMessage,
	publicationMessage,
} from "./publicationMessages";
import {
	choosePublicationOutput,
	fixturePublicationDestination,
} from "./publicationOutput";
import { originalArtistName } from "./catalogData";
import { publicationArtistNotices } from "./publicationArtistNotices";
import { PublicationFlow } from "./PublicationFlow";

const actionMessage = {
	Создать: "create",
	Обновить: "update",
	"Без изменений": "unchangedAction",
	"Невозможно обработать": "unprocessable",
	Пропустить: "skip",
} as const;

function displayReason(locale: "en" | "ru", reason: string) {
	if (reason.startsWith("Некорректные метаданные:"))
		return formatPublicationMessage(locale, "invalidMetadata", {
			fields: reason.slice("Некорректные метаданные: ".length),
		});
	const keys = {
		"Позиция исключена из плана": "positionExcluded",
		"Файл не выбран": "fileNotSelected",
		"Источник и эффективные теги совпадают с публикацией": "sourceAndTagsSame",
		"Будут обновлены выбранные метаданные и аудио": "selectedMetadataAndAudio",
		"Новая публикация из выбранного источника": "newPublicationSelectedSource",
	} as const;
	const key = keys[reason as keyof typeof keys];
	return key ? publicationMessage(locale, key) : reason;
}

export function ContextPublicationPreview({
	destination = fixturePublicationDestination,
	plan,
	files,
	releases,
	providers,
	artists,
	inventoryVersion,
	confirmDowngrade,
	onPlan,
	onReleases,
	onSuccess,
	onBack,
}: {
	destination?: import("./publicationOutput").PublicationDestination;
	plan: IncomingPlan | null;
	files: IncomingFiles;
	releases: IncomingReleases;
	providers: IncomingReleases;
	artists: CatalogArtist[];
	inventoryVersion: number;
	confirmDowngrade: boolean;
	onPlan: (plan: IncomingPlan | null, confirmDowngrade?: boolean) => void;
	onReleases: (releases: OriginalRelease[]) => void;
	onSuccess: (notice: UiNotice) => void;
	onBack: () => void;
}) {
	const { locale } = useI18n();
	const [error, setError] = useState("");
	if (!plan)
		return (
			<section>
				<h1>{publicationMessage(locale, "noPlanTitle")}</h1>
				<p>{publicationMessage(locale, "noSavedPlan")}</p>
				<a href="#library">{publicationMessage(locale, "toLibrary")}</a>
			</section>
		);

	const rows = planRows(plan, files, releases, providers, artists);
	const existing = releases.find(
		(release) => release.id === plan.localReleaseId,
	);
	const classified = rows
		.map((row) => {
			if (!row.assignment?.included)
				return {
					row,
					action: "Пропустить",
					reason: "Позиция исключена из плана",
				};
			const sourceId = selectedSourceId(row.assignment);
			const metadataRelease =
				plan.mode === "local" ? (existing ?? plan.target) : plan.target;
			const metadataTrack = row.track ?? row.targetTrack;
			const metadataIssues = publicationMetadataIssueMessages(
				metadataRelease,
				metadataTrack,
				locale,
				sourceId,
			);
			if (metadataIssues.length)
				return {
					row,
					action: "Невозможно обработать",
					reason: formatPublicationMessage(locale, "invalidMetadata", {
						fields: metadataIssues.join("; "),
					}),
				};
			if (!row.file) {
				const reason = !sourceId
					? "Файл не выбран"
					: publicationMessage(locale, "selectedFileUnavailable");
				return { row, action: "Пропустить", reason };
			}
			return {
				row,
				action:
					row.action === "Без изменений"
						? "Без изменений"
						: row.previous
							? "Обновить"
							: "Создать",
				reason:
					row.action === "Без изменений"
						? "Источник и эффективные теги совпадают с публикацией"
						: row.previous
							? "Будут обновлены выбранные метаданные и аудио"
							: "Новая публикация из выбранного источника",
			};
		})
		.map((item) => {
			const metadataRelease =
				plan.mode === "local" ? (existing ?? plan.target) : plan.target;
			const metadataTrack =
				plan.mode === "local"
					? (item.row.track ?? item.row.targetTrack)
					: item.row.targetTrack;
			const notices = publicationArtistNotices(
				metadataRelease,
				metadataTrack,
				artists,
				locale,
			);
			return { ...item, notices };
		});
	const changed = classified.filter(
		(item) => item.action === "Создать" || item.action === "Обновить",
	);
	const downgrades = classified.filter(
		(item) => item.row.downgrade && item.action === "Обновить",
	);
	const counts = [
		"Создать",
		"Обновить",
		"Без изменений",
		"Невозможно обработать",
		"Пропустить",
	].map((action) => ({
		action,
		count: classified.filter((item) => item.action === action).length,
	}));
	const stale = plan.inventoryVersion !== inventoryVersion;
	const links = rows.filter((row) => {
		const status = classified.find(
			(item) => item.row.targetTrack.id === row.targetTrack.id,
		)?.action;
		if (status === "Пропустить" || status === "Невозможно обработать")
			return false;
		const assigned = sourceIds(row.assignment).filter((id) =>
			usableFile(files[id]),
		);
		const track = existing?.tracks.find(
			(item) => item.position === row.targetTrack.position,
		);
		const persisted = new Set([
			...(track?.sourceIds ?? []),
			...(track?.sourceId ? [track.sourceId] : []),
		]);
		return assigned.some((id) => !persisted.has(id));
	});
	const canConfirm =
		!stale &&
		(changed.length > 0 || links.length > 0) &&
		(!downgrades.length || confirmDowngrade);
	const updateIncluded = (trackId: string, included: boolean) => {
		const assignment = plan.assignments[trackId];
		if (!assignment) return;
		onPlan({
			...plan,
			assignments: {
				...plan.assignments,
				[trackId]: { ...assignment, included },
			},
		});
	};
	const inspections: PublicationInspection[] = classified.map(
		({ row, action, reason, notices }) => {
			const output = row.file
				? choosePublicationOutput(row.file, {
						...destination,
						artist: originalArtistName(plan.target.artistId, artists),
						year: plan.target.year,
						release: plan.target.title,
						position: row.targetTrack.position,
						title: row.targetTrack.title,
					})
				: null;
			const rebuildSource = Boolean(
				row.file &&
					(!row.previous ||
						plan.force ||
						row.previous.sourceId !== row.file.id ||
						row.previous.codec !== row.file.codec),
			);
			return {
				id: String(row.targetTrack.id),
				position: String(row.targetTrack.position),
				title: row.targetTrack.title,
				action: publicationMessage(
					locale,
					actionMessage[action as keyof typeof actionMessage],
				),
				reason: [
					displayReason(locale, reason),
					...notices,
					...(row.downgrade && row.assignment?.included
						? [publicationMessage(locale, "qualityDowngrade")]
						: []),
				].join(" · "),
				source: row.file
					? `${row.file.name} · ${row.file.codec}`
					: selectedSourceId(row.assignment)
						? publicationMessage(locale, "unavailableSource")
						: publicationMessage(locale, "noSource"),
				outputPath: output
					? row.previous && rebuildSource && row.previous.path !== output.path
						? `${row.previous.path} → ${output.path}`
						: rebuildSource
							? output.path
							: (row.previous?.path ?? output.path)
					: publicationMessage(locale, "pathUnavailable"),
				container: output
					? row.previous &&
						rebuildSource &&
						row.previous.container !== output.container
						? `${row.previous.container} → ${output.container}`
						: rebuildSource
							? output.container
							: (row.previous?.container ?? output.container)
					: publicationMessage(locale, "noFormat"),
				included: Boolean(row.assignment?.included && row.file),
				includeDisabled: !row.file,
				includeLabel: formatPublicationMessage(locale, "includeTrack", {
					title: row.targetTrack.title,
				}),
				onInclude: (included) =>
					updateIncluded(String(row.targetTrack.id), included),
				groups: metadataSnapshotDiff(
					row.previous?.tags ?? {},
					row.nextTags,
					{
						coverHash: row.previous?.coverHash ?? null,
						lyrics: row.previous?.lyrics ?? null,
					},
					{
						coverHash: (row.track ?? row.targetTrack).coverHash ?? null,
						lyrics: (row.track ?? row.targetTrack).lyrics ?? null,
					},
				),
			};
		},
	);
	const confirm = () => {
		if (plan.inventoryVersion !== inventoryVersion) {
			setError(publicationMessage(locale, "inventoryChanged"));
			return;
		}
		if (!canConfirm) return;
		const next = structuredClone(releases);
		const applicableIDs = new Set(
			classified
				.filter(
					(item) =>
						item.action !== "Пропустить" &&
						item.action !== "Невозможно обработать",
				)
				.map((item) => item.row.targetTrack.id),
		);
		const applicablePlan: IncomingPlan = {
			...plan,
			assignments: Object.fromEntries(
				Object.entries(plan.assignments).filter(([trackId]) =>
					applicableIDs.has(trackId),
				),
			),
		};
		const applied = applyPlan(
			applicablePlan,
			files,
			next,
			providers,
			false,
			artists,
			destination,
		);
		onReleases(next);
		onPlan(null, false);
		onSuccess({
			namespace: "publication",
			id: "applySuccess",
			params: { count: applied, linkCount: links.length },
		});
	};

	return (
		<PublicationFlow
			title={formatPublicationMessage(locale, "contextTitle", {
				title: plan.target.title,
			})}
			description={publicationMessage(locale, "contextDescription")}
			returnLabel={publicationMessage(locale, "returnToObject")}
			onBack={onBack}
			stale={stale}
			error={error}
			counts={counts.map(({ action, count }) => ({
				label: publicationMessage(
					locale,
					actionMessage[action as keyof typeof actionMessage],
				),
				count,
			}))}
			items={inspections}
			footer={
				<>
					<strong>{publicationMessage(locale, "sourcesPreserved")}</strong>{" "}
					{publicationMessage(locale, "noDelete")}
				</>
			}
			primaryLabel={publicationMessage(locale, "confirmPublication")}
			primaryDisabled={!canConfirm}
			confirmationTitle={
				changed.length
					? formatPublicationMessage(locale, "writeSelectedCount", {
							count: changed.length,
						})
					: formatPublicationMessage(locale, "saveLinksCount", {
							count: links.length,
						})
			}
			confirmationDetails={
				<>
					<p>
						{formatPublicationMessage(locale, "confirmSelectedRows", {
							title: plan.target.title,
						})}
					</p>
					{links.length > 0 && (
						<p>
							{formatPublicationMessage(locale, "sourceLinksCount", {
								count: links.length,
							})}
						</p>
					)}
					{downgrades.length > 0 && (
						<p>
							{formatPublicationMessage(locale, "confirmQualityDown", {
								count: downgrades.length,
							})}
						</p>
					)}
					<p>{publicationMessage(locale, "unmatchedFiles")}</p>
				</>
			}
			confirmLabel={publicationMessage(locale, "confirm")}
			onConfirm={confirm}
			downgradeCount={downgrades.length}
			downgradeConfirmed={confirmDowngrade}
			onDowngradeConfirmed={(checked) => onPlan(plan, checked)}
		/>
	);
}
