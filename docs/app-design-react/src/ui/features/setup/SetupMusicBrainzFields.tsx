import { useState } from "react";
import { Tooltip, TooltipTrigger } from "react-aria-components";
import { AppButton } from "../../components/AppButton";
import { AppIcon } from "../../components/AppIcon";

export type SetupMusicBrainzLocale = "en" | "ru";

/** Prototype default may stay empty so no mode is preselected. */
export type SetupMusicBrainzMode = "" | "public" | "self-hosted";

export type SetupMusicBrainzVerification = "untested" | "changed" | "verified";

export interface SetupMusicBrainzFieldsProps {
  locale: SetupMusicBrainzLocale;
  mode: SetupMusicBrainzMode;
  /** Raw draft text; the parent owns persistence and verification. */
  baseURL: string;
  verification: SetupMusicBrainzVerification;
  /** Failure text from the last check; presence overrides `verification`. */
  verificationError?: string;
  verificationPending?: boolean;
  disabled?: boolean;
  /** Runs an explicit MusicBrainz connection check. */
  onVerify?: () => void;
  /** Requests a check after the URL field loses focus. */
  onBaseURLBlur?: () => void;
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
  baseURLHelp: string;
  musicBrainzVerified: string;
  musicBrainzFailed: string;
  musicBrainzCheck: string;
  musicBrainzChecking: string;
};

const copy: Record<SetupMusicBrainzLocale, Copy> = {
  en: {
    group: "MusicBrainz",
    description: "Choose the MusicBrainz service used for metadata lookup.",
    public: "Public",
    selfHosted: "Self-hosted",
    baseURL: "MusicBrainz base URL",
    baseURLHelp:
      "Enter the API address, not the website homepage. Example: https://musicbrainz.example.org/ws/2",
    musicBrainzVerified: "MusicBrainz API connection verified.",
    musicBrainzFailed: "MusicBrainz API connection failed.",
    musicBrainzCheck: "Check connection",
    musicBrainzChecking: "Checking connection…",
  },
  ru: {
    group: "MusicBrainz",
    description: "Выберите сервис MusicBrainz для поиска метаданных.",
    public: "Публичный сервис",
    selfHosted: "Свой сервер",
    baseURL: "Базовый URL MusicBrainz",
    baseURLHelp:
      "Укажите адрес API, а не главной страницы сайта. Например: https://musicbrainz.example.org/ws/2",
    musicBrainzVerified: "Подключение к API MusicBrainz проверено.",
    musicBrainzFailed: "Не удалось подключиться к API MusicBrainz.",
    musicBrainzCheck: "Проверить",
    musicBrainzChecking: "Проверка подключения…",
  },
};

const options = [
  { value: "public", label: "public" },
  { value: "self-hosted", label: "selfHosted" },
] as const;

const tooltipClass =
  "z-50 max-w-[320px] rounded-lg border border-[var(--line)] bg-[var(--ink)] px-2.5 py-1.5 text-xs text-[var(--surface)] shadow-[0_8px_24px_rgb(0_0_0/0.18)]";

