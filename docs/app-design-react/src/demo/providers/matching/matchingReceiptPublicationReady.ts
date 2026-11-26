import type {
	AggregateDisposition,
	AggregateInspectorPresentation,
	AggregatePositionView,
} from "../../../ui/features/library/AggregatePublicationPreviewView";
import type { MatchingReceiptPublicationModel } from "../../../ui/features/matching/MatchingReceiptPublicationOutlet";
import {
	type MatchingReceiptPublicationOutcomeModel,
	matchingReceiptFileChoiceLabels,
} from "../../../ui/features/matching/matchingReceiptPublicationModel";
import type { Locale } from "../../../ui/i18n/types";
import type {
	IncomingFiles,
	MatchingLinkReceipt,
} from "../incoming/incomingModel";
import { individualPublicationDetails } from "../publication/individualPublicationDetails";
import type {
	MatchingReceiptPublicationNotice,
	MatchingReceiptPublicationRow,
} from "./matchingReceiptPublication";
import type { MatchingReceiptPublicationController } from "./matchingReceiptPublicationController";

const copy = {
	en: {
		excluded: "Position excluded from this publication.",
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
	options: { admittedOperationID?: string; error?: string } = {},
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
			admittedOperationID: options.admittedOperationID,
			error: options.error,
		});
	}
	const current = row.current;
	const blocker = noticeText(text, row.blockers[0] ?? row.reason);
	return {
		error: options.error ?? "",
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

/**
 * Derives localized labels, counts, inspector state and the outcome summary
 * from the saved-result controller. The provider keeps preparation and the
 * confirmation write boundary; the returned model is what the portable outlet
 * frame renders without inspecting publication rules.
 */
export function buildMatchingReceiptPublicationReady({
	locale,
	receipt,
	files,
	controller,
}: {
	locale: Locale;
	receipt: MatchingLinkReceipt;
	files: IncomingFiles;
	controller: MatchingReceiptPublicationController;
}): MatchingReceiptPublicationModel {
	const text = copy[locale];
	const counts: Record<AggregateDisposition, number> = {
		create: 0,
		update: 0,
		unchanged: 0,
		skipped: 0,
		unprocessable: 0,
		blocked: 0,
	};
	const positions: AggregatePositionView[] = receipt.positions.map(
		(position) => {
			const trackId = String(position.trackId);
			const row = controller.rows.find((item) => item.trackId === trackId);
			const isIncluded = controller.included.has(trackId);
			const pendingJob = controller.pendingJobs.get(trackId);
			const failure = controller.failures.get(trackId);
			const isPrepared = controller.prepared.has(trackId);
			const disposition: AggregateDisposition = !isIncluded
				? "skipped"
				: pendingJob || failure || !row || !isPrepared
					? "blocked"
					: dispositionFor(row, true);
			counts[disposition] += 1;
			return {
				id: trackId,
				label: row
					? rowLabel(row)
					: `${position.displayedNumber ?? position.position} · ${position.title}`,
				status: pendingJob
					? { kind: "queued" }
					: !isPrepared && !failure
						? { kind: "not-prepared" }
						: { kind: "disposition", disposition },
				diagnostic: failure
					? { kind: "text", text: noticeText(text, failure) }
					: row && isPrepared
						? { kind: "text", text: rowDiagnostic(row, isIncluded, text) }
						: undefined,
			};
		},
	);
	const selectedReceiptPosition = receipt.positions.find(
		(position) => String(position.trackId) === controller.selectedPositionId,
	);
	const selectedTrackId = String(
		selectedReceiptPosition?.trackId ?? controller.selectedPositionId,
	);
	const selectedRow =
		controller.selectedRow ??
		controller.rows.find((row) => row.trackId === selectedTrackId);
	const selectedPendingJob = controller.pendingJobs.get(selectedTrackId);
	const selectedFailure = controller.failures.get(selectedTrackId);
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
					disabled: Boolean(selectedPendingJob),
					onSelect: (sourceId: string | null) =>
						controller.selectSource(
							String(selectedReceiptPosition.trackId),
							sourceId,
						),
				}
			: undefined;
	const emptyDetails = individualPublicationDetails({
		current: null,
		tags: {},
		coverHash: null,
		lyrics: null,
	});
	const inspector: AggregateInspectorPresentation = selectedReceiptPosition
		? {
				kind: "publication",
				details: selectedRow
					? detailsFor(selectedRow, files, text, {
							admittedOperationID: selectedPendingJob,
							error: selectedFailure
								? noticeText(text, selectedFailure)
								: undefined,
						})
					: {
							...emptyDetails,
							inspection: { ...emptyDetails.inspection, preview: undefined },
						},
				fileChoice,
				fileLabel:
					!fileChoice &&
					(selectedRow?.sourceName || selectedReceiptPosition.sourceIds[0])
						? {
								caption: matchingReceiptFileChoiceLabels(locale).legend,
								value:
									selectedRow?.sourceName ??
									files[selectedReceiptPosition.sourceIds[0]]?.name ??
									selectedReceiptPosition.sourceIds[0],
							}
						: undefined,
				onRefresh: controller.refresh,
			}
		: { kind: "empty" };
	const outcome: MatchingReceiptPublicationOutcomeModel | null =
		controller.outcome
			? {
					attempted: controller.outcome.attempted,
					published: controller.outcome.published,
					issues: controller.outcome.rejected.map(({ trackId, notice }) => ({
						id: trackId,
						label:
							positions.find((position) => position.id === trackId)?.label ??
							trackId,
						kind: "stale",
						detail: noticeText(text, notice),
					})),
					failures: controller.outcome.failed.map(({ trackId, notice }) => {
						const row = positions.find((position) => position.id === trackId);
						const position = receipt.positions.find(
							(item) => String(item.trackId) === trackId,
						);
						return {
							id: trackId,
							label:
								row?.label ??
								(position
									? `${position.displayedNumber ?? position.position} · ${position.title}`
									: trackId),
							detail: noticeText(text, notice),
						};
					}),
				}
			: null;

	return {
		counts,
		positions,
		included: controller.included,
		selectedPositionId: controller.selectedPositionId,
		canPrepare: controller.canPrepare,
		canConfirm: controller.canConfirm,
		preparing: controller.preparing,
		inspector,
		outcome,
		onToggleInclude: controller.toggleInclude,
		onSelectPosition: controller.selectPosition,
		onPrepare: controller.refresh,
		onConfirm: controller.confirm,
	};
}
