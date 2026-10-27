import { apiResponseError } from "./errors";

export function successful<T extends { status: number }, S extends T["status"]>(
  response: T,
  status: S,
): Extract<T, { status: S }> {
  if (response.status !== status) throw apiResponseError(response);
  return response as Extract<T, { status: S }>;
}
