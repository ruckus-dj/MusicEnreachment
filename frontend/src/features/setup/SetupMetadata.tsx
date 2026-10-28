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
import { apiResponseError, errorMessage } from "../../api/client/errors";
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
import { AcoustIDKeyForm } from "./AcoustIDKeyForm";

type Props = {
  state: SetupStateBody;
  refresh: () => Promise<SetupStateBody>;
  onContinue: () => void;
  onBack: () => void;
};
export function SetupMetadata({ state, refresh, onContinue, onBack }: Props) {
  const [mode, setMode] = useState<UpdateMusicBrainzBodyMode>(
    state.settings.musicbrainz_mode === "self-hosted"
      ? "self-hosted"
      : "public",
  );
  const [baseURL, setBaseURL] = useState(state.settings.musicbrainz_base_url);
  const [lrclib, setLrclib] = useState(state.settings.lrclib_enabled);
  const [pending, setPending] = useState(false);
  const [keyPending, setKeyPending] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
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
    setError("");
    setNotice("");
    try {
      const url = mode === "public" ? "" : baseURL;
      if (
        state.settings.musicbrainz_mode !== mode ||
        state.settings.musicbrainz_base_url !== url ||
        !state.settings.musicbrainz_verified_at
      ) {
        const saved = await saveSetupMusicbrainz({ mode, base_url: url });
        if (saved.status !== 204) throw apiResponseError(saved);
      }
      const response = await checkMusicbrainz();
      if (response.status !== 200) throw apiResponseError(response);
      if (!response.data.success)
        throw new Error(response.data.error || "MusicBrainz недоступен.");
      await refresh();
      setNotice("MusicBrainz проверен.");
    } catch (reason) {
      setError(errorMessage(reason, "Неизвестная ошибка."));
    } finally {
      setPending(false);
    }
  }
  async function continueStep() {
    setPending(true);
    setError("");
    setNotice("");
    try {
      const fresh = await refresh();
      if (
        fresh.settings.musicbrainz_mode !== mode ||
        fresh.settings.musicbrainz_base_url !==
          (mode === "public" ? "" : baseURL) ||
        !fresh.settings.musicbrainz_verified_at
      )
        throw new Error(
          "Сохраните и проверьте MusicBrainz после изменения конфигурации.",
        );
      const response = await saveSetupLrclib({ enabled: lrclib });
      if (response.status !== 204) throw apiResponseError(response);
      await refresh();
      onContinue();
    } catch (reason) {
      setError(errorMessage(reason, "Неизвестная ошибка."));
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
        <Label>MusicBrainz</Label>
        <RadioField value="public">
          <RadioButton>Public</RadioButton>
        </RadioField>
        <RadioField value="self-hosted">
          <RadioButton>Self-hosted</RadioButton>
        </RadioField>
      </RadioGroup>
      {mode === "self-hosted" && (
        <TextField value={baseURL} onChange={setBaseURL}>
          <Label>MusicBrainz base URL</Label>
          <Input />
        </TextField>
      )}
      <CheckboxField isSelected={lrclib} onChange={setLrclib}>
        <CheckboxButton>LRCLIB включён</CheckboxButton>
      </CheckboxField>
      <AppButton isDisabled={pending || keyPending} onPress={check}>
        Проверить MusicBrainz
      </AppButton>
      <p>
        {changed
          ? "Конфигурация изменена. Проверьте MusicBrainz снова."
          : state.settings.musicbrainz_verified_at
            ? `Проверено: ${state.settings.musicbrainz_verified_at}`
            : "Соединение ещё не проверено."}
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
          {error}
        </p>
      )}
      {notice && <p role="status">{notice}</p>}
      {pending && <p role="status">Выполняется запрос…</p>}
      <div className="setup-navigation">
        <AppButton isDisabled={pending || keyPending} onPress={onBack}>
          Назад
        </AppButton>
        <AppButton isDisabled={pending || keyPending} onPress={continueStep}>
          Продолжить
        </AppButton>
      </div>
    </div>
  );
}
