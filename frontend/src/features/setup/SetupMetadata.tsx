import { useEffect, useRef, useState } from "react";
import {
  CheckboxButton,
  CheckboxField,
  Input,
  Label,
  RadioButton,
  RadioField,
  RadioGroup,
  TextField,
} from "react-aria-components";
import {
  checkMusicbrainz,
  saveSetupLrclib,
  saveSetupMusicbrainz,
} from "../../api/generated/client";
import type {
  SetupStateBody,
  UpdateMusicBrainzBodyMode,
} from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import { useI18n } from "../../i18n/I18nProvider";
import { AcoustIDKeyForm } from "./AcoustIDKeyForm";
import {
  type SetupFeedback,
  setupFeedback,
  setupFeedbackText,
  setupHttpError,
  setupMessage,
  setupMessages,
} from "./setupMessages";

type Props = {
  state: SetupStateBody;
  refresh: () => Promise<SetupStateBody>;
  onContinue: () => void;
  onBack: () => void;
};
export function SetupMetadata({ state, refresh, onContinue, onBack }: Props) {
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
  const [notice, setNotice] = useState<SetupFeedback | null>(null);
  const alert = useRef<HTMLParagraphElement>(null);
  const changed =
    mode !== state.settings.musicbrainz_mode ||
    (mode === "self-hosted" ? baseURL : "") !==
      state.settings.musicbrainz_base_url;
  useEffect(() => {
    if (error) alert.current?.focus();
  }, [error]);
  async function check() {
    setPending(true);
    setError(null);
    setNotice(null);
    try {
      const url = mode === "public" ? "" : baseURL;
      if (
        state.settings.musicbrainz_mode !== mode ||
        state.settings.musicbrainz_base_url !== url ||
        !state.settings.musicbrainz_verified_at
      ) {
        const saved = await saveSetupMusicbrainz({ mode, base_url: url });
        if (saved.status !== 204) throw setupHttpError(saved);
      }
      const response = await checkMusicbrainz();
      if (response.status !== 200) throw setupHttpError(response);
      if (!response.data.success) {
        if (response.data.error) throw new Error(response.data.error);
        throw setupMessage("musicBrainzUnavailable");
      }
      await refresh();
      setNotice(setupMessage("musicBrainzVerified"));
    } catch (reason) {
      setError(setupFeedback(reason, "unknownError"));
    } finally {
      setPending(false);
    }
  }
  async function continueStep() {
    setPending(true);
    setError(null);
    setNotice(null);
    try {
      const fresh = await refresh();
      if (
        fresh.settings.musicbrainz_mode !== mode ||
        fresh.settings.musicbrainz_base_url !==
          (mode === "public" ? "" : baseURL) ||
        !fresh.settings.musicbrainz_verified_at
      )
        throw setupMessage("saveAndCheckMusicBrainz");
      const response = await saveSetupLrclib({ enabled: lrclib });
      if (response.status !== 204) throw setupHttpError(response);
      await refresh();
      onContinue();
    } catch (reason) {
      setError(setupFeedback(reason, "unknownError"));
    } finally {
      setPending(false);
    }
  }
  return (
    <div className="setup-metadata">
      <RadioGroup
        aria-label="MusicBrainz"
        value={mode}
        onChange={(value) =>
          setMode(value === "self-hosted" ? "self-hosted" : "public")
        }
      >
        <Label>{messages.musicBrainz}</Label>
        <RadioField value="public">
          <RadioButton>{messages.public}</RadioButton>
        </RadioField>
        <RadioField value="self-hosted">
          <RadioButton>{messages.selfHosted}</RadioButton>
        </RadioField>
      </RadioGroup>
      {mode === "self-hosted" && (
        <TextField value={baseURL} onChange={setBaseURL}>
          <Label>{messages.musicBrainzBaseUrl}</Label>
          <Input />
        </TextField>
      )}
      <CheckboxField isSelected={lrclib} onChange={setLrclib}>
        <CheckboxButton>{messages.lrclibEnabled}</CheckboxButton>
      </CheckboxField>
      <AppButton isDisabled={pending || keyPending} onPress={check}>
        {messages.checkMusicBrainz}
      </AppButton>
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
      {error && (
        <p role="alert" ref={alert} tabIndex={-1}>
          {setupFeedbackText(error, locale)}
        </p>
      )}
      {notice && <p role="status">{setupFeedbackText(notice, locale)}</p>}
      {pending && <p role="status">{messages.requestInProgress}</p>}
      <div className="setup-navigation">
        <AppButton isDisabled={pending || keyPending} onPress={onBack}>
          {messages.back}
        </AppButton>
        <AppButton isDisabled={pending || keyPending} onPress={continueStep}>
          {messages.continue}
        </AppButton>
      </div>
    </div>
  );
}
