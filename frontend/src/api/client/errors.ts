export function apiResponseError(
  response: {
    status: number;
    data?: unknown;
  },
  fallback?: string,
): Error {
  const data = response.data;
  const detail =
    typeof data === "object" &&
    data !== null &&
    "detail" in data &&
    typeof data.detail === "string"
      ? data.detail
      : "";
  return new Error(
    detail || fallback || `Ошибка сервера (${response.status}).`,
  );
}

export function errorMessage(reason: unknown, fallback: string): string {
  return reason instanceof Error ? reason.message : fallback;
}
