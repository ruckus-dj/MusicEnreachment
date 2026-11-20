import { AppButton } from "../../components/AppButton";

const english = {
  title: "Metadata providers",
  autosaveHelp:
    "Valid mode changes and the LRCLIB switch save automatically. For a new self-hosted server, save and check MusicBrainz first. On an already saved server, the throttling switch saves automatically, while the URL and request interval require Save and check.",
  acoustIDTitle: "AcoustID",
  acoustIDKey: "AcoustID application key",
  keySaved: "The key is saved. Its value cannot be read from settings.",
  keyMissing: "No key is set; AcoustID lookup is unavailable.",
  saveAcoustIDKey: "Save AcoustID key",
  deleteAcoustIDKey: "Delete AcoustID key",
  musicBrainzTitle: "MusicBrainz",
  musicBrainzMode: "MusicBrainz mode",
  publicService: "Public",
  selfHosted: "Self-hosted",
  musicBrainzBaseURL: "MusicBrainz base URL",
  throttleMusicBrainz: "Throttle requests to self-hosted MusicBrainz",
  requestInterval: "Request interval (seconds)",
  lastCheck: "Last check:",
  notChecked: "not checked",
  checked: "verified",
  selectMode: "Choose a mode",
  invalidMode: "Choose a MusicBrainz mode.",
  invalidBaseURL: "Enter a valid HTTP(S) URL.",
  invalidDelay: "Enter a request interval from 0 to 60 seconds.",
  unsavedMusicBrainz: "MusicBrainz has unsaved changes.",
  saveAndCheckMusicBrainz: "Save and check MusicBrainz",
  lrclibTitle: "LRCLIB",
  lrclibEnabled: "LRCLIB is enabled",
  lrclibHelp:
    "When off, no new lyric requests are made. Saved lyrics and current publications are unchanged.",
  unsavedLrclib: "LRCLIB has unsaved changes.",
} as const;

type SettingsProvidersCopy = Record<keyof typeof english, string>;

const russian: SettingsProvidersCopy = {
  title: "Провайдеры метаданных",
  autosaveHelp:
    "Допустимый режим MusicBrainz и переключатель LRCLIB сохраняются автоматически. Новый собственный сервер нужно сохранить и проверить. Для уже сохранённого сервера ограничение частоты сохраняется автоматически, а URL и интервал запросов — вместе с проверкой.",
  acoustIDTitle: "AcoustID",
  acoustIDKey: "Ключ приложения AcoustID",
  keySaved: "Ключ сохранён. Его значение нельзя прочитать из настроек.",
  keyMissing: "Ключ не задан; поиск AcoustID недоступен.",
  saveAcoustIDKey: "Сохранить ключ AcoustID",
  deleteAcoustIDKey: "Удалить ключ AcoustID",
  musicBrainzTitle: "MusicBrainz",
  musicBrainzMode: "Режим MusicBrainz",
  publicService: "Публичный",
  selfHosted: "Собственный сервер",
  musicBrainzBaseURL: "Базовый URL MusicBrainz",
  throttleMusicBrainz:
    "Ограничивать частоту запросов к собственному MusicBrainz",
  requestInterval: "Интервал запросов (секунды)",
  lastCheck: "Последняя проверка:",
  notChecked: "не проверено",
  checked: "проверено",
  selectMode: "Выберите режим",
  invalidMode: "Выберите режим MusicBrainz.",
  invalidBaseURL: "Введите корректный URL HTTP(S).",
  invalidDelay: "Введите интервал запросов от 0 до 60 секунд.",
  unsavedMusicBrainz: "Есть несохранённые изменения MusicBrainz.",
  saveAndCheckMusicBrainz: "Сохранить и проверить MusicBrainz",
  lrclibTitle: "LRCLIB",
  lrclibEnabled: "LRCLIB включён",
  lrclibHelp:
    "Когда выключено, новые запросы текстов не выполняются. Сохранённые тексты и текущие публикации не меняются.",
  unsavedLrclib: "Есть несохранённые изменения LRCLIB.",
};

export type SettingsProvidersLocale = "en" | "ru";

