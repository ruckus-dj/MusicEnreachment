import type { Locale } from "../../i18n/types";
import type {
	AggregateDisposition,
	AggregateInspectorPresentation,
	AggregatePositionView,
} from "../library/AggregatePublicationPreviewView";
import type {
	IncomingFiles,
	IncomingReleases,
	MatchingLinkReceipt,
} from "../incoming/incomingModel";
import type { CatalogArtist } from "../library/catalogData";
import { individualPublicationDetails } from "../publication/individualPublicationDetails";
import type { PublicationDestination } from "../publication/publicationOutput";
import {
	MatchingReceiptPublicationFrame,
	matchingReceiptFileChoiceLabels,
} from "./MatchingReceiptPublicationFrame";
import type {
	MatchingReceiptPublicationNotice,
	MatchingReceiptPublicationRow,
} from "./matchingReceiptPublication";
import {
	type MatchingReceiptPublicationOutcome,
	useMatchingReceiptPublication,
} from "./matchingReceiptPublicationController";

const copy = {
	en: {
		excluded: "Position excluded from this publication.",
		outcomeHeading: "Publication results",
		outcomeRequested: "Positions to publish: {count}.",
		outcomePublished: "Published positions: {count}.",
		outcomeFailed: "Not published: {count}.",
		outcomeNothing:
			"No position required publication; current publications were not changed.",
		notices: {
			"matchingReceipt.releaseMissing":
				"The release for this saved result was not found.",
			"matchingReceipt.receiptEmpty": "This saved result has no positions.",
			"matchingReceipt.requestedPositionMissing":
				"The requested position is not in this saved result.",
			"matchingReceipt.duplicatePosition":
				"The same position appears more than once.",
			"matchingReceipt.positionMissing":
				"The saved position is no longer in the release.",
			"matchingReceipt.positionNotIncluded":
				"Position excluded from this publication.",
			"matchingReceipt.sourceNotChosen":
				"Choose one linked file for this position.",
			"matchingReceipt.sourceOutsideReceipt":
				"The chosen file is not part of this saved result.",
			"matchingReceipt.sourceUnlinked":
				"The chosen file is not linked to this position.",
			"matchingReceipt.sourceMissing":
				"The chosen file is no longer available.",
			"matchingReceipt.sourceUnavailable": "The chosen file is unavailable.",
			"matchingReceipt.formatUnsupported":
				"This file format cannot be published in the current destination.",
			"matchingReceipt.currentPathMissing":
				"The current publication has no recorded path; it cannot be replaced here.",
			"matchingReceipt.outputPathMissing":
				"The planned output path is not defined.",
			"matchingReceipt.willCreate": "A new publication will be created.",
			"matchingReceipt.willReplace":
				"The current publication will be replaced with better-quality audio.",
			"matchingReceipt.alreadyCurrent":
				"The current publication already uses this file.",
			"matchingReceipt.sameSourceDigest":
				"The chosen file has the same audio digest as the current publication.",
			"matchingReceipt.blocked": "This position cannot be published.",
			"matchingReceipt.qualityNotBetter":
				"The chosen file is not strictly better than the current publication.",
			"matchingReceipt.qualityUnavailable":
				"Audio quality cannot be compared, so the current publication is unchanged.",
			"matchingReceipt.transcodeNotProven":
				"Lossy transcoding is not proof of better audio.",
			"matchingReceipt.apply.previewMismatch":
				"The preview does not belong to this saved result.",
			"matchingReceipt.apply.receiptMissing":
				"The saved matching result no longer exists.",
			"matchingReceipt.apply.receiptChanged":
				"The saved matching result changed after the preview.",
			"matchingReceipt.apply.stateChanged":
				"The preview is stale; prepare it again.",
			"matchingReceipt.apply.blocked": "The position could not be published.",
		} satisfies Record<MatchingReceiptPublicationNotice, string>,
	},
	ru: {
		excluded: "Позиция исключена из этой публикации.",
		outcomeHeading: "Результаты публикации",
		outcomeRequested: "Позиций к публикации: {count}.",
		outcomePublished: "Опубликовано позиций: {count}.",
		outcomeFailed: "Не опубликовано: {count}.",
		outcomeNothing:
			"Ни одной позиции не требовалась публикация; действующие публикации не изменены.",
		notices: {
			"matchingReceipt.releaseMissing":
				"Издание для этого сохранённого результата не найдено.",
			"matchingReceipt.receiptEmpty":
				"В этом сохранённом результате нет позиций.",
			"matchingReceipt.requestedPositionMissing":
				"Запрошенной позиции нет в этом сохранённом результате.",
			"matchingReceipt.duplicatePosition":
				"Одна и та же позиция указана несколько раз.",
			"matchingReceipt.positionMissing":
				"Сохранённой позиции больше нет в издании.",
			"matchingReceipt.positionNotIncluded":
				"Позиция исключена из этой публикации.",
			"matchingReceipt.sourceNotChosen":
				"Выберите один связанный файл для этой позиции.",
			"matchingReceipt.sourceOutsideReceipt":
				"Выбранный файл не входит в этот сохранённый результат.",
			"matchingReceipt.sourceUnlinked":
				"Выбранный файл не связан с этой позицией.",
			"matchingReceipt.sourceMissing": "Выбранный файл больше недоступен.",
			"matchingReceipt.sourceUnavailable": "Выбранный файл недоступен.",
			"matchingReceipt.formatUnsupported":
				"Этот формат файла нельзя опубликовать в текущем месте назначения.",
			"matchingReceipt.currentPathMissing":
				"У действующей публикации нет записанного пути; заменить её здесь нельзя.",
			"matchingReceipt.outputPathMissing":
				"Планируемый путь публикации не определён.",
			"matchingReceipt.willCreate": "Будет создана новая публикация.",
			"matchingReceipt.willReplace":
				"Действующая публикация будет заменена аудио более высокого качества.",
			"matchingReceipt.alreadyCurrent":
				"Действующая публикация уже использует этот файл.",
			"matchingReceipt.sameSourceDigest":
				"Выбранный файл совпадает с действующей публикацией по аудиодайджесту.",
			"matchingReceipt.blocked": "Эту позицию нельзя опубликовать.",
			"matchingReceipt.qualityNotBetter":
				"Выбранный файл не строго лучше действующей публикации.",
			"matchingReceipt.qualityUnavailable":
				"Качество аудио нельзя сравнить, поэтому действующая публикация не изменяется.",
			"matchingReceipt.transcodeNotProven":
				"Перекодирование с потерями не доказывает улучшение аудио.",
			"matchingReceipt.apply.previewMismatch":
				"Предпросмотр относится к другому сохранённому результату.",
			"matchingReceipt.apply.receiptMissing":
				"Сохранённый результат сопоставления больше не существует.",
			"matchingReceipt.apply.receiptChanged":
				"Сохранённый результат сопоставления изменился после предпросмотра.",
			"matchingReceipt.apply.stateChanged":
				"Предпросмотр устарел; подготовьте его заново.",
			"matchingReceipt.apply.blocked": "Позицию не удалось опубликовать.",
		} satisfies Record<MatchingReceiptPublicationNotice, string>,
	},
} as const;

