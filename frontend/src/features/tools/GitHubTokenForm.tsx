import { useEffect, useId, useRef, useState } from "react";
import { deleteGithubToken, setGithubToken } from "../../api/generated/client";
import { AppButton } from "../../components/AppButton";
import { useI18n } from "../../i18n/I18nProvider";
import "./github-token.css";
import { type ToolMessages, toolMessages } from "./toolMessages";

const tokenGuide =
  "https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens";
const tokenCreate =
  "https://github.com/settings/personal-access-tokens/new?name=MeloTrove+release+reader&description=Read-only+public+release+metadata&expires_in=90";
const redactionMarker = "\u0000redacted\u0000";

type TokenError =
  | { kind: "saveFailed" }
  | { kind: "deleteFailed" }
  | { kind: "refreshFailed" }
  | { kind: "http"; status: number }
  | { kind: "diagnostic"; text: string };

function responseDetail(response: { data?: unknown }): string | undefined {
  const data = response.data;
  if (
    typeof data === "object" &&
    data !== null &&
    "detail" in data &&
    typeof data.detail === "string" &&
    data.detail
  )
    return data.detail;
  return undefined;
}

function redact(text: string, secret: string) {
  return secret ? text.replaceAll(secret, redactionMarker) : text;
}

function responseFailure(
  response: { status: number; data?: unknown },
  secret: string,
): TokenError {
  const detail = responseDetail(response);
  return detail === undefined
    ? { kind: "http", status: response.status }
    : { kind: "diagnostic", text: redact(detail, secret) };
}

function thrownFailure(
  reason: unknown,
  fallback: TokenError,
  secret: string,
): TokenError {
  return reason instanceof Error && reason.message
    ? { kind: "diagnostic", text: redact(reason.message, secret) }
    : fallback;
}

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

export function GitHubTokenForm({
  hasToken,
  onChanged,
}: {
  hasToken: boolean;
  onChanged?: () => void | Promise<void>;
}) {
  const { locale } = useI18n();
  const copy = toolMessages[locale];
  const [draft, setDraft] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<TokenError | null>(null);
  const [saved, setSaved] = useState(false);
  const headingId = useId();
  const inputId = useId();
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  async function save() {
    if (!draft || busy) return;
    const token = draft;
    setBusy(true);
    setError(null);
    setSaved(false);
    try {
      const response = await setGithubToken({ token });
      if (response.status !== 204) {
        if (mounted.current) setError(responseFailure(response, token));
        return;
      }
      if (!mounted.current) return;
      setDraft("");
      setSaved(true);
      try {
        await onChanged?.();
      } catch {
        if (mounted.current) setError({ kind: "refreshFailed" });
      }
    } catch (reason) {
      if (mounted.current)
        setError(thrownFailure(reason, { kind: "saveFailed" }, token));
    } finally {
      if (mounted.current) setBusy(false);
    }
  }

  async function remove() {
    if (busy) return;
    const token = draft;
    setBusy(true);
    setError(null);
    setSaved(false);
    try {
      const response = await deleteGithubToken();
      if (response.status !== 204) {
        if (mounted.current) setError(responseFailure(response, token));
        return;
      }
      if (!mounted.current) return;
      setDraft("");
      setSaved(false);
      await onChanged?.();
    } catch (reason) {
      if (mounted.current)
        setError(thrownFailure(reason, { kind: "deleteFailed" }, token));
    } finally {
      if (mounted.current) setBusy(false);
    }
  }

  return (
    <section className="github-token" aria-labelledby={headingId}>
      <h2 id={headingId}>{copy.heading}</h2>
      <p>{copy.rateLimit}</p>
      <p>
        {copy.recommendation[0]}
        <code>repo</code>
        {copy.recommendation[1]}
        <code>public_repo</code>
        {copy.recommendation[2]}
      </p>
      <p>{copy.tokenStorage}</p>
      <p>{copy.tokenTransport}</p>
      <p>
        <a href={tokenCreate} target="_blank" rel="noreferrer">
          {copy.createToken}
        </a>{" "}
        ({copy.expiryNote}) ·{" "}
        <a href={tokenGuide} target="_blank" rel="noreferrer">
          {copy.guide}
        </a>
        .
      </p>
      <ol>
        <li>{copy.stepOpenSettings}</li>
        <li>{copy.stepChooseOwner}</li>
        <li>
          {copy.stepCopyToken[0]}
          <code>repo</code>
          {copy.stepCopyToken[1]}
          <code>public_repo</code>
          {copy.stepCopyToken[2]}
        </li>
      </ol>
      <label htmlFor={inputId}>{copy.tokenLabel}</label>
      <input
        id={inputId}
        type="password"
        autoComplete="new-password"
        value={draft}
        disabled={busy}
        onChange={(event) => {
          setDraft(event.currentTarget.value);
          setError(null);
          setSaved(false);
        }}
      />
      <div className="github-token-actions">
        <AppButton isDisabled={busy || !draft} onPress={() => void save()}>
          {copy.verifyAndSave}
        </AppButton>
        {hasToken && (
          <AppButton isDisabled={busy} onPress={() => void remove()}>
            {copy.removeSaved}
          </AppButton>
        )}
      </div>
      <p role="status">{hasToken ? copy.tokenSaved : copy.tokenMissing}</p>
      {saved && <p role="status">{copy.tokenVerified}</p>}
      {busy && <p role="status">{copy.requestInProgress}</p>}
      {error && <p role="alert">{errorText(error, copy)}</p>}
      {hasToken && (
        <p>
          {copy.revokeHelp}{" "}
          <a
            href="https://github.com/settings/personal-access-tokens"
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
