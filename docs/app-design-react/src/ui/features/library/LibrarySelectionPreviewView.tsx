import { useId } from "react";
import {
  Button,
  Dialog,
  Heading,
  Modal,
  ModalOverlay,
} from "react-aria-components";

export type LibrarySelectionPreviewLocale = "en" | "ru";

export interface LibrarySelectionPreviewPosition {
  release_id: string;
  position_id: string;
  title: string;
  release_title: string;
}

export interface LibrarySelectionPreviewRow {
  release_id: string;
  position_id: string;
  disposition: string;
  reason?: string;
  resolved_path?: string;
  path_clash: boolean;
}

export type LibrarySelectionPreviewState =
  | { status: "loading" }
  | { status: "error"; onRetry?: () => void }
  | { status: "limit" }
  | { status: "ready"; rows: readonly LibrarySelectionPreviewRow[] };

export interface LibrarySelectionPreviewViewProps {
  locale: LibrarySelectionPreviewLocale;
  positions: readonly LibrarySelectionPreviewPosition[];
  state: LibrarySelectionPreviewState;
  onClose: () => void;
}

const copyByLocale = {
  en: {
    title: "Preview publication",
    close: "Close",
    retry: "Retry",
    loading: "Checking the selected positions…",
    error: "Could not prepare the publication preview.",
    limit:
      "Select at most 100 positions to preview. Your selection has not changed.",
    note: "Each position is checked separately. This preview does not publish anything or guarantee that these positions can be published together.",
    conflict: "Several selected positions may use the same destination.",
    dispositions: {
      create: "Create",
      update: "Update",
      unchanged: "Unchanged",
      skipped: "Skip",
      unprocessable: "Unprocessable metadata",
      blocked: "Blocked",
      unchecked: "Not checked",
    },
    reasons: {
      missing_audio: "Audio is unavailable.",
      position_not_found: "The position no longer exists in this release.",
      source_selection_required:
        "No audio source could be selected automatically. Check the position page.",
      invalid_metadata: "The metadata is incomplete or invalid.",
      stale_authority:
        "The position changed during the check. Refresh the preview.",
      publication_attempt_active:
        "A publication operation is already in progress.",
      preview_deadline_exceeded: "The preview ran out of time.",
      managed_tools_unavailable: "The required audio tools are unavailable.",
      publication_format_unavailable: "No output format could be planned.",
      publication_path_unavailable: "No destination could be planned.",
      resolved_path_unavailable: "The destination could not be verified.",
      withheld: "No publication plan could be verified for this position.",
    },
  },
  ru: {
    title: "Предпросмотр публикации",
    close: "Закрыть",
    retry: "Повторить",
    loading: "Проверяем выбранные позиции…",
    error: "Не удалось подготовить предпросмотр публикации.",
    limit:
      "Для предпросмотра выберите не более 100 позиций. Ваш выбор не изменился.",
    note: "Каждая позиция проверяется отдельно. Предпросмотр ничего не публикует и не гарантирует, что эти позиции можно опубликовать вместе.",
    conflict: "У нескольких выбранных позиций может совпасть путь назначения.",
    dispositions: {
      create: "Создать",
      update: "Обновить",
      unchanged: "Без изменений",
      skipped: "Пропустить",
      unprocessable: "Невозможно обработать метаданные",
      blocked: "Заблокировано",
      unchecked: "Не проверено",
    },
    reasons: {
      missing_audio: "Аудио недоступно.",
      position_not_found: "Этой позиции больше нет в релизе.",
      source_selection_required:
        "Не удалось автоматически выбрать источник аудио. Проверьте страницу позиции.",
      invalid_metadata: "Метаданные неполные или некорректные.",
      stale_authority:
        "Позиция изменилась во время проверки. Обновите предпросмотр.",
      publication_attempt_active:
        "Для позиции уже запущена операция публикации.",
      preview_deadline_exceeded: "Не хватило времени на проверку.",
      managed_tools_unavailable:
        "Необходимые инструменты обработки аудио недоступны.",
      publication_format_unavailable:
        "Не удалось определить формат публикации.",
      publication_path_unavailable: "Не удалось определить путь назначения.",
      resolved_path_unavailable: "Не удалось проверить путь назначения.",
      withheld: "Не удалось проверить план публикации для этой позиции.",
    },
  },
} as const;