type ReceiptPublicationText = (typeof copy)[Locale];

function dispositionFor(
	row: MatchingReceiptPublicationRow,
	included: boolean,
): AggregateDisposition {
	if (!included) return "skipped";
	if (row.blockers.includes("matchingReceipt.positionNotIncluded"))
		return "skipped";
	if (row.action === "create") return "create";
	if (row.action === "replace") return "update";
	if (row.action === "unchanged") return "unchanged";
	if (row.blockers.includes("matchingReceipt.formatUnsupported"))
		return "unprocessable";
	return "blocked";
}

function noticeText(
	text: ReceiptPublicationText,
	notice: MatchingReceiptPublicationNotice,
) {
	return text.notices[notice];
}

function rowLabel(row: MatchingReceiptPublicationRow) {
	return `${row.displayedNumber ?? row.position} · ${row.title}`;
}

function rowDiagnostic(
	row: MatchingReceiptPublicationRow,
	included: boolean,
	text: ReceiptPublicationText,
) {
	if (!included) return text.excluded;
	if (row.action === "blocked")
		return noticeText(text, row.blockers[0] ?? row.reason);
	return noticeText(text, row.reason);
}

function detailsFor(
	row: MatchingReceiptPublicationRow,
	files: IncomingFiles,
	text: ReceiptPublicationText,
) {
	const file = row.sourceId ? files[row.sourceId] : undefined;
	if (row.target) {
		return individualPublicationDetails({
			current: row.current,
			tags: row.target.tags,
			path: row.target.path,
			renderedPath: row.target.path,
			source: file ? { codec: file.codec, path: file.path } : undefined,
			coverHash: row.target.coverHash,
			lyrics: row.target.lyrics,
		});
	}
	const current = row.current;
	const blocker = noticeText(text, row.blockers[0] ?? row.reason);
	return {
		error: "",
		loading: false,
		admittedOperationID: "",
		admissionUncertain: false,
		inspection: {
			workflow: current ? ("comparison" as const) : ("first" as const),
			sourceCandidates: [],
			selectedChoice: null,
			needsExplicitChoice: false,
			sourceChoiceDisabled: true,
			preview: {
				blockers: [blocker],
				current_comparison: current
					? {
							current_components: [current.path],
							rendered_components: [current.path],
							resolved_components: [current.path],
							audio_status: "",
							cover_status: "matches",
						}
					: undefined,
				desired_tags: current?.tags ?? {},
				resolved_path: current?.path,
				selected_source: file
					? { codec: file.codec, path: file.path }
					: undefined,
				showAudioComparisonBasis: false,
			},
		},
	};
}

export type MatchingReceiptPublicationViewProps = {
	locale: Locale;
	receipt: MatchingLinkReceipt;
	releases: IncomingReleases;
	files: IncomingFiles;
	artists: readonly CatalogArtist[];
	savedReceipts: readonly MatchingLinkReceipt[];
	destination?: PublicationDestination;
	onReleases: (releases: IncomingReleases) => void;
	onBack: () => void;
};

