import { type ReactNode, useEffect, useRef, useState } from "react";
import { deleteGithubToken, setGithubToken } from "../../api/generated/client";
import { useI18n } from "../../app/i18n/host";
import { SettingsTokenDataBinder } from "../../ui/features/settings/SettingsTokenOutlet";
import {
  type GitHubTokenFormViewProps,
  redactionMarker,
  type TokenError,
} from "../../ui/features/tools/GitHubTokenFormView";

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

type GitHubTokenFormInputs = {
  hasToken: boolean;
  onChanged?: () => void | Promise<void>;
};

export type GitHubTokenFormProviderProps = GitHubTokenFormInputs & {
  children: ReactNode;
};

/**
 * Owns the token controller for as long as the portable Settings Tools panel is
 * mounted. The panel renders the model through the typed token outlet.
 */
export function GitHubTokenFormProvider({
  hasToken,
  onChanged,
  children,
}: GitHubTokenFormProviderProps) {
  const model = useGitHubTokenFormModel({ hasToken, onChanged });
  return (
    <SettingsTokenDataBinder model={model}>{children}</SettingsTokenDataBinder>
  );
}

export function useGitHubTokenFormModel({
  hasToken,
  onChanged,
}: GitHubTokenFormInputs): GitHubTokenFormViewProps {
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

  return {
    locale,
    hasToken,
    draft,
    busy,
    saved,
    error,
    onDraftChange: (next) => {
      setDraft(next);
      setError(null);
      setSaved(false);
    },
    onSave: save,
    onRemove: remove,
  };
}