export type MusicBrainzMode = "public" | "self-hosted";

export interface SettingsProvidersErrors {
  musicBrainzMode?: boolean;
  musicBrainzBaseURL?: boolean;
  musicBrainzDelaySeconds?: boolean;
}

export interface SettingsProvidersSnapshot {
  busy: boolean;
  acoustID: {
    /** Write-only draft. A saved key is never read back into this value. */
    applicationKey: string;
    hasKey: boolean;
  };
  musicBrainz: {
    mode: MusicBrainzMode | "";
    /** Raw draft text, saved with the explicit check action. */
    baseURL: string;
    /** Autosaved by the parent when changed. */
    throttle: boolean;
    throttleDisabled?: boolean;
    /** Raw draft text, saved with the explicit check action. */
    delaySeconds: string;
    /** Server-provided value; empty means the configuration was not checked. */
    verifiedAt: string | null;
    checked?: boolean;
    checkDisabled?: boolean;
    dirty: boolean;
  };
  lrclib: {
    enabled: boolean;
    dirty: boolean;
  };
  errors?: SettingsProvidersErrors;
}

export interface SettingsProvidersActions {
  onAcoustIDApplicationKeyChange: (value: string) => void;
  onSaveAcoustIDApplicationKey: () => void;
  onDeleteAcoustIDApplicationKey: () => void;
  onMusicBrainzModeChange: (mode: MusicBrainzMode) => void;
  onMusicBrainzBaseURLChange: (value: string) => void;
  onMusicBrainzThrottleChange: (enabled: boolean) => void;
  onMusicBrainzDelaySecondsChange: (value: string) => void;
  onSaveAndCheckMusicBrainz: () => void;
  onLrclibEnabledChange: (enabled: boolean) => void;
}

export interface SettingsProvidersSectionProps {
  locale: SettingsProvidersLocale;
  snapshot: SettingsProvidersSnapshot;
  actions: SettingsProvidersActions;
}

const messages: Record<SettingsProvidersLocale, SettingsProvidersCopy> = {
  en: english,
  ru: russian,
};

const fieldIds = {
  acoustIDApplicationKey: "settings-acoustid-key",
  acoustIDApplicationKeyHelp: "settings-acoustid-key-help",
  musicBrainzMode: "settings-musicBrainzMode",
  musicBrainzModeError: "settings-musicBrainzMode-error",
  musicBrainzBaseURL: "settings-musicBrainzURL",
  musicBrainzBaseURLError: "settings-musicBrainzURL-error",
  musicBrainzDelaySeconds: "settings-musicBrainzDelay",
  musicBrainzDelaySecondsError: "settings-musicBrainzDelay-error",
  musicBrainzThrottle: "settings-musicBrainzThrottle",
  autosaveHelp: "settings-providers-autosave-help",
  lrclibEnabled: "settings-lrclib-enabled",
  lrclibHelp: "settings-lrclib-help",
} as const;

const styles = {
  panel:
    "grid min-w-0 max-w-3xl content-start gap-4 rounded-md border border-[var(--line)] bg-[var(--surface)] p-3 text-sm",
  intro: "grid gap-1",
  heading: "m-0 text-sm font-semibold",
  group: "grid min-w-0 content-start gap-2 border-t border-[var(--line)] pt-3",
  groupHeading: "m-0 text-sm font-semibold",
  field: "grid min-w-0 gap-[5px] text-xs font-semibold text-[var(--ink)]",
  input:
    "min-h-8 w-full min-w-0 rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 py-[5px] font-normal text-[var(--ink)] aria-[invalid=true]:border-[var(--error)] focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2",
  select:
    "min-h-8 w-full min-w-0 cursor-pointer rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 py-[5px] font-normal text-[var(--ink)] aria-[invalid=true]:border-[var(--error)] focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2",
  checkboxLabel:
    "flex min-w-0 items-start gap-2 text-xs font-normal text-[var(--ink)]",
  checkbox:
    "mt-0.5 size-4 flex-none cursor-pointer accent-[var(--accent)] focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2",
  help: "m-0 text-xs font-normal text-[var(--muted)]",
  error: "m-0 text-xs font-normal text-[var(--error)]",
  actions: "flex flex-wrap gap-2",
} as const;

