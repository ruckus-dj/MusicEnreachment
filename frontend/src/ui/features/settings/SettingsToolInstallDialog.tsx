import { useId, useLayoutEffect, useRef } from "react";
import { AppButton } from "../../components/AppButton";

const messages = {
  en: {
    title: (release: string) => `Download and verify ${release}`,
    note: "Downloading and verifying does not activate this version.",
    paths: "Target files",
    conflicts: "Conflicts to confirm",
    confirm: "Confirm listed conflicts",
    cancel: "Cancel",
  },
  ru: {
    title: (release: string) => `Скачать и проверить ${release}`,
    note: "Скачивание и проверка не активируют эту версию.",
    paths: "Целевые файлы",
    conflicts: "Конфликты для подтверждения",
    confirm: "Подтвердить перечисленные конфликты",
    cancel: "Отмена",
  },
} as const;

export function SettingsToolInstallDialog({
  locale,
  release,
  targets,
  conflicts,
  busy,
  confirmDisabled,
  returnFocusTo,
  error,
  onConfirm,
  onDismiss,
}: {
  locale: "en" | "ru";
  release: string;
  targets: readonly string[];
  conflicts: readonly string[];
  busy: boolean;
  confirmDisabled?: boolean;
  returnFocusTo?: HTMLElement | null;
  error?: string;
  onConfirm: () => void;
  onDismiss: () => void;
}) {
  const copy = messages[locale];
  const title = useId();
  const dialog = useRef<HTMLDialogElement>(null);
  const alert = useRef<HTMLParagraphElement>(null);

  useLayoutEffect(() => {
    const node = dialog.current;
    if (!node || node.open) return;
    node.showModal();
    node.querySelector<HTMLElement>("button:not(:disabled)")?.focus();
    return () => {
      if (node.open) node.close();
      if (returnFocusTo?.isConnected) returnFocusTo.focus();
    };
  }, [returnFocusTo]);
  useLayoutEffect(() => {
    if (error) alert.current?.focus();
  }, [error]);

  return (
    <dialog
      ref={dialog}
      aria-modal="true"
      aria-labelledby={title}
      className="m-auto max-h-[calc(100dvh-24px)] w-[min(620px,calc(100vw-24px))] gap-3 overflow-auto overscroll-contain rounded-md border border-[var(--line)] bg-[var(--surface)] p-4 text-sm text-[var(--ink)] shadow-[0_16px_48px_#0003] backdrop:bg-black/45 open:grid"
      onCancel={(event) => {
        event.preventDefault();
        event.stopPropagation();
        onDismiss();
      }}
      onKeyDown={(event) => {
        if (
          event.target instanceof Element &&
          event.target.closest("dialog") !== event.currentTarget
        )
          return;
        if (event.key === "Escape") {
          event.preventDefault();
          event.stopPropagation();
          onDismiss();
          return;
        }
        if (event.key !== "Tab") return;
        const controls = event.currentTarget.querySelectorAll<HTMLElement>(
          "button:not(:disabled)",
        );
        if (!controls.length) return;
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
      <h2 id={title} className="m-0 wrap-anywhere text-base font-semibold">
        {copy.title(release)}
      </h2>
      <p className="m-0">{copy.note}</p>
      <section>
        <h3 className="m-0 font-semibold">{copy.paths}</h3>
        <ul className="my-1 list-disc pl-5">
          {targets.map((target) => (
            <li key={target} className="wrap-anywhere">
              <code>{target}</code>
            </li>
          ))}
        </ul>
      </section>
      <section>
        <h3 className="m-0 font-semibold">{copy.conflicts}</h3>
        <ul className="my-1 list-disc pl-5">
          {conflicts.map((path) => (
            <li key={path} className="wrap-anywhere">
              <code>{path}</code>
            </li>
          ))}
        </ul>
      </section>
      {error && (
        <p
          ref={alert}
          role="alert"
          tabIndex={-1}
          className="m-0 wrap-anywhere text-[var(--error)]"
        >
          {error}
        </p>
      )}
      <div className="flex flex-wrap justify-end gap-2">
        <AppButton isDisabled={busy || confirmDisabled} onPress={onConfirm}>
          {copy.confirm}
        </AppButton>
        <AppButton isDisabled={busy} onPress={onDismiss}>
          {copy.cancel}
        </AppButton>
      </div>
    </dialog>
  );
}
