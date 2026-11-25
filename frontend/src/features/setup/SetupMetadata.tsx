import {
  type RefObject,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
} from "react";
import {
  checkSetupMusicbrainz,
  saveSetupLrclib,
  saveSetupMusicbrainz,
} from "../../api/generated/client";
import type {
  SetupStateBody,
  UpdateMusicBrainzBodyMode,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../i18n/I18nProvider";
import { AcoustIDKeyForm } from "./AcoustIDKeyForm";
import { SetupMetadataStepBody } from "./SetupScreenView";
import {
  type SetupFeedback,
  setupFeedback,
  setupFeedbackText,
  setupHttpError,
  setupMessage,
} from "./setupMessages";

export interface SetupMetadataAction {
  continue: () => Promise<boolean>;
  isBusy: () => boolean;
  /** Verifies the configured provider when needed and returns its identity. */
  ensureProvider: () => Promise<string>;
}

type Props = {
  state: SetupStateBody;
  refresh: () => Promise<SetupStateBody>;
  onBusyChange?: (busy: boolean) => void;
  onConfigurationDirtyChange?: (dirty: boolean) => void;
  actionRef?: RefObject<SetupMetadataAction | null>;
  disabled?: boolean;
};
export function SetupMetadata({
  state,
  refresh,
  onBusyChange,
  onConfigurationDirtyChange,
  actionRef,
  disabled,
}: Props) {
  const { locale } = useI18n();
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
  useEffect(() => {
    onConfigurationDirtyChange?.(changed);
  }, [changed, onConfigurationDirtyChange]);
  useEffect(
    () => () => onConfigurationDirtyChange?.(false),
    [onConfigurationDirtyChange],
  );
  async function withSubmission<T>(work: () => Promise<T>): Promise<T> {
    if (submitting.current || keyPending)
      throw setupMessage("requestInProgress");
    submitting.current = true;
    setPending(true);
    setError(null);
    try {
      return await work();
    } finally {
      submitting.current = false;
      setPending(false);
    }
  }
  async function verifyConfiguredProvider() {
    const fresh = await refresh();
    const url = mode === "public" ? "" : baseURL;
    if (
      fresh.settings.musicbrainz_mode !== mode ||
      fresh.settings.musicbrainz_base_url !== url ||
      !fresh.settings.musicbrainz_verified_at ||
      !fresh.musicbrainz_configuration_identity
    ) {
      const saved = await saveSetupMusicbrainz({ mode, base_url: url });
      if (saved.status !== 200) throw setupHttpError(saved);
      const identity = saved.data.configuration_identity;
      if (!identity) throw setupMessage("musicBrainzIdentityMissing");
      const checked = await checkSetupMusicbrainz({
        configuration_identity: identity,
      });
      if (checked.status !== 200) throw setupHttpError(checked);
      if (!checked.data.success) {
        if (checked.data.error) throw new Error(checked.data.error);
        throw setupMessage("musicBrainzUnavailable");
      }
      const verified = await refresh();
      if (
        verified.settings.musicbrainz_mode !== mode ||
        verified.settings.musicbrainz_base_url !== url ||
        !verified.settings.musicbrainz_verified_at ||
        verified.musicbrainz_configuration_identity !== identity
      ) {
        throw setupMessage("saveAndCheckMusicBrainz");
      }
      return identity;
    }
    const identity = fresh.musicbrainz_configuration_identity;
    if (!identity) throw setupMessage("musicBrainzIdentityMissing");
    return identity;
  }
  async function ensureProvider() {
    if (disabled) throw setupMessage("requestInProgress");
    // A retry after a catalog failure reuses the already verified provider:
    // saving or checking it again would only replace the identity unnecessarily.
    return withSubmission(verifyConfiguredProvider);
  }
  async function continueStep() {
    if (submitting.current || keyPending) return false;
    try {
      await withSubmission(async () => {
        const response = await saveSetupLrclib({ enabled: lrclib });
        if (response.status !== 204) throw setupHttpError(response);
      });
      return true;
    } catch (reason) {
      setError(setupFeedback(reason, "unknownError"));
      return false;
    }
  }
  useImperativeHandle(actionRef, () => ({
    isBusy: () => submitting.current || keyPending,
    continue: continueStep,
    ensureProvider,
  }));
  useEffect(() => {
    onBusyChange?.(pending || keyPending);
    return () => onBusyChange?.(false);
  }, [pending, keyPending, onBusyChange]);
  return (
    <SetupMetadataStepBody
      providers={{
        locale,
        mode,
        baseURL,
        verification: changed
          ? "changed"
          : state.settings.musicbrainz_verified_at
            ? "verified"
            : "untested",
        verifiedAt: state.settings.musicbrainz_verified_at,
        lrclib: { enabled: lrclib, onChange: setLrclib },
        disabled: disabled || pending || keyPending,
        onModeChange: setMode,
        onBaseURLChange: setBaseURL,
      }}
      acoustIDMounted={
        state.platform.supported && !state.platform.diagnostic ? (
          <AcoustIDKeyForm
            hasKey={state.settings.has_acoustid_application_key}
            onChanged={refresh}
            onBusyChange={setKeyPending}
            disabled={disabled || pending}
          />
        ) : undefined
      }
      error={
        error
          ? { text: setupFeedbackText(error, locale), errorRef: alert }
          : undefined
      }
    />
  );
}
