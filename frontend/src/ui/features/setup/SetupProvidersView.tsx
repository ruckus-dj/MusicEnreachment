import { type ReactNode, useId } from "react";
import { AppButton } from "../../components/AppButton";
import { AppIcon } from "../../components/AppIcon";
import {
  SetupMusicBrainzFields,
  type SetupMusicBrainzLocale,
  type SetupMusicBrainzMode,
  type SetupMusicBrainzVerification,
} from "./SetupMusicBrainzFields";

type Copy = {
  lrclibEnabled: string;
  acoustidOptional: string;
  acoustidDescription: string;
  registerAcoustid: string;
  apiDocumentation: string;
  commercialTerms: string;
  acoustidFreeUse: string;
  acoustidApplicationKey: string;
  keySavedHidden: string;
  keyNotSet: string;
  keyVerified: string;
  saveKey: string;
  deleteKey: string;
  saved: string;
  deleted: string;
};

const copy: Record<SetupMusicBrainzLocale, Copy> = {
  en: {
    lrclibEnabled: "LRCLIB enabled",
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
    keyVerified: "AcoustID key verified.",
    saveKey: "Save key",
    deleteKey: "Delete key",
    saved: "AcoustID application key saved.",
    deleted: "AcoustID application key deleted.",
  },
  ru: {
    lrclibEnabled: "LRCLIB включён",
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
    keyVerified: "Ключ AcoustID проверен.",
    saveKey: "Сохранить ключ",
    deleteKey: "Удалить ключ",
    saved: "Ключ приложения AcoustID сохранён.",
    deleted: "Ключ приложения AcoustID удалён.",
  },
};

export type SetupProvidersVerification = SetupMusicBrainzVerification;

export interface SetupProvidersViewProps {
  locale: SetupMusicBrainzLocale;
  mode: SetupMusicBrainzMode;
  /** Raw draft text; the parent owns persistence and verification. */
  baseURL: string;
  verification: SetupProvidersVerification;
  /** Failure text from the last check; presence overrides `verification`. */
  verificationError?: string;
  verificationPending?: boolean;
  lrclib?: {
    enabled: boolean;
    onChange: (enabled: boolean) => void;
  };
  /** Optional provider-specific form; the parent owns its execution. */
  acoustID?: ReactNode;
  disabled?: boolean;
  /** Runs an explicit MusicBrainz connection check. */
  onVerify?: () => void;
  /** Requests a check after the URL field loses focus. */
  onBaseURLBlur?: () => void;
  onModeChange: (mode: "public" | "self-hosted") => void;
  onBaseURLChange: (value: string) => void;
}

export function SetupProvidersView({
  locale,
  mode,
  baseURL,
  verification,
  verificationError,
  verificationPending,
  lrclib,
  acoustID,
  disabled,
  onVerify,
  onBaseURLBlur,
  onModeChange,
  onBaseURLChange,
}: SetupProvidersViewProps) {
  const text = copy[locale];
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
          verification={verification}
          verificationError={verificationError}
          verificationPending={verificationPending}
          disabled={disabled}
          onVerify={onVerify}
          onBaseURLBlur={onBaseURLBlur}
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
        {acoustID}
      </fieldset>
    </section>
  );
}

/** Fixed mask for a stored key; the actual value is never returned to the view. */
const maskedKeyPlaceholder = "••••••••••••";

export interface SetupAcoustIDViewProps {
  locale: SetupMusicBrainzLocale;
  /** Write-only draft; a saved key is never read back into this value. */
  draft: string;
  hasKey: boolean;
  /** Presenter-verified saved key; shows a checkmark while the draft is empty. */
  verified?: boolean;
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
  verified,
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
  const errorId = `${inputId}-error`;
  const blocked = Boolean(busy || disabled);
  const failed = Boolean(error);
  const showVerified = Boolean(
    hasKey && verified && draft.length === 0 && !busy,
  );
  return (
    <section
      className="grid min-w-0 gap-2"
      aria-labelledby={`${inputId}-heading`}
      aria-busy={busy || undefined}
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
      <div className="flex max-w-[32rem] min-w-0 flex-col gap-1">
        <label
          htmlFor={inputId}
          className="text-sm font-medium text-[var(--ink)]"
        >
          {text.acoustidApplicationKey}
        </label>
        <div
          className={`flex min-h-9 min-w-0 items-stretch overflow-hidden rounded-[var(--control-radius)] border focus-within:outline-2 focus-within:outline-offset-2 focus-within:outline-[var(--focus)] pointer-coarse:min-h-10 ${
            failed
              ? "border-[var(--error)] bg-[var(--error-soft)]"
              : "border-[var(--control)] bg-[var(--surface)]"
          }`}
        >
          <input
            autoComplete="new-password"
            disabled={blocked}
            id={inputId}
            onChange={(event) => onDraftChange(event.currentTarget.value)}
            onKeyDown={(event) => {
              if (event.key !== "Enter" || event.nativeEvent.isComposing) {
                return;
              }
              event.preventDefault();
              if (!blocked && draft.trim()) onSave();
            }}
            placeholder={
              hasKey && draft.length === 0 ? maskedKeyPlaceholder : undefined
            }
            type="password"
            value={draft}
            aria-invalid={failed || undefined}
            aria-describedby={failed ? `${statusId} ${errorId}` : statusId}
            className="min-h-9 w-full min-w-0 flex-1 bg-transparent px-2 py-1 text-sm text-[var(--ink)] focus-visible:outline-none disabled:cursor-not-allowed disabled:text-[var(--muted)] pointer-coarse:min-h-10"
          />
          {busy ? (
            <span className="flex w-9 shrink-0 items-center justify-center text-[var(--muted)]">
              <AppIcon name="refresh" className="motion-safe:animate-spin" />
            </span>
          ) : (
            showVerified && (
              <span
                role="img"
                aria-label={text.keyVerified}
                className="flex w-9 shrink-0 items-center justify-center text-[var(--success)]"
              >
                <AppIcon name="check" />
              </span>
            )
          )}
          <AppButton
            isDisabled={blocked || !draft.trim()}
            onPress={onSave}
            className="shrink-0 rounded-none border-0 border-l border-[var(--control)] focus-visible:outline-offset-[-3px]"
          >
            {text.saveKey}
          </AppButton>
          {hasKey && (
            <AppButton
              variant="icon"
              isDisabled={blocked}
              onPress={onDelete}
              className="h-auto min-h-0 w-9 min-w-9 shrink-0 rounded-none border-0 border-l border-[var(--control)] hover:bg-[color-mix(in_srgb,var(--error)_12%,transparent)] hover:text-[var(--error)] pointer-coarse:min-h-10"
            >
              <AppIcon name="trash" />
              <span className="sr-only">{text.deleteKey}</span>
            </AppButton>
          )}
        </div>
        <p id={statusId} role="status" className="sr-only">
          {hasKey
            ? verified
              ? text.keyVerified
              : text.keySavedHidden
            : text.keyNotSet}
        </p>
      </div>
      {error && (
        <p
          id={errorId}
          role="alert"
          className="m-0 wrap-anywhere text-sm text-[var(--error)]"
        >
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
