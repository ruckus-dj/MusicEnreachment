import { useQuery } from "@tanstack/react-query";
import { Button } from "react-aria-components";
import { previewLibraryPublicationSelection } from "../../api/generated/client";
import { useI18n } from "../../i18n/I18nProvider";
import {
  LibrarySelectionDialog,
  libraryPositionKey,
  librarySelectionButtonClass,
  maxLibraryPreviewPositions,
  type SelectedLibraryPosition,
} from "./LibrarySelectionDock";

const messages = {
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
};

export function LibrarySelectionPreview({
  positions,
  onClose,
}: {
  positions: SelectedLibraryPosition[];
  onClose: () => void;
}) {
  const { locale } = useI18n();
  const copy = messages[locale];
  const pairs = positions.map(({ release_id, position_id }) => ({
    release_id,
    position_id,
  }));
  const oversized = pairs.length > maxLibraryPreviewPositions;
  const preview = useQuery({
    queryKey: ["library-publication-selection-preview", pairs],
    enabled: !oversized,
    gcTime: 0,
    retry: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    queryFn: async ({ signal }) => {
      const response = await previewLibraryPublicationSelection(
        { positions: pairs },
        { signal },
      );
      if (response.status !== 200) throw new Error(copy.error);
      const results = response.data.items ?? [];
      if (
        results.length !== pairs.length ||
        results.some(
          (row, index) =>
            libraryPositionKey(row) !== libraryPositionKey(pairs[index]),
        )
      ) {
        throw new Error(copy.error);
      }
      return results;
    },
  });
  // The modal is unmounted on close, aborting the request and discarding its results.
  const rows =
    !preview.isFetching && !preview.isError ? preview.data : undefined;
  const counts = new Map<string, number>();
  for (const row of rows ?? [])
    counts.set(row.disposition, (counts.get(row.disposition) ?? 0) + 1);
  return (
    <LibrarySelectionDialog
      title={copy.title}
      isOpen
      onOpenChange={(open) => !open && onClose()}
    >
      <p className="my-2 text-sm text-[var(--muted)]">{copy.note}</p>
      {oversized ? (
        <p role="alert">{copy.limit}</p>
      ) : preview.isError ? (
        <div role="alert" className="my-3 space-y-2">
          <p>{copy.error}</p>
          <Button
            className={librarySelectionButtonClass}
            onPress={() => void preview.refetch()}
          >
            {copy.retry}
          </Button>
        </div>
      ) : !rows ? (
        <p role="status">{copy.loading}</p>
      ) : (
        <>
          <p role="status" className="my-2 font-medium">
            {Object.entries(copy.dispositions)
              .filter(([key]) => counts.has(key))
              .map(([key, title]) => `${title}: ${counts.get(key)}`)
              .join(" · ")}
          </p>
          <ul className="my-3 max-h-[52dvh] list-none overflow-auto p-0">
            {rows.map((row, index) => {
              const position = positions[index];
              const disposition =
                copy.dispositions[
                  row.disposition as keyof typeof copy.dispositions
                ] ?? copy.dispositions.unchecked;
              const reason =
                copy.reasons[row.reason as keyof typeof copy.reasons] ??
                copy.reasons.withheld;
              return (
                <li
                  key={libraryPositionKey(row)}
                  className="grid min-w-0 gap-1 border-b border-[var(--line)] py-2 text-sm wrap-anywhere"
                >
                  <span>
                    <strong>{position.title}</strong> · {position.release_title}
                  </span>
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
    </LibrarySelectionDialog>
  );
}
