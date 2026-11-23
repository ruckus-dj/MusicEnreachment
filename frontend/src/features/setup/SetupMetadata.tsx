import {
  type RefObject,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
} from "react";
import { CheckboxButton, CheckboxField } from "react-aria-components";
import {
  checkMusicbrainz,
  saveSetupLrclib,
  saveSetupMusicbrainz,
} from "../../api/generated/client";
import type {
  SetupStateBody,
  UpdateMusicBrainzBodyMode,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../i18n/I18nProvider";
import { AcoustIDKeyForm } from "./AcoustIDKeyForm";
import { SetupMusicBrainzFields } from "./SetupMusicBrainzFields";
import {
  type SetupFeedback,
  setupFeedback,
  setupFeedbackText,
  setupHttpError,
  setupMessage,
  setupMessages,
} from "./setupMessages";

export interface SetupMetadataAction {
  continue: () => Promise<boolean>;
  isBusy: () => boolean;
}

type Props = {
  state: SetupStateBody;
  refresh: () => Promise<SetupStateBody>;
  onBusyChange?: (busy: boolean) => void;
  actionRef?: RefObject<SetupMetadataAction | null>;
  disabled?: boolean;
};
export function SetupMetadata({
  state,
  refresh,
  onBusyChange,
  actionRef,
  disabled,
}: Props) {
  const { locale } = useI18n();
  const messages = setupMessages[locale];
  const [mode, setMode] = useState<UpdateMusicBrainzBodyMode>(
    state.settings.musicbrainz_mode === "self-hosted"
      ? "self-hosted"
      : "public",
  );
  const [baseURL, setBaseURL] = useState(state.settings.musicbrainz_base_url);
  const [lrclib, setLrclib] = useState(state.settings.lrclib_enabled);
  const [pending, setPending] = useState(false);
  const [keyPending, setKeyPending] = useState(false);
  const [error, setError] = useState<SetupFeedback | null>(null);
  const submitting = useRef(false);
  const alert = useRef<HTMLParagraphElement>(null);
  const changed =
    mode !== state.settings.musicbrainz_mode ||
    (mode === "self-hosted" ? baseURL : "") !==
      state.settings.musicbrainz_base_url;
  useEffect(() => {
    if (error) alert.current?.focus();
  }, [error]);
  async function continueStep() {
    if (submitting.current || keyPending) return false;
    submitting.current = true;
    setPending(true);
    setError(null);
    try {
      const fresh = await refresh();
      const url = mode === "public" ? "" : baseURL;
      if (
        fresh.settings.musicbrainz_mode !== mode ||
        fresh.settings.musicbrainz_base_url !== url ||
        !fresh.settings.musicbrainz_verified_at
      ) {
        const saved = await saveSetupMusicbrainz({ mode, base_url: url });
        if (saved.status !== 204) throw setupHttpError(saved);
        const checked = await checkMusicbrainz();
        if (checked.status !== 200) throw setupHttpError(checked);
        if (!checked.data.success) {
          if (checked.data.error) throw new Error(checked.data.error);
          throw setupMessage("musicBrainzUnavailable");
        }
        const verified = await refresh();
        if (
          verified.settings.musicbrainz_mode !== mode ||
          verified.settings.musicbrainz_base_url !== url ||
          !verified.settings.musicbrainz_verified_at
        ) {
          throw setupMessage("saveAndCheckMusicBrainz");
        }
      }
      const response = await saveSetupLrclib({ enabled: lrclib });
      if (response.status !== 204) throw setupHttpError(response);
      await refresh();
      return true;
    } catch (reason) {
      setError(setupFeedback(reason, "unknownError"));
      return false;
    } finally {
      submitting.current = false;
      setPending(false);
    }
  }
  useImperativeHandle(actionRef, () => ({
    isBusy: () => submitting.current || keyPending,
    continue: continueStep,
  }));
  useEffect(() => {
    onBusyChange?.(pending || keyPending);
    return () => onBusyChange?.(false);
  }, [pending, keyPending, onBusyChange]);
  return (
    <div className="setup-metadata">
      <fieldset
        disabled={disabled || pending || keyPending}
        className="grid min-w-0 gap-2"
      >
        <SetupMusicBrainzFields
          locale={locale}
          mode={mode}
          baseURL={baseURL}
          onModeChange={setMode}
          onBaseURLChange={setBaseURL}
        />
        <CheckboxField isSelected={lrclib} onChange={setLrclib}>
          <CheckboxButton>{messages.lrclibEnabled}</CheckboxButton>
        </CheckboxField>
        <p>
          {changed
            ? messages.musicBrainzChanged
            : state.settings.musicbrainz_verified_at
              ? `${messages.musicBrainzCheckedAt}${state.settings.musicbrainz_verified_at}`
              : messages.musicBrainzNotChecked}
        </p>
        {state.platform.supported && !state.platform.diagnostic && (
          <AcoustIDKeyForm
            hasKey={state.settings.has_acoustid_application_key}
            onChanged={refresh}
            onBusyChange={setKeyPending}
          />
        )}
      </fieldset>
      {error && (
        <p role="alert" ref={alert} tabIndex={-1}>
          {setupFeedbackText(error, locale)}
        </p>
      )}
    </div>
  );
}
