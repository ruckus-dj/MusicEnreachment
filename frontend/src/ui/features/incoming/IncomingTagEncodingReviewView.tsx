import { useId } from "react";
import { AppButton } from "../../components/AppButton";
import type { Locale } from "../../i18n/types";

const copy = {
  en: {
    title: "Tag encoding",
    note: "Changes are saved in MeloTrove. Source audio files are not changed.",
    loading: "Reading captured tags…",
    loadFailed: "Could not read encoding corrections.",
    applyFailed: "Could not save encoding corrections.",
    stale: "The group or its tags changed. Reload the preview before applying.",
    reload: "Reload preview",
    empty: "No encoding corrections for this group.",
    before: "Original",
    after: "Readable",
    saved: "Saved",
    proposed: "Proposed",
    apply: "Apply corrections to group",
    undo: "Undo saved corrections",
    done: "Tag encoding updated.",
  },
  ru: {
    title: "Кодировка тегов",
    note: "Поправки сохраняются в MeloTrove. Исходные аудиофайлы не меняются.",
    loading: "Читаем сохранённые теги…",
    loadFailed: "Не удалось прочитать поправки кодировки.",
    applyFailed: "Не удалось сохранить поправки кодировки.",
    stale:
      "Группа или теги изменились. Обновите предпросмотр перед применением.",
    reload: "Обновить предпросмотр",
    empty: "Для этой группы нет поправок кодировки.",
    before: "Исходное",
    after: "Читаемое",
    saved: "Сохранено",
    proposed: "Предложено",
    apply: "Применить исправления к группе",
    undo: "Отменить сохранённые исправления",
    done: "Кодировка тегов обновлена.",
  },
} as const;

export type IncomingTagEncodingReviewEntry = {
  readonly id: string;
  readonly title: string;
  readonly key: string;
  readonly saved: boolean;
  readonly original: string;
  readonly replacement: string;
};

export type IncomingTagEncodingReviewFailure = {
  readonly reason: "stale" | "load" | "apply";
  readonly canReload: boolean;
  readonly reloading: boolean;
};

export type IncomingTagEncodingReviewViewProps = {
  locale: Locale;
  open: boolean;
  loading: boolean;
  failure?: IncomingTagEncodingReviewFailure;
  done: boolean;
  available: boolean;
  entries: readonly IncomingTagEncodingReviewEntry[];
  canApply: boolean;
  canUndo: boolean;
  blocked: boolean;
  onToggle: (open: boolean) => void;
  onReload: () => void;
  onApply: () => void;
  onUndo: () => void;
};

export function IncomingTagEncodingReviewView({
  locale,
  open,
  loading,
  failure,
  done,
  available,
  entries,
  canApply,
  canUndo,
  blocked,
  onToggle,
  onReload,
  onApply,
  onUndo,
}: IncomingTagEncodingReviewViewProps) {
  const text = copy[locale];
  const regionId = useId();
  return (
    <details
      className="rounded border border-[var(--line)] px-3 py-2"
      onToggle={(event) => onToggle(event.currentTarget.open)}
    >
      <summary
        className="cursor-pointer text-sm font-medium"
        aria-controls={regionId}
      >
        {text.title}
      </summary>
      {open ? (
        <div className="mt-2 space-y-3 text-sm" id={regionId}>
          <p className="text-[var(--muted)]">{text.note}</p>
          {loading ? <p role="status">{text.loading}</p> : null}
          {failure ? (
            <p role="alert">
              {failure.reason === "stale"
                ? text.stale
                : failure.reason === "load"
                  ? text.loadFailed
                  : text.applyFailed}
            </p>
          ) : null}
          {failure?.canReload ? (
            <AppButton isDisabled={failure.reloading} onPress={onReload}>
              {text.reload}
            </AppButton>
          ) : null}
          {done ? <p role="status">{text.done}</p> : null}
          {entries.length > 0 ? (
            <>
              <ul className="max-h-72 space-y-2 overflow-y-auto overscroll-contain">
                {entries.map((entry) => (
                  <li
                    className="border-b border-[var(--line)] pb-2"
                    key={entry.id}
                  >
                    <p className="wrap-anywhere font-medium">
                      {entry.title} · {entry.key}
                      <span className="ml-2 font-normal text-[var(--muted)]">
                        {entry.saved ? text.saved : text.proposed}
                      </span>
                    </p>
                    <dl className="mt-1 grid gap-x-3 gap-y-1 wrap-anywhere sm:grid-cols-2">
                      <div>
                        <dt className="text-[var(--muted)]">{text.before}</dt>
                        <dd>{entry.original}</dd>
                      </div>
                      <div>
                        <dt className="text-[var(--muted)]">{text.after}</dt>
                        <dd>{entry.replacement}</dd>
                      </div>
                    </dl>
                  </li>
                ))}
              </ul>
              <div className="flex flex-wrap gap-2">
                <AppButton isDisabled={blocked || !canApply} onPress={onApply}>
                  {text.apply}
                </AppButton>
                <AppButton isDisabled={blocked || !canUndo} onPress={onUndo}>
                  {text.undo}
                </AppButton>
              </div>
            </>
          ) : available ? (
            <p>{text.empty}</p>
          ) : null}
        </div>
      ) : null}
    </details>
  );
}
