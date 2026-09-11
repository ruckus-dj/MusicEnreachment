import type { LyricsStatus } from "../types";

export type LyricsTone = "ready" | "pending" | "error";

export type LyricsDisplay = {
  readonly tone: LyricsTone;
  readonly glyph: string;
  readonly label: string;
  readonly detail: string;
};

const LYRICS_DISPLAY: Readonly<Record<LyricsStatus, LyricsDisplay>> = {
  synced: {
    tone: "ready",
    glyph: "♪",
    label: "Синхронный текст найден",
    detail: "Строки с таймингом сохранены для этой записи и войдут в публикацию.",
  },
  pending: {
    tone: "pending",
    glyph: "…",
    label: "Поиск текста ещё не завершён",
    detail: "Обработка текста идёт в фоне, состояние обновится автоматически.",
  },
  none: {
    tone: "pending",
    glyph: "—",
    label: "Текст не запрашивался",
    detail: "Для этой записи поиск синхронного текста ещё не запускался.",
  },
  no_candidate: {
    tone: "pending",
    glyph: "—",
    label: "Подходящий текст не найден",
    detail: "Ни один источник не предложил синхронный текст для этой записи.",
  },
  validation_rejected: {
    tone: "error",
    glyph: "!",
    label: "Текст отклонён проверкой",
    detail: "Найденный текст не прошёл проверку и не будет опубликован.",
  },
  error: {
    tone: "error",
    glyph: "!",
    label: "Ошибка получения текста",
    detail: "Источник текста недоступен или вернул ошибку. Повторите обработку позже.",
  },
};

export const LYRICS_SYNCED_LABEL = "Синхронный текст: есть";
export const LYRICS_MISSING_LABEL = "Синхронный текст: нет";

export function lyricsDisplay(status: LyricsStatus | null | undefined): LyricsDisplay {
  if (status === null || status === undefined) return LYRICS_DISPLAY.none;
  return LYRICS_DISPLAY[status] ?? LYRICS_DISPLAY.none;
}

export function lyricsSynced(status: LyricsStatus | null | undefined, synced: boolean): boolean {
  return synced || status === "synced";
}

export function lyricsListLabel(synced: boolean): string {
  return synced ? LYRICS_SYNCED_LABEL : LYRICS_MISSING_LABEL;
}
