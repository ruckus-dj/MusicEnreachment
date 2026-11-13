import type { Locale } from "../../i18n/locale";

function russianCountForm(count: number, forms: [string, string, string]) {
  const lastTwoDigits = count % 100;
  if (lastTwoDigits >= 11 && lastTwoDigits <= 14) return forms[2];
  const lastDigit = count % 10;
  if (lastDigit === 1) return forms[0];
  if (lastDigit >= 2 && lastDigit <= 4) return forms[1];
  return forms[2];
}

export const incomingMessages = {
  en: {
    overviewTitle: "Incoming groups",
    overviewCaption:
      "Files, paths, source readiness, and saved group evaluations",
    group: "Group",
    uniqueFilesPaths: "Unique files / paths",
    readyTotal: "Ready / total",
    evaluationState: "Evaluation status",
    openGroup: (index: number) => `Open group ${index}`,
    manualGrouping: "Manual grouping",
    automaticGrouping: "Automatic grouping",
    filePaths: "File paths",
    candidatesAuthority: (count: number, authority: string) =>
      `${count} ${count === 1 ? "candidate" : "candidates"} · ${authority}`,
    evaluationNotReceived: "Evaluation has not been received",
    openGroupForEvaluation: "Open a group to read its evaluation",
    incompleteRelease: "incomplete track list",
    releaseOrdinal: (index: number) => `release ${index}`,
    discTrack: (disc: number, track: string | number, title: string) =>
      `Disc ${disc} · track ${track} · ${title}`,
    noValue: "No value",
    metadataNotSaved: "Metadata not saved.",
    loadGroupsError: "Could not load incoming groups.",
    loadReviewError: "Could not load the matching evaluation.",
    previewError: "Could not create a preview.",
    confirmError: "Could not confirm assignments. Retry uses the same request.",
    authorityError:
      "Could not change matching mode. Retry uses the same request.",
    authorityConflict:
      "Group or matching mode changed. Refresh them before trying again.",
    confirmConflict:
      "Evaluation or group data changed. Your draft is saved; refresh the evaluation manually before creating a new preview.",
    groupsLoadError: "Could not load groups.",
    refreshAuthority: "Refresh group and mode data",
    refreshEvaluation: "Refresh saved evaluation",
    title: "Review incoming groups",
    introduction:
      "Choose a release and explicitly assign files to positions. The draft is kept on this page only.",
    refreshGroups: "Refresh list",
    loadingGroups: "Loading incoming groups…",
    noGroups: "There are no incoming groups.",
    incomingGroupsLabel: "Incoming group",
    groupOption: (index: number, count: number, manual: boolean) =>
      `Group ${index} · ${count} ${count === 1 ? "file" : "files"}${manual ? " · grouped manually" : ""}`,
    groupEditingSummary: "Merge, split, or move group files",
    loadingEvaluation: "Reading saved evaluation…",
    retryReading: "Retry reading",
    evaluationNotRun:
      "Evaluation has not been run. Automatic matching does not start here.",
    authorityHeading: "Current matching authority",
    modeLabel: "Mode:",
    revisionLabel: "revision:",
    savedEvaluationExplanation:
      "The saved current evaluation and its evidence are shown separately below. Source deletion is performed outside this screen.",
    returnAutomatic: "Return to automatic matching",
    retryReturnAutomatic: "Retry returning to automatic matching",
    authoritySavedNotice:
      "Return to automatic matching saved. A new evaluation is pending.",
    confirmNotice:
      "Confirmation saved. Files not included in the assignment remain in the incoming group.",
    refreshNotice:
      "Evaluation refreshed. Your assignment draft was kept for the selected release.",
    proposalUnavailable:
      "Saved proposal data is unavailable or incomplete. Manual review is not possible for this evaluation.",
    searchPreparationLabel: "Search and prepare a release",
    groupSummaryLabel: "Incoming group summary",
    noReleaseLayouts: "No release layouts are available for this evaluation.",
    groupDraftHeading: "Group files and current draft",
    draftAssignedTotal: "Draft: assigned / total",
    sourceSnapshot: "Source snapshot",
    providerSnapshot: "Provider snapshot",
    savedFacts: (count: number, total: number) =>
      `${count} of ${total} files with saved data`,
    savedCandidates: (count: number) =>
      `${count} saved ${count === 1 ? "candidate" : "candidates"}`,
    noCandidates: "no candidates",
    linkedManualFiles: "Unique files with saved manual links:",
    draftDoesNotChangeLinks:
      "The assignment draft does not change these links until confirmation.",
    searchFilesHeading: "Search and files",
    savedEvaluation: "Saved evaluation",
    releaseSelectionLabel: "Release (manual selection)",
    chooseRelease: "Choose a release…",
    reviewRevisionParticipants: (revision: string, count: number) =>
      `Revision: ${revision} · participants: ${count}`,
    savedRelease: "This release is saved in the server evaluation.",
    savedCandidatesHeading: "Saved candidates",
    releaseOutsideLayout: "Release outside the saved layout",
    releaseConfidence: "Release confidence:",
    unknown: "unknown",
    positions: "positions:",
    noCandidateEdges: "There are no saved candidate edges.",
    positionHeading: "Release positions",
    positionInstructions:
      "Drag a free file to a position or choose an assignment in the file list. Multiple files can be assigned to one position.",
    completeTrackList: "Complete position list",
    incompleteTrackList: "Position list is marked incomplete",
    positionCount: (count: number) =>
      `${count} ${count === 1 ? "position" : "positions"}`,
    assignedFiles: (count: number) =>
      `${count} ${count === 1 ? "file assigned" : "files assigned"}`,
    noAssignedFiles: "No files assigned",
    choosePrepareRelease:
      "Choose or prepare a release to show its positions and start manual assignment.",
    sourceFilesHeading: "Source files",
    sourceInstructions:
      "Drag a free file to a position or assign it from this keyboard-accessible list.",
    inspectorLabel: "Inspect",
    free: "Free",
    assigned: (label: string) => `Assigned: ${label}`,
    assignmentOutsideRelease: "Assignment is outside the selected release",
    assignment: "Assignment",
    leaveIncoming: "Do not assign — leave in incoming",
    savedProposalMetadata: "Saved proposal metadata",
    incomingFileMetadata: "Incoming group file metadata",
    groupDiagnostics: "Group diagnostics",
    noFilesInGroup: "There are no files in this group.",
    inspectorFileMissing: "no file selected",
    assignmentActions: "Assignment actions",
    processing: "Processing…",
    previewAll: "Preview all assignments",
    retryConfirmation: "Retry confirmation",
    confirmExplicitly: "Confirm explicitly",
    previewHeading: "Full assignment preview",
    previewSummary: (title: string, assigned: number, omitted: number) =>
      `Release: ${title}. Assigned: ${assigned}; omitted: ${omitted}.`,
    previewDigest: "Preview digest:",
    manualMetadataApplied: "Manual metadata to apply",
    titleOverride: (value: string) => `title “${value}”`,
    artistOverrides: (values: string) => `artists ${values}`,
    emptyList: "(empty list)",
    confirmedLocalRelease: "Confirmed · local release",
    receiptCounts: (assigned: number, total: number, omitted: number) =>
      `Files assigned: ${assigned} of ${total}. Omitted: ${omitted}.`,
    file: "File",
    remainsIncoming: "remains in incoming",
  },
  ru: {
    overviewTitle: "Входящие группы",
    overviewCaption:
      "Файлы, пути, готовность источников и сохранённые оценки групп",
    group: "Группа",
    uniqueFilesPaths: "Уникальные файлы / пути",
    readyTotal: "Готово / всего",
    evaluationState: "Состояние оценки",
    openGroup: (index: number) => `Открыть группу ${index}`,
    manualGrouping: "Ручная группировка",
    automaticGrouping: "Автоматическая группировка",
    filePaths: "Пути файлов",
    candidatesAuthority: (count: number, authority: string) =>
      `${count} ${russianCountForm(count, ["кандидат", "кандидата", "кандидатов"])} · ${authority}`,
    evaluationNotReceived: "Оценка ещё не получена",
    openGroupForEvaluation: "Откройте группу для чтения оценки",
    incompleteRelease: "неполный список",
    releaseOrdinal: (index: number) => `издание ${index}`,
    discTrack: (disc: number, track: string | number, title: string) =>
      `Диск ${disc} · трек ${track} · ${title}`,
    noValue: "Нет значения",
    metadataNotSaved: "Метаданные не сохранены.",
    loadGroupsError: "Не удалось загрузить входящие группы.",
    loadReviewError: "Не удалось загрузить оценку сопоставления.",
    previewError: "Не удалось создать предпросмотр.",
    confirmError:
      "Не удалось подтвердить назначение. Повтор использует тот же запрос.",
    authorityError:
      "Не удалось изменить режим сопоставления. Повтор использует тот же запрос.",
    authorityConflict:
      "Данные группы или режима изменились. Обновите их вручную перед новой попыткой.",
    confirmConflict:
      "Данные оценки или группы изменились. Черновик сохранён; обновите оценку вручную перед новым предпросмотром.",
    groupsLoadError: "Не удалось загрузить группы.",
    refreshAuthority: "Обновить данные группы и режима",
    refreshEvaluation: "Обновить сохранённую оценку",
    title: "Разбор входящих групп",
    introduction:
      "Выберите издание и явно назначьте файлы позициям. Черновик хранится только на этой странице.",
    refreshGroups: "Обновить список",
    loadingGroups: "Загрузка входящих групп…",
    noGroups: "Входящих групп нет.",
    incomingGroupsLabel: "Входящая группа",
    groupOption: (index: number, count: number, manual: boolean) =>
      `Группа ${index} · ${count} ${russianCountForm(count, ["файл", "файла", "файлов"])}${manual ? " · вручную сгруппирована" : ""}`,
    groupEditingSummary: "Объединить, разделить или переместить файлы групп",
    loadingEvaluation: "Чтение сохранённой оценки…",
    retryReading: "Повторить чтение",
    evaluationNotRun:
      "Оценка ещё не выполнялась. Автоматическое сопоставление здесь не запускается.",
    authorityHeading: "Текущий режим управления сопоставлением",
    modeLabel: "Режим:",
    revisionLabel: "ревизия:",
    savedEvaluationExplanation:
      "Сохранённая текущая оценка и её доказательства показаны отдельно ниже. Удаление источников выполняется вне этого экрана.",
    returnAutomatic: "Вернуть к автоматическому сопоставлению",
    retryReturnAutomatic: "Повторить возврат к автоматическому сопоставлению",
    authoritySavedNotice:
      "Возврат к автоматическому сопоставлению сохранён. Ожидается новая оценка.",
    confirmNotice:
      "Подтверждение сохранено. Не включённые в назначение файлы остаются во входящей группе.",
    refreshNotice:
      "Оценка обновлена. Черновик назначений сохранён для выбранного издания.",
    proposalUnavailable:
      "Сохранённые данные предложения недоступны или неполны. Ручной просмотр для этой оценки невозможен.",
    searchPreparationLabel: "Поиск и подготовка издания",
    groupSummaryLabel: "Сводка входящей группы",
    noReleaseLayouts: "Для этой оценки нет доступных макетов изданий.",
    groupDraftHeading: "Файлы группы и текущий черновик",
    draftAssignedTotal: "Черновик: назначено / всего",
    sourceSnapshot: "Снимок источника",
    providerSnapshot: "Снимок провайдера",
    savedFacts: (count: number, total: number) =>
      `${count} из ${total} ${russianCountForm(total, ["файла", "файлов", "файлов"])} с сохранёнными данными`,
    savedCandidates: (count: number) =>
      `${count} ${russianCountForm(count, ["сохранённый кандидат", "сохранённых кандидата", "сохранённых кандидатов"])}`,
    noCandidates: "кандидаты отсутствуют",
    linkedManualFiles: "Уникальных файлов с сохранёнными ручными связями:",
    draftDoesNotChangeLinks:
      "Черновик назначений не изменяет эти связи до подтверждения.",
    searchFilesHeading: "Поиск и файлы",
    savedEvaluation: "Сохраненная оценка",
    releaseSelectionLabel: "Издание (выбор вручную)",
    chooseRelease: "Выберите издание…",
    reviewRevisionParticipants: (revision: string, count: number) =>
      `Ревизия: ${revision} · участников: ${count}`,
    savedRelease: "Это издание сохранено в оценке сервера.",
    savedCandidatesHeading: "Сохранённые кандидаты",
    releaseOutsideLayout: "Издание вне сохранённого макета",
    releaseConfidence: "Уверенность издания:",
    unknown: "неизвестно",
    positions: "позиции:",
    noCandidateEdges: "Сохранённых рёбер-кандидатов нет.",
    positionHeading: "Позиции издания",
    positionInstructions:
      "Перетащите свободный файл на позицию или выберите назначение в списке файла. Несколько файлов могут быть назначены одной позиции.",
    completeTrackList: "Список позиций полный",
    incompleteTrackList: "Список позиций помечен как неполный",
    positionCount: (count: number) =>
      `${count} ${russianCountForm(count, ["позиция", "позиции", "позиций"])}`,
    assignedFiles: (count: number) => `Назначено файлов: ${count}`,
    noAssignedFiles: "Нет назначенных файлов",
    choosePrepareRelease:
      "Выберите или подготовьте издание, чтобы показать его позиции и начать ручное назначение.",
    sourceFilesHeading: "Файлы источников",
    sourceInstructions:
      "Перетащите свободный файл на позицию или назначьте его из этого списка с клавиатуры.",
    inspectorLabel: "Осмотр",
    free: "Свободен",
    assigned: (label: string) => `Назначен: ${label}`,
    assignmentOutsideRelease: "Назначение вне выбранного издания",
    assignment: "Назначение",
    leaveIncoming: "Не назначать — оставить во входящих",
    savedProposalMetadata: "Сохранённые метаданные предложения",
    incomingFileMetadata: "Метаданные файла из входящей группы",
    groupDiagnostics: "Диагностика группы",
    noFilesInGroup: "В группе нет файлов.",
    inspectorFileMissing: "файл не выбран",
    assignmentActions: "Действия с назначением",
    processing: "Обработка…",
    previewAll: "Предпросмотр всего назначения",
    retryConfirmation: "Повторить подтверждение",
    confirmExplicitly: "Явно подтвердить",
    previewHeading: "Предпросмотр полного назначения",
    previewSummary: (title: string, assigned: number, omitted: number) =>
      `Издание: ${title}. Назначено: ${assigned}; пропущено: ${omitted}.`,
    previewDigest: "Контрольный digest:",
    manualMetadataApplied: "Ручные метаданные, которые будут применены",
    titleOverride: (value: string) => `название «${value}»`,
    artistOverrides: (values: string) => `исполнители ${values}`,
    emptyList: "(пустой список)",
    confirmedLocalRelease: "Подтверждено · локальное издание",
    receiptCounts: (assigned: number, total: number, omitted: number) =>
      `Назначено файлов: ${assigned} из ${total}. Пропущено: ${omitted}.`,
    file: "Файл",
    remainsIncoming: "остаётся во входящих",
  },
} as const;

export type IncomingMessages = (typeof incomingMessages)[Locale];
export type IncomingMessageKey = keyof IncomingMessages;

export type IncomingFeedback = {
  key: IncomingMessageKey;
  raw?: string;
  status?: number;
  refresh?: "authority" | "evaluation";
};

export function incomingFeedbackText(
  locale: Locale,
  feedback: IncomingFeedback,
) {
  if (feedback.raw !== undefined) return feedback.raw;
  const value = incomingMessages[locale][feedback.key];
  if (typeof value !== "string") return "";
  return feedback.status === undefined
    ? value
    : `${value} (HTTP ${feedback.status}).`;
}
