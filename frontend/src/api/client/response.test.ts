import { describe, expect, it } from "vitest";
import { successful } from "./response";

describe("successful API response", () => {
  it("returns the original expected response, including empty successes", () => {
    const response = { status: 204 as const, data: undefined };
    expect(successful(response, 204)).toBe(response);
  });

  it("retains the payload and narrows a response union", () => {
    const response:
      | { status: 200; data: string }
      | { status: 409; data: null } = { status: 200, data: "result" };
    expect(successful(response, 200).data).toBe("result");
  });

  it("reports server detail and preserves the shared status fallback", () => {
    expect(() =>
      successful({ status: 409, data: { detail: "Конфликт." } }, 200),
    ).toThrow("Конфликт.");
    expect(() => successful({ status: 503, data: null }, 200)).toThrow(
      "Ошибка сервера (503).",
    );
  });
});
