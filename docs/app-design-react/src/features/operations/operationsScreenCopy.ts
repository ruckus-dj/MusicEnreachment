import type { OperationsListCopy } from "./OperationsList";

/**
 * Screen copy shared by the application and the demo prototype.
 *
 * The colocated Operations list already receives every row, filter and action
 * through props. This catalog covers only the parts both hosts render the same
 * way: list chrome, status/kind filter labels, row action labels and the
 * pagination range. Server-scope and demo-specific wording stays with each
 * host adapter.
 */
export interface OperationsScreenCopy extends OperationsListCopy {
  attention: string;
  allStatuses: string;
  failed: string;
  running: string;
  queued: string;
  allKinds: string;
  kindPublication: string;
  kindAnalysis: string;
  kindRemoval: string;
  kindMatching: string;
  paginationRange: string;
  retry: string;
  retrying: string;
  dismiss: string;
  dismissing: string;
  dismissed: string;
  abandon: string;
  abandoning: string;
  abandoned: string;
  abandonError: string;
  retryError: string;
  dismissError: string;
}

export const operationsScreenCopy = {
  en: {
    title: "Current operations",
    description:
      "Work in progress and errors that need attention. History is not retained.",
    refresh: "Refresh",
    refreshing: "Refreshing…",
    loading: "Loading operations…",
    loadError: "Could not load operations.",
    retryLoad: "Retry loading",
    emptyTitle: "No current operations",
    emptyDescription:
      "Work in progress and failed operations will appear here.",
    target: "Target",
    operation: "Operation",
    stateStage: "Status / stage",
    progress: "Progress",
    created: "Created",
    error: "Error",
    actions: "Actions",
    footnote:
      "Queued, running, and failed operations are shown. Completed operations are hidden.",
    search: "Search operations",
    statusFilter: "Status",
    kindFilter: "Kind",
    details: "Details",
    close: "Close",
    previous: "Previous",
    next: "Next",
    allStatuses: "All",
    attention: "Needs attention",
    failed: "Failed",
    running: "Running",
    queued: "Queued",
    allKinds: "All kinds",
    kindPublication: "Publication",
    kindAnalysis: "Analysis",
    kindRemoval: "Removal",
    kindMatching: "Matching",
    paginationRange: "{from}–{to} of {total}",
    retry: "Retry",
    retrying: "Retrying…",
    dismiss: "Dismiss",
    dismissing: "Dismissing…",
    dismissed: "Operation dismissed.",
    abandon: "Abandon…",
    abandoning: "Loading…",
    abandoned: "Unfinished republications abandoned.",
    abandonError: "Could not abandon unfinished republications.",
    retryError: "Could not retry operation.",
    dismissError: "Could not dismiss operation.",
  },
  ru: {
    title: "Текущие операции",
    description:
      "Работа в процессе и ошибки, требующие внимания. История не сохраняется.",
    refresh: "Обновить",
    refreshing: "Обновление…",
    loading: "Загрузка операций…",
    loadError: "Не удалось загрузить операции.",
    retryLoad: "Повторить загрузку",
    emptyTitle: "Нет текущих операций",
    emptyDescription:
      "Здесь появятся операции в процессе и неудачные операции.",
    target: "Объект",
    operation: "Операция",
    stateStage: "Статус / этап",
    progress: "Прогресс",
    created: "Создано",
    error: "Ошибка",
    actions: "Действия",
    footnote:
      "Показаны операции в очереди, выполняемые и неудачные. Завершённые операции скрыты.",
    search: "Поиск операций",
    statusFilter: "Статус",
    kindFilter: "Тип",
    details: "Подробности",
    close: "Закрыть",
    previous: "Назад",
    next: "Вперёд",
    allStatuses: "Все",
    attention: "Требует внимания",
    failed: "Ошибка",
    running: "Выполняется",
    queued: "В очереди",
    allKinds: "Все типы",
    kindPublication: "Публикация",
    kindAnalysis: "Анализ",
    kindRemoval: "Удаление",
    kindMatching: "Сопоставление",
    paginationRange: "{from}–{to} из {total}",
    retry: "Повторить",
    retrying: "Повтор…",
    dismiss: "Скрыть",
    dismissing: "Скрытие…",
    dismissed: "Операция скрыта.",
    abandon: "Прервать…",
    abandoning: "Загрузка…",
    abandoned: "Незавершённые перепубликации прерваны.",
    abandonError: "Не удалось прервать незавершённые перепубликации.",
    retryError: "Не удалось повторить операцию.",
    dismissError: "Не удалось скрыть операцию.",
  },
} as const satisfies Record<"en" | "ru", OperationsScreenCopy>;
