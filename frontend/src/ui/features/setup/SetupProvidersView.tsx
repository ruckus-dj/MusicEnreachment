import { type ReactNode, useId } from "react";
import { AppButton } from "../../components/AppButton";
import {
  SetupMusicBrainzFields,
  type SetupMusicBrainzLocale,
  type SetupMusicBrainzMode,
} from "./SetupMusicBrainzFields";

type Copy = {
  lrclibEnabled: string;
  musicBrainzChanged: string;
  musicBrainzCheckedAt: string;
  musicBrainzVerified: string;
  musicBrainzNotChecked: string;
  acoustidOptional: string;
  acoustidDescription: string;
  registerAcoustid: string;
  apiDocumentation: string;
  commercialTerms: string;
  acoustidFreeUse: string;
  acoustidApplicationKey: string;
  keySavedHidden: string;
  keyNotSet: string;
  saveKey: string;
  deleteKey: string;
  saved: string;
  deleted: string;
};

const copy: Record<SetupMusicBrainzLocale, Copy> = {
  en: {
    lrclibEnabled: "LRCLIB enabled",
    musicBrainzChanged: "Configuration changed. Check MusicBrainz again.",
    musicBrainzCheckedAt: "Checked: ",
    musicBrainzVerified: "Connection verified.",
    musicBrainzNotChecked: "Connection has not been checked yet.",
    acoustidOptional: "AcoustID (optional)",
    acoustidDescription:
      "An application key enables audio-fingerprint search. The fingerprint, not the audio file, is sent. Use an application key, not a user API key (that is used to submit fingerprints).",
    registerAcoustid: "Register an AcoustID application",
    apiDocumentation: "API documentation",
    commercialTerms: "Commercial use terms",
    acoustidFreeUse:
      "Free AcoustID use is intended for non-commercial projects.",
    acoustidApplicationKey: "AcoustID application key",
    keySavedHidden: "Key saved (value hidden).",
    keyNotSet: "Key is not set.",
    saveKey: "Save key",
    deleteKey: "Delete key",
    saved: "AcoustID application key saved.",
    deleted: "AcoustID application key deleted.",
  },
  ru: {
    lrclibEnabled: "LRCLIB включён",
    musicBrainzChanged: "Конфигурация изменена. Проверьте MusicBrainz снова.",
    musicBrainzCheckedAt: "Проверено: ",
    musicBrainzVerified: "Соединение проверено.",
    musicBrainzNotChecked: "Соединение ещё не проверено.",
    acoustidOptional: "AcoustID (необязательно)",
    acoustidDescription:
      "Ключ приложения включает поиск по аудиоотпечатку: отправляется отпечаток, а не аудиофайл. Нужен ключ приложения, а не пользовательский API key (он используется для отправки отпечатков).",
    registerAcoustid: "Зарегистрировать приложение AcoustID",
    apiDocumentation: "Документация API",
    commercialTerms: "Условия коммерческого использования",
    acoustidFreeUse:
      "Бесплатное использование AcoustID предназначено для некоммерческих проектов.",
    acoustidApplicationKey: "Ключ приложения AcoustID",
    keySavedHidden: "Ключ сохранён (значение скрыто).",
    keyNotSet: "Ключ не задан.",
    saveKey: "Сохранить ключ",
    deleteKey: "Удалить ключ",
    saved: "Ключ приложения AcoustID сохранён.",
    deleted: "Ключ приложения AcoustID удалён.",
  },
};

export type SetupProvidersVerification = "untested" | "changed" | "verified";

const statusClassName: Record<SetupProvidersVerification, string> = {
  untested: "m-0 text-sm text-[var(--muted)]",
  changed: "m-0 text-sm text-[var(--warning)]",
  verified: "m-0 text-sm text-[var(--success)]",
};

export interface SetupProvidersViewProps {
  locale: SetupMusicBrainzLocale;
  mode: SetupMusicBrainzMode;
  /** Raw draft text; the parent owns persistence and verification. */
  baseURL: string;
  verification: SetupProvidersVerification;
  /** RFC3339 instant shown only for a verified configuration. */
  verifiedAt?: string | null;
  lrclib?: {
    enabled: boolean;
    onChange: (enabled: boolean) => void;
  };
  /** Optional provider-specific form; the parent owns its execution. */
  acoustID?: ReactNode;
  disabled?: boolean;
  onModeChange: (mode: "public" | "self-hosted") => void;
  onBaseURLChange: (value: string) => void;
}

