import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll } from "vitest";

export const server = setupServer();

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => {
  server.resetHandlers();
  server.events.removeAllListeners();
});
afterAll(() => server.close());

// Arm before the action. Vitest's test timeout bounds every await.
export function nextResponse() {
  return new Promise<{ request: Request; response: Response }>((resolve) => {
    const listener = ({
      request,
      response,
    }: {
      request: Request;
      response: Response;
    }) => {
      server.events.removeListener("response:mocked", listener);
      resolve({ request: request.clone(), response: response.clone() });
    };
    server.events.on("response:mocked", listener);
  });
}
