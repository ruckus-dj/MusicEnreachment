import { useEffect, useRef, useState } from "react";
import { Dialog, Heading, Modal, ModalOverlay } from "react-aria-components";
import { AppButton } from "../../components/AppButton";

export type SourceArtifactCleanupCandidate = {
  artifact_id: string;
  source_relative_path: string;
  state: string;
};

export type SourceArtifactCleanupOperation = {
  state: string;
  stage: string;
  safe_error?: string;
};

export type SourceArtifactCleanupResult = {
  artifact_id: string;
  state: string;
  safe_error?: string;
};

export type SourceArtifactCleanupViewProps = {
  locale: "en" | "ru";
  candidates: readonly SourceArtifactCleanupCandidate[];
  batchCount?: number;
  selected: readonly string[];
  submitted: readonly string[];
  operation?: SourceArtifactCleanupOperation;
  results?: readonly SourceArtifactCleanupResult[] | null;
  loading: boolean;
  busy: boolean;
  isPending: boolean;
  error?: { text: string };
  streamError?: string;
  onLoad: () => void;
  onToggle: (id: string) => void;
  onStart: (ids: readonly string[]) => void;
};

const copy = {
  en: {
    title: "Temporary analysis copies",
    description:
      "Paths are relative to each source root. Cleanup removes only temporary copies, not source files.",
    loading: "Loading available copies…",
    refresh: "Refresh selection",
    currentBatch: (count: string) =>
      `Current selection: ${count}. This is the number of records in the returned batch, not the total number of copies or a deletion limit. Maximum batch size: 1000.`,
    refreshing: "Refreshing selection…",
    noCandidates: "No copies are available for cleanup.",
    candidates: "Copies available for cleanup",
    selectAction: (count: string) => `Clean up selected (${count})`,
    outsideBatch: "Selected copies outside the current selection",
    outsideBatchHelp:
      "These selected IDs are no longer in the current selection; they have not been replaced by other copies:",
    deselect: "Deselect",
    operation: "Copy cleanup",
    running: (state: string, stage: string) => `Operation ${state}: ${stage}.`,
    completed: (state: string) => `Operation completed: ${state}.`,
    streamInterrupted:
      "Updates were interrupted; the client is still waiting for the latest operation snapshot.",
    results: "Cleanup results",
    unknownRemoval:
      "The server returned no per-copy results; deletion state is unknown.",
    unreportedResult: "No cleanup result reported",
    confirmTitle: "Clean up selected copies?",
    confirmationCopies: (count: string) =>
      `Selected copies: ${count}. Only these will be cleaned up:`,
    sourceFilesNotDeleted: "Source files are not deleted.",
    confirm: "Confirm cleanup",
    cancel: "Cancel",
    operationStates: {
      queued: "Queued",
      running: "Running",
      succeeded: "Completed",
      failed: "Failed",
      unknown: "Unknown",
    },
    itemStates: {
      cleanup_eligible: "Eligible for cleanup",
      cleanup_failed: "Previous cleanup failed",
      succeeded: "Cleaned up",
      failed: "Could not delete",
      claimed: "In progress",
      unknown: "Unknown",
    },
  },
  ru: {
    title: "Временные копии анализа",
    description:
      "Пути указаны от корня источника. Очистка удаляет только временные копии, а не исходные файлы.",
    loading: "Загрузка доступных копий…",
    refresh: "Обновить подборку",
    currentBatch: (count: string) =>
      `В текущей подборке: ${count}. Это количество записей в возвращённом пакете, а не общее число копий или лимит удаления. Максимум в пакете — 1000.`,
    refreshing: "Обновление подборки…",
    noCandidates: "Нет копий, доступных для очистки.",
    candidates: "Копии, доступные для очистки",
    selectAction: (count: string) => `Очистить выбранные (${count})`,
    outsideBatch: "Выбранные копии вне текущей подборки",
    outsideBatchHelp:
      "Эти выбранные идентификаторы больше не входят в текущую подборку; они не заменены другими копиями:",
    deselect: "Снять выбор",
    operation: "Очистка копий",
    running: (state: string, stage: string) => `Операция ${state}: ${stage}.`,
    completed: (state: string) => `Операция завершена: ${state}.`,
    streamInterrupted:
      "Поток обновлений прерван; клиент продолжает ждать актуальный снимок операции.",
    results: "Результаты очистки",
    unknownRemoval:
      "Сервер не вернул результаты по отдельным копиям; состояние удаления неизвестно.",
    unreportedResult: "Результат очистки не получен",
    confirmTitle: "Очистить выбранные копии?",
    confirmationCopies: (count: string) =>
      `Выбрано копий: ${count}. Очистка затронет только их:`,
    sourceFilesNotDeleted: "Файлы источника не удаляются.",
    confirm: "Подтвердить очистку",
    cancel: "Отмена",
    operationStates: {
      queued: "В очереди",
      running: "Выполняется",
      succeeded: "Завершено",
      failed: "Ошибка",
      unknown: "Неизвестно",
    },
    itemStates: {
      cleanup_eligible: "Ожидает очистки",
      cleanup_failed: "Предыдущая очистка не удалась",
      succeeded: "Очищена",
      failed: "Не удалось удалить",
      claimed: "Выполняется",
      unknown: "Неизвестно",
    },
  },
} as const;

