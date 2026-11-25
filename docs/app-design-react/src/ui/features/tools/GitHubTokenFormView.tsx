import { useId } from "react";
import { AppButton } from "../../components/AppButton";
import {
  type GitHubTokenLocale,
  type ToolMessages,
  toolMessages,
} from "./toolMessages";

const tokenGuide =
  "https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens";
const tokenCreate =
  "https://github.com/settings/personal-access-tokens/new?name=MeloTrove+release+reader&description=Read-only+public+release+metadata&expires_in=90";
const tokenSettings = "https://github.com/settings/personal-access-tokens";

// Controllers redact diagnostics before passing them here and keep this marker
// in the text; the view replaces it with the localized redaction copy.
export const redactionMarker = "\u0000redacted\u0000";

export type TokenError =
  | { kind: "saveFailed" }
  | { kind: "deleteFailed" }
  | { kind: "refreshFailed" }
  | { kind: "http"; status: number }
  | { kind: "diagnostic"; text: string };

// The draft is fully controlled: it lives in the controller's memory only and
// is never stored, so it disappears with the page.
export type GitHubTokenFormViewProps = {
  locale: GitHubTokenLocale;
  simulated?: boolean;
  hasToken: boolean;
  draft: string;
  busy: boolean;
  saved: boolean;
  /** Already redacted by the controller. */
  error: TokenError | null;
  onDraftChange: (draft: string) => void;
  onSave: () => void | Promise<void>;
  onRemove: () => void | Promise<void>;
};

const sectionClass =
  "grid gap-3 rounded-md border border-[var(--line)] bg-[var(--surface)] p-4";
const headingClass = "m-0 text-base font-semibold [text-wrap:balance]";
const paragraphClass = "m-0 max-w-[75ch] text-sm";
const mutedClass = "text-[var(--muted)]";
const codeClass =
  "rounded-[3px] bg-[var(--raised)] px-1 py-0.5 font-mono text-[0.9em] text-[var(--ink)]";
const linkClass =
  "text-[var(--accent)] underline underline-offset-2 hover:no-underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--focus)]";
const stepsClass = "m-0 max-w-[75ch] list-decimal space-y-1 pl-5 text-sm";
const fieldClass =
  "grid max-w-[420px] min-w-0 gap-[5px] text-xs font-semibold text-[var(--ink)]";
const inputClass =
  "min-h-[38px] w-full min-w-0 rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-[10px] py-2 font-normal text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2 disabled:cursor-not-allowed disabled:text-[var(--muted)]";
const actionsClass = "flex flex-wrap items-center gap-2";
const statusClass = "m-0 text-sm text-[var(--muted)]";
const successClass = "m-0 text-sm text-[var(--accent)]";
const errorClass =
  "m-0 wrap-anywhere rounded border border-[var(--error)] bg-[var(--error-soft)] p-2 text-sm text-[var(--error)]";
const revokeClass = "m-0 max-w-[75ch] text-sm text-[var(--muted)]";

function errorText(error: TokenError, copy: ToolMessages) {
  switch (error.kind) {
    case "diagnostic":
      return error.text.replaceAll(redactionMarker, copy.redacted);
    case "http":
      return copy.httpError(error.status);
    case "saveFailed":
      return copy.saveError;
    case "deleteFailed":
      return copy.deleteError;
    case "refreshFailed":
      return copy.refreshError;
  }
}

export function GitHubTokenFormView({
  locale,
  simulated = false,
  hasToken,
  draft,
  busy,
  saved,
  error,
  onDraftChange,
  onSave,
  onRemove,
}: GitHubTokenFormViewProps) {
  const copy = toolMessages[locale];
  const headingId = useId();
  const inputId = useId();
  const stateId = useId();
  const errorId = useId();
  const modeId = useId();

  return (
    <section className={sectionClass} aria-labelledby={headingId}>
      <h2 id={headingId} className={headingClass}>
        {copy.heading}
      </h2>
      {simulated ? (
        <p id={modeId} className={`${paragraphClass} font-medium`}>
          {copy.demoWarning}
        </p>
      ) : (
        <>
          <p className={paragraphClass}>{copy.rateLimit}</p>
          <p className={paragraphClass}>
            {copy.recommendation[0]}
            <code className={codeClass}>repo</code>
            {copy.recommendation[1]}
            <code className={codeClass}>public_repo</code>
            {copy.recommendation[2]}
          </p>
          <p className={`${paragraphClass} ${mutedClass}`}>
            {copy.tokenStorage}
          </p>
          <p className={`${paragraphClass} ${mutedClass}`}>
            {copy.tokenTransport}
          </p>
          <p className={paragraphClass}>
            <a
              className={linkClass}
              href={tokenCreate}
              target="_blank"
              rel="noreferrer"
            >
              {copy.createToken}
            </a>{" "}
            ({copy.expiryNote}) ·{" "}
            <a
              className={linkClass}
              href={tokenGuide}
              target="_blank"
              rel="noreferrer"
            >
              {copy.guide}
            </a>
            .
          </p>
          <ol className={stepsClass}>
            <li>{copy.stepOpenSettings}</li>
            <li>{copy.stepChooseOwner}</li>
            <li>
              {copy.stepCopyToken[0]}
              <code className={codeClass}>repo</code>
              {copy.stepCopyToken[1]}
              <code className={codeClass}>public_repo</code>
              {copy.stepCopyToken[2]}
            </li>
          </ol>
        </>
      )}
      <div className={fieldClass}>
        <label htmlFor={inputId}>{copy.tokenLabel}</label>
        <input
          id={inputId}
          type="password"
          autoComplete="new-password"
          value={draft}
          disabled={busy}
          aria-describedby={[simulated && modeId, stateId, error && errorId]
            .filter(Boolean)
            .join(" ")}
          onChange={(event) => onDraftChange(event.currentTarget.value)}
          className={inputClass}
        />
      </div>
      <div className={actionsClass}>
        <AppButton
          isDisabled={busy || !(simulated ? draft.trim() : draft)}
          onPress={() => void onSave()}
        >
          {copy.verifyAndSave}
        </AppButton>
        {hasToken && (
          <AppButton isDisabled={busy} onPress={() => void onRemove()}>
            {copy.removeSaved}
          </AppButton>
        )}
      </div>
      <div className="grid gap-1">
        <p id={stateId} role="status" className={statusClass}>
          {hasToken
            ? simulated
              ? copy.demoSaved
              : copy.tokenSaved
            : copy.tokenMissing}
        </p>
        {saved && (
          <p role="status" className={successClass}>
            {simulated ? copy.demoVerified : copy.tokenVerified}
          </p>
        )}
        {busy && (
          <p role="status" className={statusClass}>
            {copy.requestInProgress}
          </p>
        )}
        {error && (
          <p id={errorId} role="alert" className={errorClass}>
            {errorText(error, copy)}
          </p>
        )}
      </div>
      {hasToken && !simulated && (
        <p className={revokeClass}>
          {copy.revokeHelp}{" "}
          <a
            className={linkClass}
            href={tokenSettings}
            target="_blank"
            rel="noreferrer"
          >
            {copy.revokeLink}
          </a>
          .
        </p>
      )}
    </section>
  );
}
