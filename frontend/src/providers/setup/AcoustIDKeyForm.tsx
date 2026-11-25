import { useState } from "react";
import {
  deleteAcoustidApplicationKey,
  setAcoustidApplicationKey,
} from "../../api/generated/client";
import { useI18n } from "../../app/i18n/host";
import { SetupAcoustIDView } from "../../ui/features/setup/SetupProvidersView";
import {
  type SetupFeedback,
  setupFeedback,
  setupFeedbackText,
  setupHttpError,
} from "./setupMessages";

type Props = {
  hasKey: boolean;
  onChanged: () => unknown;
  onBusyChange?: (busy: boolean) => void;
  disabled?: boolean;
};

export function AcoustIDKeyForm({
  hasKey,
  onChanged,
  onBusyChange,
  disabled,
}: Props) {
  const { locale } = useI18n();
  const [draft, setDraft] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<SetupFeedback | null>(null);
  const [notice, setNotice] = useState<"saved" | "deleted">();

  async function mutate(action: "save" | "delete") {
    if (disabled || busy || (action === "save" && !draft)) return;
    setBusy(true);
    onBusyChange?.(true);
    setError(null);
    setNotice(undefined);
    try {
      const response =
        action === "save"
          ? await setAcoustidApplicationKey({ key: draft })
          : await deleteAcoustidApplicationKey();
      if (response.status !== 204) throw setupHttpError(response);
      setDraft("");
      await onChanged();
      setNotice(action === "save" ? "saved" : "deleted");
    } catch (reason) {
      setError(setupFeedback(reason, "saveKeyError", draft || undefined));
    } finally {
      setBusy(false);
      onBusyChange?.(false);
    }
  }

  return (
    <SetupAcoustIDView
      locale={locale}
      draft={draft}
      hasKey={hasKey}
      busy={busy}
      disabled={disabled}
      error={error ? setupFeedbackText(error, locale) : undefined}
      notice={notice}
      onDraftChange={setDraft}
      onSave={() => {
        void mutate("save");
      }}
      onDelete={() => {
        void mutate("delete");
      }}
    />
  );
}
