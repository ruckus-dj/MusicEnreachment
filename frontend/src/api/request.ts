import { errorMessages } from "../errorMessages";

export class ApiError extends Error {
  readonly status: number;

  constructor(
    status: number,
    message: string,
    readonly detail: unknown = null,
  ) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

export async function api<T>(path: string, options?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    headers: { "content-type": "application/json" },
    ...options,
  });
  const payload: unknown = await response.json().catch(() => ({}));
  if (!response.ok) {
    const detail =
      typeof payload === "object" && payload !== null && "detail" in payload
        ? payload.detail
        : null;
    throw new ApiError(
      response.status,
      typeof detail === "string" ? detail : errorMessages.requestFailed,
      detail,
    );
  }
  return payload as T;
}
