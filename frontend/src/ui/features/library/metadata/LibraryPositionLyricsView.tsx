import type { Locale } from "../../../i18n/types";
import { releaseControlButtonClass } from "../libraryReleaseControlStyles";

const copy = {
  en: {
    savedLyrics: "Saved lyrics",
    saveLyrics: "Save lyrics",
    saveExplicitlyEmpty: "Save as explicitly empty",
    applySavedLyrics: "Apply saved lyrics",
  },
  ru: {
    savedLyrics: "Сохранённый текст песни",
    saveLyrics: "Сохранить текст",
    saveExplicitlyEmpty: "Сохранить как явно пустой",
    applySavedLyrics: "Применить сохранённый текст",
  },
} as const;

export function LibraryPositionLyricsView({
  locale,
  value,
  onChange,
  saveDisabled,
  applyDisabled,
  onSave,
  onClear,
  onApply,
}: {
  locale: Locale;
  value: string;
  onChange: (value: string) => void;
  saveDisabled: boolean;
  applyDisabled: boolean;
  onSave: () => void;
  onClear: () => void;
  onApply: () => void;
}) {
  const text = copy[locale];
  return (
    <>
      <label className="block">
        {text.savedLyrics}
        <textarea
          className="mt-1 block min-h-40 w-full rounded border p-2"
          value={value}
          onChange={(event) => onChange(event.currentTarget.value)}
        />
      </label>
      <div className="flex flex-wrap gap-2">
        <button
          type="button"
          className={releaseControlButtonClass}
          disabled={saveDisabled}
          onClick={onSave}
        >
          {text.saveLyrics}
        </button>
        <button
          type="button"
          className={releaseControlButtonClass}
          disabled={saveDisabled}
          onClick={onClear}
        >
          {text.saveExplicitlyEmpty}
        </button>
        <button
          type="button"
          className={releaseControlButtonClass}
          disabled={applyDisabled}
          onClick={onApply}
        >
          {text.applySavedLyrics}
        </button>
      </div>
    </>
  );
}
