import { describe, expect, it } from "vitest";
import { apiResponseError, errorMessage } from "./errors";

describe("shared error helpers", () => {
  it("uses Error messages and the supplied fallback for other values", () => {
    expect(errorMessage(new Error("specific failure"), "fallback")).toBe(
      "specific failure",
    );
    expect(errorMessage("not an Error", "fallback")).toBe("fallback");
  });

  it("uses a string API detail when present", () => {
    expect(
      apiResponseError({ status: 409, data: { detail: "Конфликт." } }).message,
    ).toBe("Конфликт.");
  });

  it("uses an optional API fallback only when no string detail is present", () => {
    expect(
      apiResponseError(
        { status: 503, data: { detail: 17 } },
        "Сервер вернул ошибку 503.",
      ).message,
    ).toBe("Сервер вернул ошибку 503.");
    expect(
      apiResponseError(
        { status: 503, data: { detail: "Есть детали." } },
        "Сервер вернул ошибку 503.",
      ).message,
    ).toBe("Есть детали.");
  });

  it("falls back to the status for null and malformed payloads", () => {
    expect(apiResponseError({ status: 503, data: null }).message).toBe(
      "Ошибка сервера (503).",
    );
    expect(
      apiResponseError({ status: 502, data: { detail: 42 } }).message,
    ).toBe("Ошибка сервера (502).");
    expect(
      apiResponseError({ status: 500, data: "not an object" }).message,
    ).toBe("Ошибка сервера (500).");
  });

  it("uses the status fallback when detail is empty or absent", () => {
    expect(
      apiResponseError({ status: 404, data: { detail: "" } }).message,
    ).toBe("Ошибка сервера (404).");
    expect(apiResponseError({ status: 401 }).message).toBe(
      "Ошибка сервера (401).",
    );
  });
});
