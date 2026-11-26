import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { I18nProvider } from "../demoPreferences";
import { initialDemoSettings } from "../settings/SettingsScreen";
import { SetupScreen } from "./SetupScreen";
import { useSetupToolDownloadsDemoProvider } from "./useSetupToolDownloadsDemoProvider";

const conflict = "/var/lib/melotrove/tools/ffmpeg/8.0.1/ffmpeg";

function setup(selectedRelease: string) {
  const start = vi.fn();
  render(
    <I18nProvider>
      <SetupScreen
        settings={{
          ...initialDemoSettings,
          selectedRelease: { ffmpeg: selectedRelease },
        }}
        onSettings={vi.fn()}
        step={1}
        toolActivity={{ start }}
      />
    </I18nProvider>,
  );
  return start;
}

beforeEach(() => {
  localStorage.setItem("music-design-v2-language", "en");
  window.location.hash = "";
});
afterEach(() => vi.restoreAllMocks());

it("renders an admitted tool download again when the host supplies its snapshot", () => {
  const operation = {
    id: "fixture-install",
    source: {
      locale: "en" as const,
      status: {
        text: "Tool download — FFmpeg, version 8.0: Running",
        stage: "Downloading files",
        progress: "25 / 100 bytes",
      },
    },
  };
  const onSettings = vi.fn();
  const element = (
    <I18nProvider>
      <SetupScreen
        settings={initialDemoSettings}
        onSettings={onSettings}
        step={1}
        toolActivity={{ operations: [operation] }}
      />
    </I18nProvider>
  );
  const progress = () =>
    screen.getByText(/Running · Downloading files · 25 \/ 100 bytes/);
  const before = render(element);
  expect(progress()).toHaveAttribute("role", "status");
  before.unmount();
  render(element);
  expect(progress()).toHaveAttribute("role", "status");
  expect(onSettings).not.toHaveBeenCalled();
});

it("queues a clean download and marks the exact release ready only when it settles", () => {
  function Harness() {
    const { settings, setSettings, activity } =
      useSetupToolDownloadsDemoProvider({
        ...initialDemoSettings,
        selectedRelease: { ffmpeg: "8.0" },
      });
    return (
      <SetupScreen
        settings={settings}
        onSettings={setSettings}
        toolActivity={activity}
        step={1}
      />
    );
  }
  vi.useFakeTimers();
  try {
    render(
      <I18nProvider>
        <Harness />
      </I18nProvider>,
    );
    const action = () => screen.getByRole("button", { name: "Install FFmpeg" });
    fireEvent.click(action());
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(action()).toBeDisabled();
    expect(screen.getByText(/version 8\.0: Queued/)).toBeVisible();
    act(() => vi.advanceTimersByTime(400));
    expect(
      screen.getByText(/version 8\.0: Running · Downloading files/),
    ).toBeVisible();
    expect(screen.queryByText(/bytes/)).not.toBeInTheDocument();
    act(() => vi.advanceTimersByTime(800));
    expect(screen.getByText(/version 8\.0: Completed/)).toBeVisible();
    expect(screen.queryByRole("button", { name: "Install FFmpeg" })).toBeNull();
  } finally {
    vi.useRealTimers();
  }
});

it("shows exact preflight conflicts and confirms or cancels the retained plan", async () => {
  const start = setup("8.0.1");
  fireEvent.click(screen.getByRole("button", { name: "Install FFmpeg" }));
  const dialog = await screen.findByRole("dialog", {
    name: "Overwrite only these files:",
  });
  expect(within(dialog).getByText(conflict)).toBeVisible();
  expect(start).not.toHaveBeenCalled();
  fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(start).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Install FFmpeg" }));
  fireEvent.click(
    within(
      await screen.findByRole("dialog", {
        name: "Overwrite only these files:",
      }),
    ).getByRole("button", { name: "Confirm overwrite" }),
  );
  expect(start).toHaveBeenCalledExactlyOnceWith(
    expect.objectContaining({
      kind: "ffmpeg",
      release: "8.0.1",
      toolsRoot: initialDemoSettings.toolsPath,
      conflicts: [conflict],
    }),
  );
});