function describedBy(...ids: Array<string | undefined>) {
  return ids.filter(Boolean).join(" ") || undefined;
}

export function SettingsProvidersSection({
  locale,
  snapshot,
  actions,
}: SettingsProvidersSectionProps) {
  const copy = messages[locale];
  const errors = snapshot.errors;
  const { acoustID, musicBrainz, lrclib } = snapshot;
  return (
    <section
      aria-labelledby="settings-providers-title"
      className={styles.panel}
    >
      <div className={styles.intro}>
        <h2 id="settings-providers-title" className={styles.heading}>
          {copy.title}
        </h2>
        <p id={fieldIds.autosaveHelp} className={styles.help}>
          {copy.autosaveHelp}
        </p>
      </div>

      <section
        aria-labelledby="settings-acoustid-title"
        className={styles.group}
      >
        <h3 id="settings-acoustid-title" className={styles.groupHeading}>
          {copy.acoustIDTitle}
        </h3>
        <div className={styles.field}>
          <label htmlFor={fieldIds.acoustIDApplicationKey}>
            {copy.acoustIDKey}
          </label>
          <input
            id={fieldIds.acoustIDApplicationKey}
            type="password"
            autoComplete="new-password"
            maxLength={4096}
            value={acoustID.applicationKey}
            onChange={(event) =>
              actions.onAcoustIDApplicationKeyChange(event.currentTarget.value)
            }
            aria-describedby={fieldIds.acoustIDApplicationKeyHelp}
            className={styles.input}
          />
        </div>
        <p id={fieldIds.acoustIDApplicationKeyHelp} className={styles.help}>
          {acoustID.hasKey ? copy.keySaved : copy.keyMissing}
        </p>
        <div className={styles.actions}>
          <AppButton
            isDisabled={snapshot.busy || acoustID.applicationKey.length === 0}
            onPress={actions.onSaveAcoustIDApplicationKey}
          >
            {copy.saveAcoustIDKey}
          </AppButton>
          {acoustID.hasKey && (
            <AppButton
              isDisabled={snapshot.busy}
              onPress={actions.onDeleteAcoustIDApplicationKey}
            >
              {copy.deleteAcoustIDKey}
            </AppButton>
          )}
        </div>
      </section>

      <section
        aria-labelledby="settings-musicbrainz-title"
        className={styles.group}
      >
        <h3 id="settings-musicbrainz-title" className={styles.groupHeading}>
          {copy.musicBrainzTitle}
        </h3>
        <div className={styles.field}>
          <label htmlFor={fieldIds.musicBrainzMode}>
            {copy.musicBrainzMode}
          </label>
          <select
            id={fieldIds.musicBrainzMode}
            value={musicBrainz.mode}
            onChange={(event) => {
              const mode = event.currentTarget.value;
              if (mode === "public" || mode === "self-hosted")
                actions.onMusicBrainzModeChange(mode);
            }}
            aria-invalid={Boolean(errors?.musicBrainzMode)}
            aria-describedby={describedBy(
              fieldIds.autosaveHelp,
              errors?.musicBrainzMode
                ? fieldIds.musicBrainzModeError
                : undefined,
            )}
            className={styles.select}
          >
            {musicBrainz.mode === "" && (
              <option value="" disabled>
                {copy.selectMode}
              </option>
            )}
            <option value="public">{copy.publicService}</option>
            <option value="self-hosted">{copy.selfHosted}</option>
          </select>
        </div>
        {errors?.musicBrainzMode && (
          <p
            id={fieldIds.musicBrainzModeError}
            className={styles.error}
            role="alert"
          >
            {copy.invalidMode}
          </p>
        )}
        {musicBrainz.mode === "self-hosted" && (
          <>
            <div className={styles.field}>
              <label htmlFor={fieldIds.musicBrainzBaseURL}>
                {copy.musicBrainzBaseURL}
              </label>
              <input
                id={fieldIds.musicBrainzBaseURL}
                type="text"
                value={musicBrainz.baseURL}
                onChange={(event) =>
                  actions.onMusicBrainzBaseURLChange(event.currentTarget.value)
                }
                aria-invalid={Boolean(errors?.musicBrainzBaseURL)}
                aria-describedby={
                  errors?.musicBrainzBaseURL
                    ? fieldIds.musicBrainzBaseURLError
                    : undefined
                }
                className={styles.input}
              />
            </div>
            {errors?.musicBrainzBaseURL && (
              <p
                id={fieldIds.musicBrainzBaseURLError}
                className={styles.error}
                role="alert"
              >
                {copy.invalidBaseURL}
              </p>
            )}
            <label
              htmlFor={fieldIds.musicBrainzThrottle}
              className={styles.checkboxLabel}
            >
              <input
                id={fieldIds.musicBrainzThrottle}
                type="checkbox"
                checked={musicBrainz.throttle}
                disabled={musicBrainz.throttleDisabled}
                onChange={(event) =>
                  actions.onMusicBrainzThrottleChange(
                    event.currentTarget.checked,
                  )
                }
                aria-describedby={fieldIds.autosaveHelp}
                className={styles.checkbox}
              />
              <span>{copy.throttleMusicBrainz}</span>
            </label>
            <div className={styles.field}>
              <label htmlFor={fieldIds.musicBrainzDelaySeconds}>
                {copy.requestInterval}
              </label>
              <input
                id={fieldIds.musicBrainzDelaySeconds}
                type="text"
                inputMode="decimal"
                min="0"
                max="60"
                step="any"
                required
                value={musicBrainz.delaySeconds}
                onChange={(event) =>
                  actions.onMusicBrainzDelaySecondsChange(
                    event.currentTarget.value,
                  )
                }
                aria-invalid={Boolean(errors?.musicBrainzDelaySeconds)}
                aria-describedby={
                  errors?.musicBrainzDelaySeconds
                    ? fieldIds.musicBrainzDelaySecondsError
                    : undefined
                }
                className={styles.input}
              />
            </div>
            {errors?.musicBrainzDelaySeconds && (
              <p
                id={fieldIds.musicBrainzDelaySecondsError}
                className={styles.error}
                role="alert"
              >
                {copy.invalidDelay}
              </p>
            )}
          </>
        )}
        <p className={styles.help}>
          {copy.lastCheck}{" "}
          {musicBrainz.verifiedAt ? (
            <time dateTime={musicBrainz.verifiedAt}>
              {musicBrainz.verifiedAt}
            </time>
          ) : musicBrainz.checked ? (
            copy.checked
          ) : (
            copy.notChecked
          )}
        </p>
        {musicBrainz.dirty && (
          <p className={styles.help}>{copy.unsavedMusicBrainz}</p>
        )}
        <div className={styles.actions}>
          <AppButton
            isDisabled={snapshot.busy || musicBrainz.checkDisabled}
            onPress={actions.onSaveAndCheckMusicBrainz}
          >
            {copy.saveAndCheckMusicBrainz}
          </AppButton>
        </div>
      </section>

      <section aria-labelledby="settings-lrclib-title" className={styles.group}>
        <h3 id="settings-lrclib-title" className={styles.groupHeading}>
          {copy.lrclibTitle}
        </h3>
        <label
          htmlFor={fieldIds.lrclibEnabled}
          className={styles.checkboxLabel}
        >
          <input
            id={fieldIds.lrclibEnabled}
            type="checkbox"
            checked={lrclib.enabled}
            onChange={(event) =>
              actions.onLrclibEnabledChange(event.currentTarget.checked)
            }
            aria-describedby={describedBy(
              fieldIds.autosaveHelp,
              fieldIds.lrclibHelp,
            )}
            className={styles.checkbox}
          />
          <span>{copy.lrclibEnabled}</span>
        </label>
        <p id={fieldIds.lrclibHelp} className={styles.help}>
          {copy.lrclibHelp}
        </p>
        {lrclib.dirty && <p className={styles.help}>{copy.unsavedLrclib}</p>}
      </section>
    </section>
  );
}