const panelClassName =
  "min-w-0 mb-[10px] rounded-[8px] border border-[var(--line)] bg-[var(--surface)] p-[10px] [&>h2]:mb-2 [&>h2]:text-sm [&>h2]:font-semibold [&>h3]:mb-2 [&>h3]:text-sm [&>h3]:font-semibold [&>p]:my-2";
const listClassName = "m-0 grid min-w-0 gap-2 pl-5 wrap-anywhere";
const overlayClassName =
  "react-aria-ModalOverlay ui-modal-overlay fixed inset-0 z-[100] grid place-items-center overflow-auto overscroll-contain bg-[#0008] p-4";
const modalClassName =
  "react-aria-Modal ui-modal w-full max-w-[560px] rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)] p-[18px] text-[var(--ink)] shadow-[0_16px_48px_#0003]";
const dialogClassName =
  "react-aria-Dialog ui-dialog wrap-anywhere outline-none [&_[slot=title]]:mb-2 [&_[slot=title]]:mt-0 [&_[slot=title]]:text-base [&_p]:my-3 [&_p]:mb-[14px] [&_button]:mr-2 [&_button]:rounded-[var(--control-radius)] [&_button]:border [&_button]:border-[var(--control)] [&_button]:px-[10px] [&_button]:py-1.5";

type Confirmation = { id: string; label: string };

