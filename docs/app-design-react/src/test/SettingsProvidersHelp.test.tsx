import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import {
  type SettingsProvidersActions,
  SettingsProvidersSection,
  type SettingsProvidersSnapshot,
} from "../ui/features/settings/SettingsProvidersSection";

const noop = () => {};

const actions: SettingsProvidersActions = {
  onAcoustIDApplicationKeyChange: noop,
  onSaveAcoustIDApplicationKey: noop,
  onDeleteAcoustIDApplicationKey: noop,
  onMusicBrainzModeChange: noop,
  onMusicBrainzBaseURLChange: noop,
  onMusicBrainzThrottleChange: noop,
  onMusicBrainzDelaySecondsChange: noop,
  onSaveAndCheckMusicBrainz: noop,
  onLrclibEnabledChange: noop,
};

function snapshot(): SettingsProvidersSnapshot {
  return {
    busy: false,
    acoustID: { applicationKey: "", hasKey: false },
    musicBrainz: {
      mode: "self-hosted",
      baseURL: "https://mb.example.org",
      throttle: true,
      delaySeconds: "1",
      verifiedAt: null,
      dirty: false,
    },
    lrclib: { enabled: false, dirty: false },
  };
}

describe("SettingsProvidersSection frame", () => {
  it("fills the settings tab and keeps its help copy readable", () => {
    const { container } = render(
      <SettingsProvidersSection
        locale="en"
        snapshot={snapshot()}
        actions={actions}
      />,
    );
    const panel = container.querySelector("section");
    expect(panel).not.toBeNull();
    expect(panel).not.toHaveClass("max-w-3xl");
    expect(
      screen.getByText(
        /Valid mode changes and the LRCLIB switch save automatically\./,
      ),
    ).toHaveClass("max-w-3xl");
  });
});

describe.each([
  "en",
  "ru",
] as const)("MusicBrainz manual save help (%s)", (locale) => {
  it("keeps the Save-and-check rule visible for URL and interval", () => {
    render(
      <SettingsProvidersSection
        locale={locale}
        snapshot={snapshot()}
        actions={actions}
      />,
    );

    const hint =
      locale === "en"
        ? /The URL and request interval are saved only by Save and check MusicBrainz\./
        : /URL и интервал запросов сохраняются только кнопкой «Сохранить и проверить MusicBrainz»\./;
    expect(screen.getByText(hint)).toBeVisible();

    const url = screen.getByLabelText(
      locale === "en" ? "MusicBrainz base URL" : "Базовый URL MusicBrainz",
    );
    const interval = screen.getByLabelText(
      locale === "en"
        ? "Request interval (seconds)"
        : "Интервал запросов (секунды)",
    );
    expect(url).toHaveAccessibleDescription(hint);
    expect(interval).toHaveAccessibleDescription(hint);
  });
});
