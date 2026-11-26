import { useId, useLayoutEffect, useRef } from "react";
import { AppButton } from "../../components/AppButton";

const copyByLocale = {
  en: {
    title: "Change publication directory?",
    warning:
      "Changing the output directory cancels queued operations and clears the application's references to previous files. The previous files themselves are not deleted.",
    running:
      "Wait for running operations to finish before changing the publication directory.",
    confirm: "Confirm output directory change",
    cancel: "Cancel",
  },
  ru: {
    title: "Сменить каталог публикации?",
    warning:
      "Изменение каталога отменит все операции в очереди и очистит ссылки приложения на прежние файлы. Сами файлы не удаляются.",
    running:
      "Дождитесь завершения выполняющихся операций, прежде чем менять каталог публикации.",
    confirm: "Подтвердить смену каталога публикации",
    cancel: "Отмена",
  },
} as const;

export type OutputResetFeedback =
  | { kind: "running" }
  | { kind: "error"; detail: string };

export function SettingsOutputResetDialog({
  locale,
  busy,
  feedback,
  onConfirm,
  onDismiss,
}: {
  locale: "en" | "ru";
  busy: boolean;
  feedback?: OutputResetFeedback;
  onConfirm: () => void;
  onDismiss: () => void;
}) {
  const copy = copyByLocale[locale];
  const title = useId();
  const dialog = useRef<HTMLDialogElement>(null);
  const alert = useRef<HTMLParagraphElement>(null);
  const feedbackText =
    feedback?.kind === "running" ? copy.running : feedback?.detail;

  useLayoutEffect(() => {
    const current = dialog.current;
    if (!current || current.open) return;
    current.showModal();
    current.querySelector<HTMLElement>("button:not(:disabled)")?.focus();
    return () => {
      if (current.open) current.close();
    };
  }, []);
  useLayoutEffect(() => {
    if (feedbackText !== undefined) alert.current?.focus();
  }, [feedbackText]);

  const dismiss = () => {
    if (!busy) onDismiss();
  };
  return (
    <dialog
      ref={dialog}
      aria-modal="true"
      aria-labelledby={title}
      aria-describedby={`${title}-warning`}
      className="m-auto max-h-[calc(100dvh-24px)] w-[min(560px,calc(100vw-24px))] gap-3 overflow-auto rounded-md border border-[var(--line)] bg-[var(--surface)] p-4 text-sm text-[var(--ink)] shadow-[0_24px_64px_rgb(0_0_0/0.24)] backdrop:bg-[rgb(0_0_0/0.45)] open:grid"
      onCancel={(event) => {
        event.preventDefault();
        event.stopPropagation();
        dismiss();
      }}
      onKeyDown={(event) => {
        if (
          event.target instanceof Element &&
          event.target.closest("dialog") !== event.currentTarget
        ) {
          return;
        }
        if (event.key === "Escape") {
          event.preventDefault();
          event.stopPropagation();
          dismiss();
          return;
        }
        if (event.key !== "Tab") return;
        const controls = event.currentTarget.querySelectorAll<HTMLElement>(
          "button:not(:disabled)",
        );
        if (controls.length === 0) return;
        const first = controls[0];
        const last = controls[controls.length - 1];
        if (event.shiftKey && document.activeElement === first) {
          event.preventDefault();
          last.focus();
        } else if (!event.shiftKey && document.activeElement === last) {
          event.preventDefault();
          first.focus();
        } else if (!event.currentTarget.contains(document.activeElement)) {
          event.preventDefault();
          (event.shiftKey ? last : first).focus();
        }
      }}
    >
      <h2 id={title} className="m-0 text-base font-semibold">
        {copy.title}
      </h2>
      <p id={`${title}-warning`} className="m-0">
        {copy.warning}
      </p>
      {feedbackText !== undefined && (
        <p
          ref={alert}
          tabIndex={-1}
          role="alert"
          className="m-0 wrap-anywhere text-[var(--error)]"
        >
          {feedbackText}
        </p>
      )}
      <div className="flex flex-wrap justify-end gap-2">
        <AppButton
          isDisabled={busy}
          className="font-semibold enabled:border-[var(--error)]! enabled:bg-[var(--error-soft)]! enabled:text-[var(--error)]!"
          onPress={onConfirm}
        >
          {copy.confirm}
        </AppButton>
        <AppButton isDisabled={busy} onPress={dismiss}>
          {copy.cancel}
        </AppButton>
      </div>
    </dialog>
  );
}