export function SourceArtifactCleanupView({
  locale,
  candidates,
  batchCount,
  selected,
  submitted,
  operation,
  results,
  loading,
  busy,
  isPending,
  error,
  streamError,
  onLoad,
  onToggle,
  onStart,
}: SourceArtifactCleanupViewProps) {
  const label = copy[locale];
  const [confirmation, setConfirmation] = useState<Confirmation[]>();
  const alert = useRef<HTMLParagraphElement>(null);

  useEffect(() => {
    if (error) alert.current?.focus();
  }, [error]);

  const number = (value: number) =>
    new Intl.NumberFormat(locale === "ru" ? "ru-RU" : "en-US").format(value);
  const stateText = (state: string) =>
    Object.hasOwn(label.itemStates, state)
      ? label.itemStates[state as keyof typeof label.itemStates]
      : label.itemStates.unknown;
  const operationStateText = (state: string) =>
    Object.hasOwn(label.operationStates, state)
      ? label.operationStates[state as keyof typeof label.operationStates]
      : label.operationStates.unknown;

  const pathCounts = new Map<string, number>();
  const shortLabels = new Map<string, number>();
  const shortLabel = (path: string, id: string) =>
    `${path} (${id.slice(0, 8)})`;
  for (const { artifact_id, source_relative_path: path } of candidates) {
    pathCounts.set(path, (pathCounts.get(path) ?? 0) + 1);
    const short = shortLabel(path, artifact_id);
    shortLabels.set(short, (shortLabels.get(short) ?? 0) + 1);
  }
  const candidateLabels = new Map(
    candidates.map(({ artifact_id: id, source_relative_path: path }) => {
      if (pathCounts.get(path) === 1) return [id, path];
      const short = shortLabel(path, id);
      return [id, shortLabels.get(short) === 1 ? short : `${path} (${id})`];
    }),
  );
  const selectedNotInBatch = selected.filter((id) => !candidateLabels.has(id));
  const selectedCount = number(selected.length);
  const resultIDs = new Set(results?.map(({ artifact_id }) => artifact_id));
  const unreported = isPending
    ? []
    : submitted.filter((id) => !resultIDs.has(id));

  return (
    <section
      aria-labelledby="artifact-cleanup-title"
      className={panelClassName}
    >
      <h2 id="artifact-cleanup-title">{label.title}</h2>
      <p>{label.description}</p>

      {loading && batchCount === undefined && (
        <p role="status">{label.loading}</p>
      )}
      {error && (
        <>
          <p ref={alert} tabIndex={-1} role="alert">
            {error.text}
          </p>
          <AppButton onPress={onLoad}>{label.refresh}</AppButton>
        </>
      )}
      {batchCount !== undefined && (
        <p role="status">{label.currentBatch(number(batchCount))}</p>
      )}
      {loading && batchCount !== undefined && (
        <p role="status">{label.refreshing}</p>
      )}

      {batchCount !== undefined && candidates.length === 0 ? (
        <p>{label.noCandidates}</p>
      ) : (
        candidates.length > 0 && (
          <>
            <ul aria-label={label.candidates} className={listClassName}>
              {candidates.map((candidate) => (
                <li key={candidate.artifact_id}>
                  <label>
                    <input
                      type="checkbox"
                      checked={selected.includes(candidate.artifact_id)}
                      disabled={isPending || busy}
                      onChange={() => onToggle(candidate.artifact_id)}
                    />{" "}
                    <code>{candidateLabels.get(candidate.artifact_id)}</code> —{" "}
                    {stateText(candidate.state)}
                  </label>
                </li>
              ))}
            </ul>
            <AppButton
              isDisabled={selected.length === 0 || isPending || busy}
              onPress={() =>
                setConfirmation(
                  selected.map((id) => ({
                    id,
                    label: candidateLabels.get(id) ?? id,
                  })),
                )
              }
            >
              {label.selectAction(selectedCount)}
            </AppButton>
          </>
        )
      )}

      {selectedNotInBatch.length > 0 && (
        <section aria-label={label.outsideBatch}>
          <p>{label.outsideBatchHelp}</p>
          <ul className={listClassName}>
            {selectedNotInBatch.map((id) => (
              <li key={id}>
                <code>{id}</code>{" "}
                <AppButton
                  isDisabled={isPending || busy}
                  onPress={() => onToggle(id)}
                >
                  {label.deselect}
                </AppButton>
              </li>
            ))}
          </ul>
        </section>
      )}

      {operation && (
        <section aria-labelledby="artifact-cleanup-operation-title">
          <h3 id="artifact-cleanup-operation-title">{label.operation}</h3>
          {isPending ? (
            <p role="status">
              {label.running(
                operationStateText(operation.state),
                operation.stage,
              )}
            </p>
          ) : (
            <p role="status">
              {label.completed(operationStateText(operation.state))}
              {operation.safe_error ? ` ${operation.safe_error}` : ""}
            </p>
          )}
          {streamError && isPending && (
            <p role="status">{label.streamInterrupted}</p>
          )}
          {(Boolean(results?.length) || unreported.length > 0) && (
            <ul aria-label={label.results} className={listClassName}>
              {results?.map((result) => (
                <li key={result.artifact_id}>
                  <code>{result.artifact_id}</code>: {stateText(result.state)}
                  {result.safe_error ? ` — ${result.safe_error}` : ""}
                </li>
              ))}
              {unreported.map((id) => (
                <li key={id}>
                  <code>{id}</code>: {label.unreportedResult}
                </li>
              ))}
            </ul>
          )}
          {!isPending && !results?.length && <p>{label.unknownRemoval}</p>}
        </section>
      )}

      {confirmation && (
        <ModalOverlay
          className={overlayClassName}
          isOpen
          isDismissable={!busy}
          isKeyboardDismissDisabled={busy}
          onOpenChange={(open) => {
            if (!open && !busy) setConfirmation(undefined);
          }}
        >
          <Modal className={modalClassName}>
            <Dialog className={dialogClassName} aria-label={label.confirmTitle}>
              <Heading
                slot="title"
                className="react-aria-Heading ui-heading min-w-0"
              >
                {label.confirmTitle}
              </Heading>
              <p>{label.confirmationCopies(number(confirmation.length))}</p>
              <ul className={listClassName}>
                {confirmation.map(({ id, label: path }) => (
                  <li key={id}>
                    <code>{path}</code>
                  </li>
                ))}
              </ul>
              <p>{label.sourceFilesNotDeleted}</p>
              <AppButton
                autoFocus
                isDisabled={busy}
                onPress={() => setConfirmation(undefined)}
              >
                {label.cancel}
              </AppButton>
              <AppButton
                isDisabled={busy}
                onPress={() => {
                  const ids = confirmation.map(({ id }) => id);
                  setConfirmation(undefined);
                  onStart(ids);
                }}
              >
                {label.confirm}
              </AppButton>
            </Dialog>
          </Modal>
        </ModalOverlay>
      )}
    </section>
  );
}
