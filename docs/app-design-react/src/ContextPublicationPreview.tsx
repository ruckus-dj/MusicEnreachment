import { useState } from "react";
import { useI18n } from "./i18n";
import type { UiNotice } from "./uiNotice";
import { Button, Checkbox, Dialog, Heading, Modal } from "./ui";
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
import { originalArtistName } from "./catalogData";
import { dialogFooter, objectActions } from "./matchingStyles";

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
	const [confirming, setConfirming] = useState(false);
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
	const classified = rows.map((row) => {
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
		({ row, action, reason }) => {
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
			return {
				id: String(row.targetTrack.id),
				position: String(row.targetTrack.position),
				title: row.targetTrack.title,
				action: publicationMessage(
					locale,
					actionMessage[action as keyof typeof actionMessage],
				),
				reason: `${displayReason(locale, reason)}${row.downgrade && row.assignment?.included ? ` · ${publicationMessage(locale, "qualityDowngrade")}` : ""}`,
				source: row.file
					? `${row.file.name} · ${row.file.codec}`
					: selectedSourceId(row.assignment)
						? publicationMessage(locale, "unavailableSource")
						: publicationMessage(locale, "noSource"),
				outputPath:
					row.previous?.path ??
					output?.path ??
					publicationMessage(locale, "pathUnavailable"),
				container:
					row.previous?.codec ??
					output?.container ??
					publicationMessage(locale, "noFormat"),
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
		setConfirming(false);
	};

	return (
		<section
			className="min-w-0 px-[clamp(14px,2vw,28px)] py-5"
			aria-labelledby="context-publication-title"
		>
			<header className="demo-object-header mb-3 flex flex-wrap items-center justify-between gap-3">
				<div className="min-w-0 [overflow-wrap:anywhere]">
					<h1 id="context-publication-title">
						{formatPublicationMessage(locale, "contextTitle", {
							title: plan.target.title,
						})}
					</h1>
					<p className="mt-[5px] max-w-[760px] text-[var(--muted)]">
						{publicationMessage(locale, "contextDescription")}
					</p>
				</div>
				<Button onPress={onBack}>
					{publicationMessage(locale, "returnToObject")}
				</Button>
			</header>
			{(stale || error) && (
				<p role="alert">
					{error || publicationMessage(locale, "inventoryChanged")}
				</p>
			)}
			<p
				className="demo-preview-counts flex flex-wrap gap-2 py-3"
				aria-label={publicationMessage(locale, "actionSummary")}
			>
				{counts.map(({ action, count }) => (
					<span
						key={action}
						className="rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-1 text-[11px]"
					>
						{publicationMessage(
							locale,
							actionMessage[action as keyof typeof actionMessage],
						)}
						: {count}
					</span>
				))}
				<span className="rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-1 text-[11px]">
					{publicationMessage(locale, "formatLossless")}
				</span>
			</p>
			<PublicationInspector items={inspections} locale={locale} />
			<p>
				<strong>{publicationMessage(locale, "sourcesPreserved")}</strong>{" "}
				{publicationMessage(locale, "noDelete")}
			</p>
			{downgrades.length > 0 && (
				<Checkbox
					isSelected={confirmDowngrade}
					onChange={(checked) => onPlan(plan, checked)}
				>
					<span className="checkbox" />
					{formatPublicationMessage(locale, "confirmDowngrade", {
						count: downgrades.length,
					})}
				</Checkbox>
			)}
			<div className={objectActions}>
				<Button
					className="border-[var(--button)]! bg-[var(--button)]! font-semibold text-[var(--button-text)]! hover:brightness-90"
					isDisabled={!canConfirm}
					onPress={() => setConfirming(true)}
				>
					{publicationMessage(locale, "confirmPublication")}
				</Button>
				<Button onPress={onBack}>{publicationMessage(locale, "cancel")}</Button>
			</div>
			<Modal isOpen={confirming} onOpenChange={setConfirming}>
				<Dialog
					aria-labelledby="context-publication-confirm-title"
					className="grid gap-2"
				>
					{({ close }) => (
						<div className="grid gap-2">
							<Heading slot="title" id="context-publication-confirm-title">
								{changed.length
									? formatPublicationMessage(locale, "writeSelectedCount", {
											count: changed.length,
										})
									: formatPublicationMessage(locale, "saveLinksCount", {
											count: links.length,
										})}
							</Heading>
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
							{(stale || error) && (
								<p role="alert">
									{error || publicationMessage(locale, "inventoryChanged")}
								</p>
							)}
							<div className={dialogFooter}>
								<Button
									className="border-[var(--button)]! bg-[var(--button)]! font-semibold text-[var(--button-text)]! hover:brightness-90"
									isDisabled={!canConfirm}
									onPress={confirm}
								>
									{publicationMessage(locale, "confirm")}
								</Button>
								<Button onPress={close}>
									{publicationMessage(locale, "cancel")}
								</Button>
							</div>
						</div>
					)}
				</Dialog>
			</Modal>
		</section>
	);
}
