import type { RuntimeSettings, RuntimeSettingsDraft, RuntimeSettingsPayload } from "../types";

/**
 * Baseline for every `PUT /api/settings` field. `RuntimeSettingsRequest` has no defaults on the
 * server, so a save is only accepted when the complete object is sent. Drafts are always merged
 * with these values to keep the payload whole even if a response is missing an optional field.
 */
export const runtimeSettingsDefaults: RuntimeSettingsDraft = {
  confidence_threshold: 0.7,
  timeout_seconds: 10,
  retry_delay_seconds: 30,
  max_attempts: 3,
  worker_concurrency: 1,
  musicbrainz_enabled: true,
  musicbrainz_user_agent: "music-ingest/0.1.0 (music-ingest@example.com)",
  musicbrainz_host: "https://musicbrainz.org",
  musicbrainz_request_delay_seconds: 1.5,
  acoustid_enabled: false,
  acoustid_request_delay_seconds: 1 / 3,
  acoustid_client_key: "",
  artwork_enabled: true,
  lrclib_enabled: true,
  lrclib_host: "https://lrclib.net",
  lrclib_user_agent: "music-ingest/0.1.0 (music-ingest@example.com)",
  lrclib_timeout_seconds: 15,
  lrclib_max_attempts: 3,
  lrclib_request_delay_seconds: 0.3,
  lrclib_max_response_bytes: 4 * 1024 * 1024,
  lrclib_match_confidence_threshold: 0.7,
};

/** Builds the editable draft from a GET response, filling absent fields from the defaults. */
export function runtimeSettingsDraftFrom(settings: RuntimeSettings): RuntimeSettingsDraft {
  return { ...runtimeSettingsDefaults, ...settings, acoustid_client_key: "" };
}

/**
 * Builds the save payload by enumerating every required field, so no call site can send a partial
 * settings object. The typed literal makes a forgotten field a compile error.
 */
export function runtimeSettingsPayload(draft: RuntimeSettingsDraft): RuntimeSettingsPayload {
  return {
    confidence_threshold: draft.confidence_threshold,
    timeout_seconds: draft.timeout_seconds,
    retry_delay_seconds: draft.retry_delay_seconds,
    max_attempts: draft.max_attempts,
    worker_concurrency: draft.worker_concurrency,
    musicbrainz_enabled: draft.musicbrainz_enabled,
    musicbrainz_user_agent: draft.musicbrainz_user_agent,
    musicbrainz_host: draft.musicbrainz_host,
    musicbrainz_request_delay_seconds: draft.musicbrainz_request_delay_seconds,
    acoustid_enabled: draft.acoustid_enabled,
    acoustid_request_delay_seconds: draft.acoustid_request_delay_seconds,
    acoustid_client_key: draft.acoustid_client_key || null,
    artwork_enabled: draft.artwork_enabled,
    lrclib_enabled: draft.lrclib_enabled,
    lrclib_host: draft.lrclib_host,
    lrclib_user_agent: draft.lrclib_user_agent,
    lrclib_timeout_seconds: draft.lrclib_timeout_seconds,
    lrclib_max_attempts: draft.lrclib_max_attempts,
    lrclib_request_delay_seconds: draft.lrclib_request_delay_seconds,
    lrclib_max_response_bytes: draft.lrclib_max_response_bytes,
    lrclib_match_confidence_threshold: draft.lrclib_match_confidence_threshold,
  };
}

export type RuntimeSettingsFieldErrors = Readonly<
  Partial<Record<keyof RuntimeSettingsDraft, string>>
>;

function inRange(value: number, minimum: number, maximum: number): boolean {
  return Number.isFinite(value) && value >= minimum && value <= maximum;
}

/**
 * Mirrors the LRCLIB constraints of `RuntimeSettingsRequest` so the operator learns about a
 * rejected value before the API answers with 422. Host is HTTPS-only, as on the server.
 */
export function runtimeSettingsFieldErrors(
  draft: RuntimeSettingsDraft,
): RuntimeSettingsFieldErrors {
  const errors: Partial<Record<keyof RuntimeSettingsDraft, string>> = {};
  if (!/^https:\/\/[^/?#]+$/.test(draft.lrclib_host))
    errors.lrclib_host = "Хост LRCLIB: укажите адрес вида https://lrclib.net без пути.";
  if (draft.lrclib_user_agent.trim().length === 0 || draft.lrclib_user_agent.length > 255)
    errors.lrclib_user_agent = "User-Agent LRCLIB обязателен и не длиннее 255 символов.";
  if (!inRange(draft.lrclib_timeout_seconds, Number.MIN_VALUE, 120))
    errors.lrclib_timeout_seconds = "Таймаут LRCLIB: от 0 до 120 секунд.";
  if (!Number.isInteger(draft.lrclib_max_attempts) || !inRange(draft.lrclib_max_attempts, 1, 10))
    errors.lrclib_max_attempts = "Максимум попыток LRCLIB: от 1 до 10.";
  if (!inRange(draft.lrclib_request_delay_seconds, 0, 3600))
    errors.lrclib_request_delay_seconds = "Задержка запросов LRCLIB: от 0 до 3600 секунд.";
  if (
    !Number.isInteger(draft.lrclib_max_response_bytes) ||
    !inRange(draft.lrclib_max_response_bytes, 1024, 16 * 1024 * 1024)
  )
    errors.lrclib_max_response_bytes = "Размер ответа LRCLIB: от 1024 до 16777216 байт.";
  if (!inRange(draft.lrclib_match_confidence_threshold, 0, 1))
    errors.lrclib_match_confidence_threshold = "Порог совпадения LRCLIB: от 0 до 1.";
  return errors;
}
