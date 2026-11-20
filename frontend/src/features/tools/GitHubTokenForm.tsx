import { useEffect, useRef, useState } from "react";
import { deleteGithubToken, setGithubToken } from "../../api/generated/client";
import { useI18n } from "../../i18n/I18nProvider";
import {
  GitHubTokenFormView,
  redactionMarker,
  type TokenError,
} from "./GitHubTokenFormView";

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

export function GitHubTokenForm({
  hasToken,
  onChanged,
}: {
  hasToken: boolean;
  onChanged?: () => void | Promise<void>;
}) {
  const { locale } = useI18n();
  const [draft, setDraft] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<TokenError | null>(null);
  const [saved, setSaved] = useState(false);
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
    <GitHubTokenFormView
      locale={locale}
      hasToken={hasToken}
      draft={draft}
      busy={busy}
      saved={saved}
      error={error}
      onDraftChange={(next) => {
        setDraft(next);
        setError(null);
        setSaved(false);
      }}
      onSave={save}
      onRemove={remove}
    />
  );
}
