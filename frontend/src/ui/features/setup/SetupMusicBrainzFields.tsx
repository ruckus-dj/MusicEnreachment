export type SetupMusicBrainzLocale = "en" | "ru";

/** Prototype default may stay empty so no mode is preselected. */
export type SetupMusicBrainzMode = "" | "public" | "self-hosted";

export interface SetupMusicBrainzFieldsProps {
  locale: SetupMusicBrainzLocale;
  mode: SetupMusicBrainzMode;
  /** Raw draft text; the parent owns persistence and verification. */
  baseURL: string;
  /** Only raised for an actual selection, never for the empty default. */
  onModeChange: (mode: "public" | "self-hosted") => void;
  onBaseURLChange: (value: string) => void;
}

type Copy = {
  group: string;
  description: string;
  public: string;
  selfHosted: string;
  baseURL: string;
};

const copy: Record<SetupMusicBrainzLocale, Copy> = {
  en: {
    group: "MusicBrainz",
    description: "Choose the MusicBrainz service used for metadata lookup.",
    public: "Public",
    selfHosted: "Self-hosted",
    baseURL: "MusicBrainz base URL",
  },
  ru: {
    group: "MusicBrainz",
    description: "Выберите сервис MusicBrainz для поиска метаданных.",
    public: "Публичный сервис",
    selfHosted: "Свой сервер",
    baseURL: "Базовый URL MusicBrainz",
  },
};

const options = [
  { value: "public", label: "public" },
  { value: "self-hosted", label: "selfHosted" },
] as const;

export function SetupMusicBrainzFields({
  locale,
  mode,
  baseURL,
  onModeChange,
  onBaseURLChange,
}: SetupMusicBrainzFieldsProps) {
  const text = copy[locale];
  const baseURLId = "setup-musicbrainz-base-url";
  return (
    <fieldset className="m-0 flex min-w-0 flex-col gap-3 border-0 p-0">
      <legend className="p-0 text-sm font-semibold text-[var(--ink)]">
        {text.group}
      </legend>
      <p className="m-0 text-sm text-[var(--muted)]">{text.description}</p>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        {options.map((option) => {
          const id = `setup-musicbrainz-mode-${option.value}`;
          return (
            <label
              key={option.value}
              htmlFor={id}
              className="flex min-h-8 cursor-pointer items-center gap-2 text-sm text-[var(--ink)] pointer-coarse:min-h-10"
            >
              <input
                id={id}
                type="radio"
                name="setup-musicbrainz-mode"
                value={option.value}
                checked={mode === option.value}
                onChange={() => onModeChange(option.value)}
                className="size-4 accent-[var(--accent)] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--focus)]"
              />
              <span>{text[option.label]}</span>
            </label>
          );
        })}
      </div>
      {mode === "self-hosted" && (
        <div className="flex flex-col gap-1">
          <label
            htmlFor={baseURLId}
            className="text-sm font-medium text-[var(--ink)]"
          >
            {text.baseURL}
          </label>
          <input
            id={baseURLId}
            type="text"
            inputMode="url"
            value={baseURL}
            onChange={(event) => onBaseURLChange(event.target.value)}
            autoComplete="off"
            spellCheck={false}
            className="min-h-9 w-full min-w-0 rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 py-1 text-sm text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--focus)] pointer-coarse:min-h-10"
          />
        </div>
      )}
    </fieldset>
  );
}
