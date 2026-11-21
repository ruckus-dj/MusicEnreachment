import type { RefObject } from "react";
import { useCallback, useId, useLayoutEffect, useRef } from "react";
import { AppButton } from "../../components/AppButton";

export type SettingsLeaveAction = {
  label: string;
  onPress: () => void;
  isDisabled?: boolean;
};

export type SettingsLeaveDialogProps = {
  title: string;
  description: string;
  actions: {
    stay: SettingsLeaveAction;
    keepDraft: SettingsLeaveAction;
    discardDraft: SettingsLeaveAction;
    save: SettingsLeaveAction & { isHidden?: boolean };
  };
  error?: string;
  status?: string;
  dismissOnBackdrop?: boolean;
  dialogRef?: RefObject<HTMLDialogElement | null>;
};

const dialogClassName =
  "m-auto max-h-[calc(100dvh-24px)] w-[min(560px,calc(100vw-24px))] gap-3 overflow-auto rounded-md border border-[var(--line)] bg-[var(--surface)] p-4 text-sm text-[var(--ink)] shadow-[0_16px_48px_#0003] backdrop:bg-black/45 open:grid";

const focusableSelector =
  'button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), a[href], [tabindex]:not([tabindex="-1"])';

export function SettingsLeaveDialog({
  title,
  description,
  actions,
  error,
  status,
  dismissOnBackdrop,
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
      onClick={(event) => {
        if (!dismissOnBackdrop || event.target !== event.currentTarget) return;
        const { left, right, top, bottom } =
          event.currentTarget.getBoundingClientRect();
        if (
          event.clientX < left ||
          event.clientX >= right ||
          event.clientY < top ||
          event.clientY >= bottom
        )
          stay();
      }}
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
        {title}
      </h2>
      <p id={descriptionID} className="m-0">
        {description}
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
          {actions.stay.label}
        </AppButton>
        <AppButton
          isDisabled={actions.keepDraft.isDisabled}
          onPress={actions.keepDraft.onPress}
        >
          {actions.keepDraft.label}
        </AppButton>
        <AppButton
          isDisabled={actions.discardDraft.isDisabled}
          onPress={actions.discardDraft.onPress}
        >
          {actions.discardDraft.label}
        </AppButton>
        {!actions.save.isHidden && (
          <AppButton
            data-settings-navigation-save
            isDisabled={actions.save.isDisabled}
            onPress={actions.save.onPress}
          >
            {actions.save.label}
          </AppButton>
        )}
      </div>
    </dialog>
  );
}
