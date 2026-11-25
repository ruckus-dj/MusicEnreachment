const libraryLoadErrorCopy = {
  en: {
    error: "Could not load the library.",
    httpError: "Could not load the library (HTTP {status}).",
  },
  ru: {
    error: "Не удалось загрузить медиатеку.",
    httpError: "Не удалось загрузить медиатеку (HTTP {status}).",
  },
} as const;

/** Localized message for a failed library catalog read. */
export function libraryCatalogLoadError(locale: "en" | "ru", status?: number) {
  const text = libraryLoadErrorCopy[locale];
  return status === undefined
    ? text.error
    : text.httpError.replace("{status}", String(status));
}
