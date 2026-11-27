import {
  type ReactNode,
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
import { useI18n } from "../../app/i18n/host";
import {
  SetupMetadataDataBinder,
  type SetupScreenMetadataStepView,
} from "../../ui/features/setup/SetupScreenView";
import {
  type SetupFeedback,
  setupFeedback,
  setupFeedbackText,
  setupHttpError,
  setupMessage,
} from "../../ui/features/setup/setupMessages";
import { useSetupAlertFocus } from "../../ui/features/setup/useSetupAlertFocus";
import { SetupAcoustIDBinder } from "./AcoustIDKeyForm";

export interface SetupMetadataAction {
  continue: () => Promise<boolean>;
  isBusy: () => boolean;
  /** Reads proof for this exact draft without saving or checking it. */
  verifiedIdentity: () => string | undefined;
}

export interface SetupMetadataDraft {
  mode: UpdateMusicBrainzBodyMode;
  baseURL: string;
  lrclib: boolean;
}

export interface SetupMusicBrainzFailure {
  mode: UpdateMusicBrainzBodyMode;
  baseURL: string;
  feedback: SetupFeedback;
}

type Props = {
  state: SetupStateBody;
  refresh: () => Promise<SetupStateBody>;
  initialDraft?: SetupMetadataDraft;
  onDraftChange?: (draft: SetupMetadataDraft) => void;
  verificationFailure: SetupMusicBrainzFailure | null;
  onVerificationFailure: (failure: SetupMusicBrainzFailure | null) => void;
  onBusyChange?: (busy: boolean) => void;
  onConfigurationDirtyChange?: (dirty: boolean) => void;
  actionRef?: RefObject<SetupMetadataAction | null>;
  disabled?: boolean;
  children: ReactNode;
};
export function SetupMetadataBinder({
  state,
  refresh,
  initialDraft,
  onDraftChange,
  verificationFailure,
  onVerificationFailure,
  onBusyChange,
  onConfigurationDirtyChange,
  actionRef,
  disabled,
  children,
}: Props) {
  const { locale } = useI18n();
  const [mode, setMode] = useState<UpdateMusicBrainzBodyMode>(
    initialDraft?.mode ??
      (state.settings.musicbrainz_mode === "self-hosted"
        ? "self-hosted"
        : "public"),
  );
  const [baseURL, setBaseURL] = useState(
    initialDraft?.baseURL ?? state.settings.musicbrainz_base_url,
  );
  const [lrclib, setLrclib] = useState(
    initialDraft?.lrclib ?? state.settings.lrclib_enabled,
  );
  const [pending, setPending] = useState(false);
  const [verificationPending, setVerificationPending] = useState(false);
  const [keyPending, setKeyPending] = useState(false);
  const [error, setError] = useState<SetupFeedback | null>(null);
  const submitting = useRef(false);
  const urlEdited = useRef(false);
  const alert = useRef<HTMLParagraphElement>(null);
  const changed =
    mode !== state.settings.musicbrainz_mode ||
    (mode === "self-hosted" ? baseURL : "") !==
      state.settings.musicbrainz_base_url;
  const verificationError =
    verificationFailure?.mode === mode &&
    verificationFailure.baseURL === (mode === "public" ? "" : baseURL)
      ? verificationFailure.feedback
      : null;
  const providerBlocked = changed || verificationError !== null;
  useSetupAlertFocus(alert, error);
  useEffect(() => {
    onConfigurationDirtyChange?.(providerBlocked);
  }, [providerBlocked, onConfigurationDirtyChange]);
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
  async function checkProvider(
    nextMode: UpdateMusicBrainzBodyMode,
    nextURL: string,
  ) {
    if (disabled || submitting.current || keyPending) return;
    let configurationSaved = false;
    const url = nextMode === "public" ? "" : nextURL;
    setVerificationPending(true);
    try {
      await withSubmission(async () => {
        const fresh = await refresh();
        let identity = fresh.musicbrainz_configuration_identity;
        if (
          fresh.settings.musicbrainz_mode !== nextMode ||
          fresh.settings.musicbrainz_base_url !== url ||
          !identity
        ) {
          const saved = await saveSetupMusicbrainz({
            mode: nextMode,
            base_url: url,
          });
          if (saved.status !== 200) throw setupHttpError(saved);
          identity = saved.data.configuration_identity;
          configurationSaved = true;
        }
        if (!identity) throw setupMessage("musicBrainzIdentityMissing");
        const checked = await checkSetupMusicbrainz({
          configuration_identity: identity,
        });
        if (checked.status !== 200) throw setupHttpError(checked);
        if (!checked.data.success) throw setupMessage("musicBrainzUnavailable");
        const verified = await refresh();
        if (
          verified.settings.musicbrainz_mode !== nextMode ||
          verified.settings.musicbrainz_base_url !== url ||
          !verified.settings.musicbrainz_verified_at ||
          verified.musicbrainz_configuration_identity !== identity
        ) {
          throw setupMessage("saveAndCheckMusicBrainz");
        }
      });
      onVerificationFailure(null);
    } catch (reason) {
      onVerificationFailure({
        mode: nextMode,
        baseURL: url,
        feedback: setupFeedback(reason, "musicBrainzUnavailable"),
      });
      if (configurationSaved) {
        // The saved endpoint changed even though verification did not pass.
        // Discard any earlier proof in the view, without masking the error.
        await refresh().catch(() => {});
      }
    } finally {
      setVerificationPending(false);
    }
  }
  function verifiedIdentity() {
    if (
      providerBlocked ||
      disabled ||
      submitting.current ||
      !state.settings.musicbrainz_verified_at
    )
      return undefined;
    return state.musicbrainz_configuration_identity || undefined;
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
    isBusy: () => submitting.current || verificationPending || keyPending,
    continue: continueStep,
    verifiedIdentity,
  }));
  useEffect(() => {
    onBusyChange?.(pending || verificationPending || keyPending);
    return () => onBusyChange?.(false);
  }, [pending, verificationPending, keyPending, onBusyChange]);
  function changeMode(next: UpdateMusicBrainzBodyMode) {
    urlEdited.current = false;
    setMode(next);
    onDraftChange?.({ mode: next, baseURL, lrclib });
    if (next === "public") void checkProvider(next, "");
  }
  function changeBaseURL(next: string) {
    urlEdited.current = true;
    setBaseURL(next);
    onDraftChange?.({ mode, baseURL: next, lrclib });
  }
  function changeLrclib(next: boolean) {
    setLrclib(next);
    onDraftChange?.({ mode, baseURL, lrclib: next });
  }
  const model: SetupScreenMetadataStepView = {
    providers: {
      locale,
      mode,
      baseURL,
      verification: changed
        ? "changed"
        : state.settings.musicbrainz_verified_at
          ? "verified"
          : "untested",
      verificationError:
        verificationError && !verificationPending
          ? setupFeedbackText(verificationError, locale)
          : undefined,
      lrclib: { enabled: lrclib, onChange: changeLrclib },
      disabled: disabled || pending || verificationPending || keyPending,
      verificationPending,
      onVerify: () => {
        urlEdited.current = false;
        void checkProvider(mode, baseURL);
      },
      onBaseURLBlur: () => {
        if (!urlEdited.current || !baseURL.trim()) return;
        urlEdited.current = false;
        void checkProvider(mode, baseURL);
      },
      onModeChange: changeMode,
      onBaseURLChange: changeBaseURL,
    },
    acoustID:
      state.platform.supported && !state.platform.diagnostic
        ? {
            Component: SetupAcoustIDBinder,
            props: {
              hasKey: state.settings.has_acoustid_application_key,
              verified: state.settings.acoustid_application_key_verified,
              onChanged: refresh,
              onBusyChange: setKeyPending,
              disabled: disabled || pending,
            },
          }
        : undefined,
    error: error
      ? { text: setupFeedbackText(error, locale), errorRef: alert }
      : undefined,
  };
  return (
    <SetupMetadataDataBinder model={model}>{children}</SetupMetadataDataBinder>
  );
}
