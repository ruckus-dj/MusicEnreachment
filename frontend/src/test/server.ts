import { cleanup } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll } from "vitest";
import type { SourceArtifactCleanupCandidatesBody } from "../api/generated/client.schemas";

// Baseline handlers live on the shared server so resetHandlers() restores them
// after every test. Suites that need other behavior call server.use(...), which
// takes priority for that test only.
export const defaultHandlers = [
  // SourcesScreen mounts SourceArtifactCleanupPanel, which reads candidates on
  // first render; the empty baseline keeps unrelated tests off the error path.
  http.get("/api/source-analysis/artifacts/cleanup", () =>
    HttpResponse.json<SourceArtifactCleanupCandidatesBody>({
      candidates: [],
      count: 0,
    }),
  ),
];

export const server = setupServer(...defaultHandlers);

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => {
  cleanup();
  server.resetHandlers();
  server.events.removeAllListeners();
});
afterAll(() => server.close());

// Arm before the action and fail promptly if the matching response never arrives.
export function nextResponse(pathname?: string, method?: string) {
  return new Promise<{ request: Request; response: Response }>(
    (resolve, reject) => {
      const listener = ({
        request,
        response,
      }: {
        request: Request;
        response: Response;
      }) => {
        if (
          (pathname !== undefined &&
            new URL(request.url).pathname !== pathname) ||
          (method !== undefined && request.method !== method)
        )
          return;
        clearTimeout(timeout);
        server.events.removeListener("response:mocked", listener);
        resolve({
          request: request.bodyUsed ? request : request.clone(),
          response: response.clone(),
        });
      };
      const timeout = setTimeout(() => {
        server.events.removeListener("response:mocked", listener);
        reject(
          new Error(
            `Missing mocked response${method ? ` for ${method}` : ""}${pathname ? ` ${pathname}` : ""}`,
          ),
        );
      }, 3000);
      server.events.on("response:mocked", listener);
    },
  );
}
