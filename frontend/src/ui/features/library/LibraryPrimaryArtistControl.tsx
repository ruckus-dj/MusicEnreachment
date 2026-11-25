import type { Locale } from "../../i18n/types";
import {
  releaseControlAlertClass,
  releaseControlSelectClass,
} from "./libraryReleaseControlStyles";

const copy = {
  en: {
    release: "Release primary artist",
    track: "Track primary artist",
    notSelected: "Not selected",
  },
  ru: {
    release: "Основной исполнитель релиза",
    track: "Основной исполнитель композиции",
    notSelected: "Не выбран",
  },
} as const;

export type LibraryPrimaryArtistModel = {
  selectedId?: string | null;
  options: readonly { id: string; name: string }[];
  disabled?: boolean;
  error?: string;
  onChange: (id: string | null) => void;
};

export function LibraryPrimaryArtistControl({
  locale,
  positionNumber,
  model,
}: {
  locale: Locale;
  positionNumber?: string;
  model: LibraryPrimaryArtistModel;
}) {
  const text = copy[locale];
  const label = positionNumber === undefined ? text.release : text.track;
  return (
    <label className="block">
      {label}
      <select
        className={releaseControlSelectClass}
        aria-label={
          positionNumber === undefined ? label : `${label} ${positionNumber}`
        }
        value={model.selectedId || ""}
        disabled={model.disabled}
        onChange={(event) => model.onChange(event.currentTarget.value || null)}
      >
        <option value="">{text.notSelected}</option>
        {model.options.map(({ id, name }) => (
          <option key={id} value={id}>
            {name}
          </option>
        ))}
      </select>
      {model.error && (
        <p role="alert" className={releaseControlAlertClass}>
          {model.error}
        </p>
      )}
    </label>
  );
}
