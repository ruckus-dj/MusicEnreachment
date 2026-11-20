import type { Ref } from "react";
import { AppButton } from "../../components/AppButton";
import { DirectoryPathFieldView } from "../../components/DirectoryPathFieldView";
import type { BrowseDirectory } from "../../components/DirectoryPickerView";

// Controlled move controls; the caller owns the draft, mode, and preflight.
const messages = {
  en: {
    newDirectory: "New tools directory",
    removeOld: "Remove old managed files after a successful move",
    chooseFolder: "Choose folder",
    checkMove: "Check move",
    cancel: "Cancel",
  },
  ru: {
    newDirectory: "Новый каталог инструментов",
    removeOld: "Удалить прежние управляемые файлы после успешного переноса",
    chooseFolder: "Выбрать папку",
    checkMove: "Проверить перенос",
    cancel: "Отмена",
  },
} as const;

export type SettingsToolsMoveDestination =
  | {
      /** Read-only path plus the shared directory picker. */
      kind: "picker";
      value: string;
      onSelect: (path: string) => void;
      browse: BrowseDirectory;
    }
  | {
      /** Editable path plus a caller-provided browse action. */
      kind: "editable";
      value: string;
      onChange: (value: string) => void;
      onBrowse: () => void;
    };

export type SettingsToolsMoveEditorProps = {
  locale: "en" | "ru";
  destination: SettingsToolsMoveDestination;
  /** Omit when the remove-old option does not apply. */
  removeOld?: {
    checked: boolean;
    onChange: (checked: boolean) => void;
  };
  /** Already-formatted, user-safe failure text. */
  error?: string;
  /** Focus target for the alert; the host focuses it when the error appears. */
  errorRef?: Ref<HTMLParagraphElement>;
  /**
   * Pre-review actions. Omit after a successful preflight so the review body
   * owns the next action row.
   */
  preflight?: {
    disabled: boolean;
    onPress: () => void;
    onCancel?: () => void;
  };
};

const editorClass = "grid min-w-0 gap-3 text-sm text-[var(--ink)]";
/** Existing application field id, retained for tests and label wiring. */
const fieldId = "settings-move-tools-directory";
const focus =
  "focus-visible:relative focus-visible:z-10 focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-[var(--focus)]";
const inputClass = `${focus} min-h-9 w-full min-w-0 rounded-[5px] border border-[var(--control)] bg-[var(--surface)] px-[9px] py-1.5 text-[var(--ink)] max-[599px]:min-h-10`;
const browseRowClass = "flex min-w-0 gap-2 max-[599px]:grid";
const checkboxRowClass = "flex cursor-pointer items-start gap-2";
const checkboxClass = "mt-0.5 size-4 flex-none accent-[var(--accent)]";
const errorClass =
  "m-0 wrap-anywhere rounded border border-[var(--error)] bg-[var(--error-soft)] px-3 py-2 text-[var(--error)]";
const actionsClass = "flex flex-wrap justify-end gap-2";

export function SettingsToolsMoveEditor({
  locale,
  destination,
  removeOld,
  error,
  errorRef,
  preflight,
}: SettingsToolsMoveEditorProps) {
  const copy = messages[locale];

  return (
    <div className={editorClass}>
      {destination.kind === "picker" ? (
        <DirectoryPathFieldView
          id={fieldId}
          label={copy.newDirectory}
          value={destination.value}
          onSelect={destination.onSelect}
          browse={destination.browse}
          locale={locale}
        />
      ) : (
        <div className="grid min-w-0 gap-1">
          <label className="font-semibold" htmlFor={fieldId}>
            {copy.newDirectory}
          </label>
          <div className={browseRowClass}>
            <input
              className={inputClass}
              id={fieldId}
              type="text"
              autoComplete="off"
              spellCheck={false}
              value={destination.value}
              onChange={(event) =>
                destination.onChange(event.currentTarget.value)
              }
            />
            <AppButton
              className="flex-none"
              type="button"
              onPress={destination.onBrowse}
            >
              {copy.chooseFolder}
            </AppButton>
          </div>
        </div>
      )}
      {removeOld && (
        <label className={checkboxRowClass}>
          <input
            className={checkboxClass}
            type="checkbox"
            checked={removeOld.checked}
            onChange={(event) =>
              removeOld.onChange(event.currentTarget.checked)
            }
          />
          <span>{copy.removeOld}</span>
        </label>
      )}
      {error && (
        <p className={errorClass} ref={errorRef} role="alert" tabIndex={-1}>
          {error}
        </p>
      )}
      {preflight && (
        <div className={actionsClass}>
          <AppButton
            isDisabled={preflight.disabled}
            type="button"
            onPress={preflight.onPress}
          >
            {copy.checkMove}
          </AppButton>
          {preflight.onCancel && (
            <AppButton
              isDisabled={preflight.disabled}
              type="button"
              onPress={preflight.onCancel}
            >
              {copy.cancel}
            </AppButton>
          )}
        </div>
      )}
    </div>
  );
}
