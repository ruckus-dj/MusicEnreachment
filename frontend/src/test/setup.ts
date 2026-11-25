import "@testing-library/jest-dom/vitest";
import "../app/i18n/host";
import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

afterEach(() => {
  cleanup();
});
