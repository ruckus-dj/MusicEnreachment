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

  return {
    locale,
    draft,
    hasKey,
    busy,
    disabled,
    error: error ? setupFeedbackText(error, locale) : undefined,
    notice,
    onDraftChange: setDraft,
    onSave: () => {
      void mutate("save");
    },
    onDelete: () => {
      void mutate("delete");
    },
  };
}
