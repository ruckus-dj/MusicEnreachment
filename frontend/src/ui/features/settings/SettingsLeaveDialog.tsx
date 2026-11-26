import type { RefObject } from "react";
import { useCallback, useId, useLayoutEffect, useRef } from "react";
import { AppButton } from "../../components/AppButton";
import type { Locale } from "../../i18n/types";
import { settingsMessage } from "./settingsMessages";

export type SettingsLeaveAction = {
  onPress: () => void;
  isDisabled?: boolean;
};

export type SettingsLeaveDialogProps = {
  locale: Locale;
  actions: {
    stay: SettingsLeaveAction;
    keepDraft: SettingsLeaveAction;
    discardDraft: SettingsLeaveAction;
    save: SettingsLeaveAction & { isHidden?: boolean };
  };
  error?: string;
  status?: string;
  dialogRef?: RefObject<HTMLDialogElement | null>;
};

const dialogClassName =
  "m-auto max-h-[calc(100dvh-24px)] w-[min(560px,calc(100vw-24px))] gap-3 overflow-auto rounded-md border border-[var(--line)] bg-[var(--surface)] p-4 text-sm text-[var(--ink)] shadow-[0_24px_64px_rgb(0_0_0/0.24)] backdrop:bg-[rgb(0_0_0/0.45)] open:grid";

const focusableSelector =
  'button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), a[href], [tabindex]:not([tabindex="-1"])';

export function SettingsLeaveDialog({
  locale,
  actions,
  error,
  status,
  dialogRef,
}: SettingsLeaveDialogProps) {
  const titleID = useId();
  const descriptionID = `${titleID}-description`;
  const dialog = useRef<HTMLDialogElement>(null);

  const assignDialog = useCallback(
    (node: HTMLDialogElement | null) => {
      dialog.current = node;
      if (dialogRef) dialogRef.current = node;
    },
    [dialogRef],
  );

  useLayoutEffect(() => {
    const current = dialog.current;
    if (!current || current.open) return;
    current.showModal();
    current.querySelector<HTMLElement>(focusableSelector)?.focus();
    return () => {
      if (current.open) current.close();
    };
  }, []);

  const stay = () => actions.stay.onPress();

  return (
    <dialog
      ref={assignDialog}
      aria-modal="true"
      aria-labelledby={titleID}
      aria-describedby={descriptionID}
      className={dialogClassName}
      onCancel={(event) => {
        event.preventDefault();
        event.stopPropagation();
        stay();
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
          stay();
          return;
        }
        if (event.key !== "Tab") return;
        const controls =
          event.currentTarget.querySelectorAll<HTMLElement>(focusableSelector);
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
      <h2 id={titleID} className="m-0 text-base font-semibold">
        {settingsMessage(locale, "unsavedSettings")}
      </h2>
      <p id={descriptionID} className="m-0">
        {settingsMessage(locale, "navigationPrompt")}
      </p>
      {error !== undefined && error !== "" && (
        <p role="alert" className="m-0 wrap-anywhere text-[var(--error)]">
          {error}
        </p>
      )}
      {status !== undefined && status !== "" && (
        <p role="status" className="m-0 wrap-anywhere">
          {status}
        </p>
      )}
      <div className="flex flex-wrap justify-end gap-2">
        <AppButton
          isDisabled={actions.stay.isDisabled}
          onPress={actions.stay.onPress}
        >
          {settingsMessage(locale, "stay")}
        </AppButton>
        <AppButton
          isDisabled={actions.keepDraft.isDisabled}
          onPress={actions.keepDraft.onPress}
        >
          {settingsMessage(locale, "leaveKeepDraft")}
        </AppButton>
        <AppButton
          isDisabled={actions.discardDraft.isDisabled}
          onPress={actions.discardDraft.onPress}
        >
          {settingsMessage(locale, "discardDraftLeave")}
        </AppButton>
        {!actions.save.isHidden && (
          <AppButton
            data-settings-navigation-save
            isDisabled={actions.save.isDisabled}
            onPress={actions.save.onPress}
          >
            {settingsMessage(locale, "saveLeave")}
          </AppButton>
        )}
      </div>
    </dialog>
  );
}
