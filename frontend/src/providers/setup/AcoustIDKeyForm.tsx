import { type ReactNode, useState } from "react";
import {
  deleteAcoustidApplicationKey,
  setAcoustidApplicationKey,
} from "../../api/generated/client";
import { useI18n } from "../../app/i18n/host";
import type { SetupAcoustIDViewProps } from "../../ui/features/setup/SetupProvidersView";
import {
  SetupAcoustIDDataBinder,
  type SetupAcoustIDProviderProps,
} from "../../ui/features/setup/SetupScreenView";
import {
  type SetupFeedback,
  setupFeedback,
  setupFeedbackText,
  setupHttpError,
  setupMessage,
} from "../../ui/features/setup/setupMessages";

export function SetupAcoustIDBinder({
  children,
  ...props
}: SetupAcoustIDProviderProps & { children: ReactNode }) {
  const model = useSetupAcoustIDModel(props);
  return (
    <SetupAcoustIDDataBinder model={model}>{children}</SetupAcoustIDDataBinder>
  );
}

function useSetupAcoustIDModel({
  hasKey,
  verified,
  onChanged,
  onBusyChange,
  disabled,
}: SetupAcoustIDProviderProps): SetupAcoustIDViewProps {
  const { locale } = useI18n();
  const [draft, setDraft] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<SetupFeedback | null>(null);
  const [notice, setNotice] = useState<"saved" | "deleted">();

  async function mutate(action: "save" | "delete") {
    if (disabled || busy || (action === "save" && !draft.trim())) return;
    setBusy(true);
    onBusyChange?.(true);
    setError(null);
    setNotice(undefined);
    try {
      const response =
        action === "save"
          ? await setAcoustidApplicationKey({ key: draft })
          : await deleteAcoustidApplicationKey();
      if (action === "save") {
        if (response.status === 422) throw setupMessage("acoustidKeyRejected");
        if (response.status === 502)
          throw setupMessage("acoustidCheckUnavailable");
      }
      if (response.status !== 204) throw setupHttpError(response);
      setDraft("");
      await onChanged();
      if (action === "delete") setNotice("deleted");
    } catch (reason) {
      setError(setupFeedback(reason, "saveKeyError", draft || undefined));
    } finally {
      setBusy(false);
      onBusyChange?.(false);
    }
  }

  return {
    locale,
    draft,
    hasKey,
    verified,
    busy,
    disabled,
    error: error ? setupFeedbackText(error, locale) : undefined,
    notice,
    onDraftChange: (value) => {
      setDraft(value);
      setError(null);
      setNotice(undefined);
    },
    onSave: () => {
      void mutate("save");
    },
    onDelete: () => {
      void mutate("delete");
    },
  };
}
