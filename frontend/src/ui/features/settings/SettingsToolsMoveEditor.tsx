import { type Ref, useState } from "react";
import { AppButton } from "../../components/AppButton";
import {
  type BrowseDirectory,
  DirectoryPickerView,
} from "../../components/DirectoryPickerView";

// Controlled move controls; the caller owns the draft, mode, and preflight.
const messages = {
  en: {
    title: "Move tools directory",
    newDirectory: "New tools directory",
    removeOld: "Remove old managed files after a successful move",
    chooseFolder: "Choose folder",
    checkMove: "Check move",
  },
  ru: {
    title: "Перенести каталог инструментов",
    newDirectory: "Новый каталог инструментов",
    removeOld: "Удалить прежние управляемые файлы после успешного переноса",
    chooseFolder: "Выбрать папку",
    checkMove: "Проверить перенос",
  },
} as const;

export function settingsToolsMoveTitle(locale: keyof typeof messages) {
  return messages[locale].title;
}

export type SettingsToolsMoveEditorProps = {
  locale: "en" | "ru";
  destination: {
    value: string;
    onChange: (value: string) => void;
    browse: BrowseDirectory;
    resolveInitialPath?: (value: string) => string;
  };
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
   * Preflight is separate from confirmation of the frozen review.
   */
  preflight: {
    disabled: boolean;
    onPress: (trigger: Element) => void;
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
  const [browseFrom, setBrowseFrom] = useState<string>();

  return (
    <section
      aria-labelledby="settings-tools-move-title"
      className={`${editorClass} max-w-2xl border-t border-[var(--line)] pt-3`}
    >
      <h3 id="settings-tools-move-title" className="m-0 font-semibold">
        {copy.title}
      </h3>
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
            onPress={() =>
              setBrowseFrom(
                destination.resolveInitialPath?.(destination.value) ??
                  destination.value,
              )
            }
          >
            {copy.chooseFolder}
          </AppButton>
        </div>
      </div>
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
      <div className={actionsClass}>
        <AppButton
          isDisabled={preflight.disabled}
          type="button"
          onPress={(event) => preflight.onPress(event.target)}
        >
          {copy.checkMove}
        </AppButton>
      </div>
      {browseFrom !== undefined && (
        <DirectoryPickerView
          open
          initialPath={browseFrom}
          locale={locale}
          browse={destination.browse}
          onSelect={destination.onChange}
          onClose={() => setBrowseFrom(undefined)}
        />
      )}
    </section>
  );
}
