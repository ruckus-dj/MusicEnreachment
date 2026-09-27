const apiBaseURL = "/api";

export async function apiFetch(
  path: string,
  init?: RequestInit,
): Promise<Response> {
  return fetch(`${apiBaseURL}${path}`, {
    ...init,
    headers: { Accept: "application/json", ...init?.headers },
  });
}