export const librarySelectionButtonClass =
  "min-h-9 rounded-[5px] border border-[var(--line)] bg-[var(--surface)] px-[10px] py-[6px] text-[var(--ink)] data-[hovered]:bg-[var(--raised)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--focus)]";

export const maxLibraryPreviewPositions = 100;

function previewPositionKey(position: {
  release_id: string;
  position_id: string;
}) {
  return `${position.release_id}:${position.position_id}`;
}

function countDispositions(rows: readonly LibrarySelectionPreviewRow[]) {
  const counts = new Map<string, number>();
  for (const row of rows)
    counts.set(row.disposition, (counts.get(row.disposition) ?? 0) + 1);
  return counts;
}

export function LibrarySelectionPreviewView({
  locale,
  positions,
  state,
  onClose,
}: LibrarySelectionPreviewViewProps) {
  const copy = copyByLocale[locale];
  const headingId = useId();
  const dispositionTitles: Record<string, string> = copy.dispositions;
  const reasonTitles: Record<string, string> = copy.reasons;
  const positionsByKey = new Map(
    positions.map((position) => [previewPositionKey(position), position]),
  );
  const counts =
    state.status === "ready" ? countDispositions(state.rows) : null;
  return (
    <ModalOverlay
      isOpen
      isDismissable
      onOpenChange={(open) => !open && onClose()}
      className="fixed inset-0 z-[80] grid place-items-center overflow-y-auto bg-black/45 p-3"
    >
      <Modal className="w-full max-w-[680px] rounded-lg border border-[var(--line)] bg-[var(--surface)] p-4 text-[var(--ink)] shadow-xl">
        <Dialog aria-labelledby={headingId} className="min-w-0 outline-none">
          <Heading
            slot="title"
            id={headingId}
            className="text-lg font-semibold"
          >
            {copy.title}
          </Heading>
          <p className="my-2 text-sm text-[var(--muted)]">{copy.note}</p>
          {state.status === "limit" ? (
            <p role="alert">{copy.limit}</p>
          ) : state.status === "error" ? (
            <div role="alert" className="my-3 space-y-2">
              <p>{copy.error}</p>
              {state.onRetry && (
                <Button
                  className={librarySelectionButtonClass}
                  onPress={state.onRetry}
                >
                  {copy.retry}
                </Button>
              )}
            </div>
          ) : state.status === "loading" ? (
            <p role="status">{copy.loading}</p>
          ) : (
            <>
              <p role="status" className="my-2 font-medium">
                {Object.entries(copy.dispositions)
                  .filter(([key]) => counts?.has(key))
                  .map(([key, title]) => `${title}: ${counts?.get(key)}`)
                  .join(" · ")}
              </p>
              <ul className="my-3 max-h-[52dvh] list-none overflow-auto p-0">
                {state.rows.map((row) => {
                  const position = positionsByKey.get(previewPositionKey(row));
                  const disposition =
                    dispositionTitles[row.disposition] ??
                    copy.dispositions.unchecked;
                  const reason =
                    reasonTitles[row.reason ?? ""] ?? copy.reasons.withheld;
                  return (
                    <li
                      key={previewPositionKey(row)}
                      className="grid min-w-0 gap-1 border-b border-[var(--line)] py-2 text-sm wrap-anywhere"
                    >
                      {position && (
                        <span>
                          <strong>{position.title}</strong> ·{" "}
                          {position.release_title}
                        </span>
                      )}
                      <span>
                        {disposition}
                        {row.reason ? ` · ${reason}` : ""}
                      </span>
                      {row.resolved_path && <code>{row.resolved_path}</code>}
                      {row.path_clash && <span>{copy.conflict}</span>}
                    </li>
                  );
                })}
              </ul>
            </>
          )}
          <div className="mt-3 flex justify-end">
            <Button
              autoFocus
              className={librarySelectionButtonClass}
              onPress={onClose}
            >
              {copy.close}
            </Button>
          </div>
        </Dialog>
      </Modal>
    </ModalOverlay>
  );
}