export function SetupProvidersView({
  locale,
  mode,
  baseURL,
  verification,
  verifiedAt,
  lrclib,
  acoustID,
  disabled,
  onModeChange,
  onBaseURLChange,
}: SetupProvidersViewProps) {
  const text = copy[locale];
  let status: ReactNode = text.musicBrainzNotChecked;
  if (verification === "changed") {
    status = text.musicBrainzChanged;
  } else if (verification === "verified") {
    status = verifiedAt ? (
      <>
        {text.musicBrainzCheckedAt}
        <time dateTime={verifiedAt}>{verifiedAt}</time>
      </>
    ) : (
      text.musicBrainzVerified
    );
  }
  return (
    <section className="grid gap-2 border-t border-[var(--line)] pt-3">
      <fieldset
        className="m-0 grid min-w-0 gap-2 border-0 p-0"
        disabled={disabled}
      >
        <SetupMusicBrainzFields
          locale={locale}
          mode={mode}
          baseURL={baseURL}
          onModeChange={onModeChange}
          onBaseURLChange={onBaseURLChange}
        />
        {lrclib && (
          <label className="flex min-h-8 w-fit cursor-pointer items-center gap-2 text-sm text-[var(--ink)] pointer-coarse:min-h-10">
            <input
              type="checkbox"
              checked={lrclib.enabled}
              disabled={disabled}
              onChange={(event) => lrclib.onChange(event.currentTarget.checked)}
              className="size-4 accent-[var(--accent)] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--focus)] disabled:cursor-not-allowed"
            />
            <span>{text.lrclibEnabled}</span>
          </label>
        )}
        <p className={statusClassName[verification]}>{status}</p>
        {acoustID}
      </fieldset>
    </section>
  );
}

export interface SetupAcoustIDViewProps {
  locale: SetupMusicBrainzLocale;
  /** Write-only draft; a saved key is never read back into this value. */
  draft: string;
  hasKey: boolean;
  busy?: boolean;
  disabled?: boolean;
  error?: string;
  notice?: "saved" | "deleted";
  onDraftChange: (value: string) => void;
  onSave: () => void;
  onDelete: () => void;
}

export function SetupAcoustIDView({
  locale,
  draft,
  hasKey,
  busy,
  disabled,
  error,
  notice,
  onDraftChange,
  onSave,
  onDelete,
}: SetupAcoustIDViewProps) {
  const text = copy[locale];
  const inputId = useId();
  const statusId = `${inputId}-status`;
  const blocked = Boolean(busy || disabled);
  return (
    <section
      className="grid min-w-0 gap-2"
      aria-labelledby={`${inputId}-heading`}
    >
      <h3
        id={`${inputId}-heading`}
        className="m-0 text-sm font-semibold text-[var(--ink)]"
      >
        {text.acoustidOptional}
      </h3>
      <p className="m-0 text-sm text-[var(--muted)]">
        {text.acoustidDescription}
      </p>
      <p className="m-0 text-sm text-[var(--muted)]">
        <a
          href="https://acoustid.org/new-application"
          target="_blank"
          rel="noreferrer"
        >
          {text.registerAcoustid}
        </a>
        {" · "}
        <a
          href="https://acoustid.org/webservice"
          target="_blank"
          rel="noreferrer"
        >
          {text.apiDocumentation}
        </a>
        {" · "}
        <a href="https://acoustid.biz/" target="_blank" rel="noreferrer">
          {text.commercialTerms}
        </a>
      </p>
      <p className="m-0 text-sm text-[var(--muted)]">{text.acoustidFreeUse}</p>
      <div className="flex max-w-[32rem] flex-col gap-1">
        <label
          htmlFor={inputId}
          className="text-sm font-medium text-[var(--ink)]"
        >
          {text.acoustidApplicationKey}
        </label>
        <input
          autoComplete="new-password"
          disabled={blocked}
          id={inputId}
          onChange={(event) => onDraftChange(event.currentTarget.value)}
          type="password"
          value={draft}
          aria-describedby={statusId}
          className="min-h-9 w-full min-w-0 rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 py-1 text-sm text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--focus)] disabled:cursor-not-allowed disabled:text-[var(--muted)] pointer-coarse:min-h-10"
        />
      </div>
      <p
        id={statusId}
        role="status"
        className="m-0 text-sm text-[var(--muted)]"
      >
        {hasKey ? text.keySavedHidden : text.keyNotSet}
      </p>
      <div className="flex flex-wrap gap-2">
        <AppButton isDisabled={blocked || draft.length === 0} onPress={onSave}>
          {text.saveKey}
        </AppButton>
        {hasKey && (
          <AppButton isDisabled={blocked} onPress={onDelete}>
            {text.deleteKey}
          </AppButton>
        )}
      </div>
      {error && (
        <p role="alert" className="m-0 text-sm text-[var(--error)]">
          {error}
        </p>
      )}
      {notice && (
        <p role="status" className="m-0 text-sm text-[var(--muted)]">
          {text[notice]}
        </p>
      )}
    </section>
  );
}
