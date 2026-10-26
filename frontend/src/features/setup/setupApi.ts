import {
  apiResponseError,
  errorMessage as sharedErrorMessage,
} from "../../api/client/errors";

export function setupError(response: { status: number; data: unknown }): Error {
  return apiResponseError(response);
}

export function errorMessage(reason: unknown): string {
  return sharedErrorMessage(reason, "Неизвестная ошибка.");
}
