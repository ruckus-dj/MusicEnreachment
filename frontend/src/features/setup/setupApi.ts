export function setupError(response: { status: number; data: unknown }): Error {
  const { data } = response;
  if (
    typeof data === "object" &&
    data !== null &&
    "detail" in data &&
    typeof data.detail === "string" &&
    data.detail
  ) {
    return new Error(data.detail);
  }
  return new Error(`Ошибка сервера (${response.status}).`);
}

export function errorMessage(reason: unknown): string {
  return reason instanceof Error ? reason.message : "Неизвестная ошибка.";
}
