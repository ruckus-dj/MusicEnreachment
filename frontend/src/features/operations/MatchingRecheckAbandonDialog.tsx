import {
  Dialog as AriaDialog,
  Heading as AriaHeading,
  Modal as AriaModal,
  ModalOverlay as AriaModalOverlay,
} from "react-aria-components";
import type { MatchingRecheckAbandonmentPosition } from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import type { Locale } from "../../i18n/locale";

interface MatchingRecheckAbandonCopy {
  title: string;
  description: string;
  positions: string;
  committed: string;
  unfinished: string;
  confirm: string;
  busy: string;
  cancel: string;
}

const copyByLocale = {
  en: {
    title: "Abandon unfinished republications?",
    description:
      "This closes the failed matching recheck and discards its unfinished republications. Installed files are not changed.",
    positions: "Previous paths and replacement status",
    committed: "replacement completed",
    unfinished: "unfinished",
    confirm: "Abandon republications",
    busy: "Abandoning…",
    cancel: "Cancel",
  },
  ru: {
    title: "Прервать незавершённые перепубликации?",
    description:
      "Неудачная проверка сопоставления будет закрыта, а её незавершённые перепубликации отброшены. Установленные файлы не изменяются.",
    positions: "Прежние пути и состояние замены",
    committed: "замена завершена",
    unfinished: "не завершена",
    confirm: "Прервать перепубликации",
    busy: "Прерывание…",
    cancel: "Отмена",
  },
} as const satisfies Record<Locale, MatchingRecheckAbandonCopy>;

/**
 * Confirms discarding the unfinished republications of one failed matching
 * recheck member. The caller loads the exact preview first and confirms the
 * revision it saw; a changed preview is rejected by the server.
 */
export function MatchingRecheckAbandonDialog({
  locale,
  positions,
  busy,
  error,
  onConfirm,
  onCancel,
}: {
  locale: Locale;
  positions: MatchingRecheckAbandonmentPosition[];
  busy: boolean;
  error?: string;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  const copy = copyByLocale[locale];
  return (
    <AriaModalOverlay
      isOpen
      isDismissable={!busy}
      isKeyboardDismissDisabled={busy}
      onOpenChange={(open) => {
        if (!open && !busy) onCancel();
      }}
      className="fixed inset-0 z-[100] grid place-items-center overflow-auto overscroll-contain bg-[#0008] p-3"
    >
      <AriaModal className="max-h-[calc(100dvh-24px)] w-full max-w-[620px] overflow-auto rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)] p-4 text-[var(--ink)] shadow-[0_16px_48px_#0003]">
        <AriaDialog className="outline-none">
          <AriaHeading
            slot="title"
            className="m-0 text-base leading-tight [text-wrap:balance]"
          >
            {copy.title}
          </AriaHeading>
          <p className="mt-2 mb-0 text-[13px] text-[var(--muted)]">
            {copy.description}
          </p>
          <h3 className="mt-3 mb-1 text-xs font-semibold">{copy.positions}</h3>
          <ul className="m-0 grid max-h-[240px] list-none gap-1 overflow-auto p-0 text-xs">
            {positions.map((position) => (
              <li
                key={position.operation_id}
                className="flex min-w-0 flex-wrap items-baseline justify-between gap-x-3 gap-y-0.5 border-b border-[var(--line)] pb-1 last:border-b-0"
              >
                <span className="min-w-0 [overflow-wrap:anywhere]">
                  {position.relative_path}
                </span>
                <span
                  className={
                    position.committed
                      ? "shrink-0 text-[11px] text-[var(--muted)]"
                      : "shrink-0 text-[11px] text-[var(--warning)]"
                  }
                >
                  {position.committed ? copy.committed : copy.unfinished}
                </span>
              </li>
            ))}
          </ul>
          {error && (
            <p
              role="alert"
              className="mt-3 mb-0 text-xs text-[var(--error)] [overflow-wrap:anywhere]"
            >
              {error}
            </p>
          )}
          <div className="mt-4 flex flex-wrap justify-end gap-2">
            <AppButton
              isDisabled={busy}
              className="font-semibold enabled:border-[var(--error)]! enabled:bg-[var(--error-soft)]! enabled:text-[var(--error)]!"
              onPress={onConfirm}
            >
              {busy ? copy.busy : copy.confirm}
            </AppButton>
            <AppButton isDisabled={busy} onPress={onCancel}>
              {copy.cancel}
            </AppButton>
          </div>
        </AriaDialog>
      </AriaModal>
    </AriaModalOverlay>
  );
}