export function SetupMusicBrainzFields({
  locale,
  mode,
  baseURL,
  verification,
  verificationError,
  verificationPending,
  disabled,
  onVerify,
  onBaseURLBlur,
  onModeChange,
  onBaseURLChange,
}: SetupMusicBrainzFieldsProps) {
  const text = copy[locale];
  const errorText = verificationError?.trim() || undefined;
  const failed = errorText !== undefined;
  const checking = Boolean(verificationPending);
  const baseURLId = "setup-musicbrainz-base-url";
  const baseURLHelpId = `${baseURLId}-help`;
  const baseURLErrorId = `${baseURLId}-error`;
  const [showError, setShowError] = useState(false);

  const verified = verification === "verified";
  const verifyButton = onVerify && (
    <AppButton
      aria-busy={checking || undefined}
      isDisabled={disabled || checking}
      onPress={onVerify}
      className={
        mode === "self-hosted"
          ? "shrink-0 rounded-none border-0 border-l border-[var(--control)] focus-visible:outline-offset-[-3px]"
          : undefined
      }
    >
      {checking && (
        <AppIcon name="refresh" className="motion-safe:animate-spin" />
      )}
      {text.musicBrainzCheck}
    </AppButton>
  );

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
      {mode !== "self-hosted" && (failed || checking || verified) && (
        <p
          role="status"
          className={`m-0 flex items-start gap-2 text-sm ${
            failed
              ? "text-[var(--error)]"
              : !checking && verified
                ? "text-[var(--success)]"
                : "text-[var(--muted)]"
          }`}
        >
          <AppIcon
            name={failed ? "close" : checking ? "refresh" : "check"}
            className={`mt-0.5 size-4 shrink-0 ${checking ? "motion-safe:animate-spin" : ""}`}
          />
          <span className="min-w-0">
            {failed ? (
              <>
                <span className="font-medium">{text.musicBrainzFailed}</span>{" "}
                <span className="wrap-anywhere">{errorText}</span>
              </>
            ) : checking ? (
              text.musicBrainzChecking
            ) : (
              text.musicBrainzVerified
            )}
          </span>
        </p>
      )}
      {mode === "self-hosted" && (
        <div className="flex min-w-0 flex-col gap-1">
          <label
            htmlFor={baseURLId}
            className="text-sm font-medium text-[var(--ink)]"
          >
            {text.baseURL}
          </label>
          <div
            className={`flex min-h-9 min-w-0 items-stretch overflow-hidden rounded-[var(--control-radius)] border focus-within:outline-2 focus-within:outline-offset-2 focus-within:outline-[var(--focus)] pointer-coarse:min-h-10 ${
              failed
                ? "border-[var(--error)] bg-[var(--error-soft)]"
                : "border-[var(--control)] bg-[var(--surface)]"
            }`}
          >
            <input
              id={baseURLId}
              type="text"
              inputMode="url"
              value={baseURL}
              onChange={(event) => onBaseURLChange(event.target.value)}
              onBlur={() => onBaseURLBlur?.()}
              onKeyDown={(event) => {
                if (event.key !== "Enter") return;
                event.preventDefault();
                if (!checking) onVerify?.();
              }}
              autoComplete="off"
              spellCheck={false}
              aria-invalid={failed || undefined}
              aria-describedby={
                failed ? `${baseURLHelpId} ${baseURLErrorId}` : baseURLHelpId
              }
              className="min-h-9 w-full min-w-0 flex-1 bg-transparent px-2 py-1 text-sm text-[var(--ink)] focus-visible:outline-none pointer-coarse:min-h-10"
            />
            {failed ? (
              <TooltipTrigger>
                <AppButton
                  variant="icon"
                  aria-label={`${text.musicBrainzFailed} ${errorText}`}
                  aria-expanded={showError}
                  aria-controls={baseURLErrorId}
                  onPress={() => setShowError((open) => !open)}
                  className="h-auto min-h-0 w-9 min-w-9 shrink-0 rounded-none text-[var(--error)] hover:bg-[color-mix(in_srgb,var(--error)_12%,transparent)] pointer-coarse:min-h-10"
                >
                  <AppIcon name="close" />
                </AppButton>
                <Tooltip className={tooltipClass}>
                  <span className="block font-medium">
                    {text.musicBrainzFailed}
                  </span>
                  <span className="mt-1 block wrap-anywhere">{errorText}</span>
                </Tooltip>
              </TooltipTrigger>
            ) : (
              !checking &&
              verified && (
                <TooltipTrigger>
                  <AppButton
                    variant="icon"
                    aria-label={text.musicBrainzVerified}
                    isDisabled={disabled}
                    onPress={onVerify}
                    className="h-auto min-h-0 w-9 min-w-9 shrink-0 rounded-none text-[var(--success)] hover:bg-[var(--success-soft)] pointer-coarse:min-h-10"
                  >
                    <AppIcon name="check" />
                  </AppButton>
                  <Tooltip className={tooltipClass}>
                    {text.musicBrainzVerified}
                  </Tooltip>
                </TooltipTrigger>
              )
            )}
            {verifyButton}
          </div>
          <p
            id={baseURLHelpId}
            className="m-0 wrap-anywhere text-sm text-[var(--muted)]"
          >
            {text.baseURLHelp}
          </p>
          {failed ? (
            <p
              id={baseURLErrorId}
              role="status"
              className={`wrap-anywhere text-sm text-[var(--error)] ${
                showError
                  ? "m-0 block"
                  : "sr-only pointer-coarse:not-sr-only m-0"
              }`}
            >
              <span className="font-medium">{text.musicBrainzFailed}</span>{" "}
              <span>{errorText}</span>
            </p>
          ) : checking ? (
            <p
              role="status"
              className="m-0 flex items-center gap-2 text-sm text-[var(--muted)]"
            >
              <AppIcon
                name="refresh"
                className="size-4 shrink-0 motion-safe:animate-spin"
              />
              {text.musicBrainzChecking}
            </p>
          ) : verified ? (
            <p role="status" className="sr-only">
              {text.musicBrainzVerified}
            </p>
          ) : null}
        </div>
      )}
      {mode !== "self-hosted" && verifyButton}
    </fieldset>
  );
}
