import { expect, it } from "vitest";
import { errorAfterSnapshotRead } from "./IncomingOperationFollower";

it("clears a REST read error after a manual refresh of an unchanged snapshot", () => {
  expect(
    errorAfterSnapshotRead(
      { kind: "rest", message: "snapshot unavailable" },
      "refresh",
    ),
  ).toBeUndefined();
});

it("clears a stream disconnect after an unchanged snapshot is read on reconnect", () => {
  expect(
    errorAfterSnapshotRead(
      { kind: "stream", message: "stream disconnected" },
      "stream",
    ),
  ).toBeUndefined();
});

it("keeps a stream disconnect notice through a successful REST refresh", () => {
  const disconnected = {
    kind: "stream" as const,
    message: "stream disconnected",
  };
  expect(errorAfterSnapshotRead(disconnected, "refresh")).toBe(disconnected);
});