export function MatchingReceiptPublicationView({
	locale,
	receipt,
	releases,
	files,
	artists,
	savedReceipts,
	destination,
	onReleases,
	onBack,
}: MatchingReceiptPublicationViewProps) {
	const text = copy[locale];
	const controller = useMatchingReceiptPublication({
		receipt,
		releases,
		files,
		artists,
		savedReceipts,
		destination,
		onReleases,
	});
	const counts: Record<AggregateDisposition, number> = {
		create: 0,
		update: 0,
		unchanged: 0,
		skipped: 0,
		unprocessable: 0,
		blocked: 0,
	};
	const positions: AggregatePositionView[] = controller.rows.map((row) => {
		const isIncluded = controller.included.has(row.trackId);
		const disposition = dispositionFor(row, isIncluded);
		counts[disposition] += 1;
		return {
			id: row.trackId,
			label: rowLabel(row),
			status: { kind: "disposition", disposition },
			diagnostic: {
				kind: "text",
				text: rowDiagnostic(row, isIncluded, text),
			},
		};
	});
	const selectedReceiptPosition = receipt.positions.find(
		(position) => String(position.trackId) === controller.selectedPositionId,
	);
	const fileChoice =
		selectedReceiptPosition && selectedReceiptPosition.sourceIds.length > 1
			? {
					...matchingReceiptFileChoiceLabels(locale),
					options: selectedReceiptPosition.sourceIds.map((sourceId) => ({
						id: sourceId,
						label: files[sourceId]?.name ?? sourceId,
					})),
					selectedId: controller.sourceSelection(
						String(selectedReceiptPosition.trackId),
					),
					onSelect: (sourceId: string | null) =>
						controller.selectSource(
							String(selectedReceiptPosition.trackId),
							sourceId,
						),
				}
			: undefined;
	const inspector: AggregateInspectorPresentation = controller.selectedRow
		? {
				kind: "publication",
				details: detailsFor(controller.selectedRow, files, text),
				fileChoice,
				fileLabel:
					!fileChoice && controller.selectedRow.sourceName
						? {
								caption: matchingReceiptFileChoiceLabels(locale).legend,
								value: controller.selectedRow.sourceName,
							}
						: undefined,
				onRefresh: controller.refresh,
			}
		: { kind: "empty" };

	return (
		<MatchingReceiptPublicationFrame
			locale={locale}
			releaseTitle={receipt.releaseTitle}
			onBack={onBack}
			counts={counts}
			positions={positions}
			included={controller.included}
			selectedPositionId={controller.selectedPositionId}
			canPrepare={controller.included.size > 0}
			canConfirm={controller.canConfirm}
			preparing={false}
			inspector={inspector}
			onToggleInclude={controller.toggleInclude}
			onSelectPosition={controller.selectPosition}
			onPrepare={controller.refresh}
			onConfirm={controller.confirm}
		>
			{controller.outcome ? (
				<MatchingReceiptPublicationOutcomeView
					locale={locale}
					receipt={receipt}
					previewRows={controller.rows}
					outcome={controller.outcome}
				/>
			) : null}
		</MatchingReceiptPublicationFrame>
	);
}

function MatchingReceiptPublicationOutcomeView({
	locale,
	receipt,
	previewRows,
	outcome,
}: {
	locale: Locale;
	receipt: MatchingLinkReceipt;
	previewRows: readonly MatchingReceiptPublicationRow[];
	outcome: MatchingReceiptPublicationOutcome;
}) {
	const text = copy[locale];
	const titleFor = (trackId: string) => {
		const row = previewRows.find((item) => item.trackId === trackId);
		if (row) return rowLabel(row);
		const position = receipt.positions.find(
			(item) => String(item.trackId) === trackId,
		);
		return position
			? `${position.displayedNumber ?? position.position} · ${position.title}`
			: trackId;
	};
	return (
		<section
			role="status"
			aria-live="polite"
			className="rounded border border-[var(--line)] bg-[var(--raised)] p-3 text-sm"
		>
			<h2 className="m-0 text-sm font-semibold">{text.outcomeHeading}</h2>
			<p className="mt-1">
				{text.outcomeRequested.replace("{count}", String(outcome.attempted))}{" "}
				{text.outcomePublished.replace("{count}", String(outcome.published))}
				{outcome.failed.length
					? ` ${text.outcomeFailed.replace("{count}", String(outcome.failed.length))}`
					: ""}
			</p>
			{!outcome.attempted && <p>{text.outcomeNothing}</p>}
			{outcome.failed.length ? (
				<ul className="mt-1 list-disc pl-5">
					{outcome.failed.map((failure) => (
						<li key={failure.trackId} className="wrap-anywhere">
							{titleFor(failure.trackId)}: {noticeText(text, failure.notice)}
						</li>
					))}
				</ul>
			) : null}
		</section>
	);
}
