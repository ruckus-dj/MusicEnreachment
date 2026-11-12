import { useId, useState } from "react";
import {
  deleteAcoustidApplicationKey,
  setAcoustidApplicationKey,
} from "../../api/generated/client";
import { AppButton } from "../../components/AppButton";
import { useI18n } from "../../i18n/I18nProvider";
import {
  type SetupFeedback,
  setupFeedback,
  setupFeedbackText,
  setupHttpError,
  setupMessage,
  setupMessages,
} from "./setupMessages";
import "./AcoustIDKeyForm.css";

type Props = {
  hasKey: boolean;
  onChanged: () => unknown;
  onBusyChange?: (busy: boolean) => void;
};

export function AcoustIDKeyForm({ hasKey, onChanged, onBusyChange }: Props) {
  const { locale } = useI18n();
  const messages = setupMessages[locale];
  const [draft, setDraft] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<SetupFeedback | null>(null);
  const [notice, setNotice] = useState<SetupFeedback | null>(null);
  const inputId = useId();

  async function mutate(action: "save" | "delete") {
    if (busy || (action === "save" && !draft)) return;
    setBusy(true);
    onBusyChange?.(true);
    setError(null);
    setNotice(null);
    try {
      const response =
        action === "save"
          ? await setAcoustidApplicationKey({ key: draft })
          : await deleteAcoustidApplicationKey();
      if (response.status !== 204) throw setupHttpError(response);
      setDraft("");
      await onChanged();
      setNotice(setupMessage(action === "save" ? "keySaved" : "keyDeleted"));
    } catch (reason) {
      setError(setupFeedback(reason, "saveKeyError", draft || undefined));
    } finally {
      setBusy(false);
      onBusyChange?.(false);
    }
  }

  return (
    <section
      className="setup-acoustid-key"
      aria-labelledby={`${inputId}-heading`}
    >
      <h2 id={`${inputId}-heading`}>{messages.acoustidOptional}</h2>
      <p>{messages.acoustidDescription}</p>
      <p>
        <a
          href="https://acoustid.org/new-application"
          target="_blank"
          rel="noreferrer"
        >
          {messages.registerAcoustid}
        </a>
        {" · "}
        <a
          href="https://acoustid.org/webservice"
          target="_blank"
          rel="noreferrer"
        >
          {messages.apiDocumentation}
        </a>
        {" · "}
        <a href="https://acoustid.biz/" target="_blank" rel="noreferrer">
          {messages.commercialTerms}
        </a>
      </p>
      <p>{messages.acoustidFreeUse}</p>
      <label htmlFor={inputId}>{messages.acoustidApplicationKey}</label>
      <input
        autoComplete="new-password"
        disabled={busy}
        id={inputId}
        onChange={(event) => setDraft(event.currentTarget.value)}
        type="password"
        value={draft}
      />
      <p role="status">
        {hasKey ? messages.keySavedHidden : messages.keyNotSet}
      </p>
      <div className="setup-acoustid-key-actions">
        <AppButton isDisabled={busy || !draft} onPress={() => mutate("save")}>
          {messages.saveKey}
        </AppButton>
        {hasKey && (
          <AppButton isDisabled={busy} onPress={() => mutate("delete")}>
            {messages.deleteKey}
          </AppButton>
        )}
      </div>
      {error && <p role="alert">{setupFeedbackText(error, locale)}</p>}
      {notice && <p role="status">{setupFeedbackText(notice, locale)}</p>}
    </section>
  );
}
