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
import { errorMessage, setupError } from "./setupApi";

type Props = {
  state: SetupStateBody;
  refresh: () => Promise<SetupStateBody>;
  onContinue: () => void;
};
export function SetupMetadata({ state, refresh, onContinue }: Props) {
  const [mode, setMode] = useState<UpdateMusicBrainzBodyMode>(
    state.settings.musicbrainz_mode === "self-hosted"
      ? "self-hosted"
      : "public",
  );
  const [baseURL, setBaseURL] = useState(state.settings.musicbrainz_base_url);
  const [lrclib, setLrclib] = useState(state.settings.lrclib_enabled);
  const [pending, setPending] = useState(false);
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
        if (saved.status !== 204) throw setupError(saved);
      }
      const response = await checkMusicbrainz();
      if (response.status !== 200) throw setupError(response);
      if (!response.data.success)
        throw new Error(response.data.error || "MusicBrainz недоступен.");
      await refresh();
      setNotice("MusicBrainz проверен.");
    } catch (reason) {
      setError(errorMessage(reason));
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
      if (response.status !== 204) throw setupError(response);
      await refresh();
      onContinue();
    } catch (reason) {
      setError(errorMessage(reason));
    } finally {
      setPending(false);
    }
  }
  return (
    <div>
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
      <AppButton isDisabled={pending} onPress={check}>
        Проверить MusicBrainz
      </AppButton>
      <p>
        {changed
          ? "Конфигурация изменена. Проверьте MusicBrainz снова."
          : state.settings.musicbrainz_verified_at
            ? `Проверено: ${state.settings.musicbrainz_verified_at}`
            : "Соединение ещё не проверено."}
      </p>
      <AppButton isDisabled={pending} onPress={continueStep}>
        Продолжить
      </AppButton>
      {error && (
        <p role="alert" ref={alert} tabIndex={-1}>
          {error}
        </p>
      )}
      {notice && <p role="status">{notice}</p>}
      {pending && <p role="status">Выполняется запрос…</p>}
    </div>
  );
}
