import { act, renderHook } from "@testing-library/react";
import { useState } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { SetupScreenMetadataStepView } from "../../../ui/features/setup/SetupScreenView";
import { I18nProvider } from "../demoPreferences";
import { musicBrainzConfigurationFingerprint } from "../settings/providerValidation";
import {
  type DemoSettings,
  initialDemoSettings,
} from "../settings/SettingsScreen";
import { demoGenreCatalogLoadDelayMs } from "./useSetupGenreCatalogDemoProvider";
import {
  demoMusicBrainzCheckDelayMs,
  type SetupScreenProps,
  useSetupScreenDemoProvider,
} from "./useSetupScreenDemoProvider";

function renderSetup(
  initial: Partial<DemoSettings> = {},
  props: Partial<SetupScreenProps> = {},
) {
  const onSettings = vi.fn();
  const view = renderHook(
    () => {
      const [settings, setSettings] = useState<DemoSettings>({
        ...initialDemoSettings,
        ...initial,
      });
      return useSetupScreenDemoProvider({
        settings,
        onSettings: (next) => {
          onSettings(next);
          setSettings(next);
        },
        step: 2,
        ...props,
      });
    },
    { wrapper: I18nProvider },
  );
  return { view, onSettings };
}

beforeEach(() => {
  localStorage.setItem("music-design-v2-language", "en");
});
afterEach(() => vi.useRealTimers());

function metadata(view: ReturnType<typeof renderSetup>["view"]) {
  return view.result.current.publication
    .metadata as SetupScreenMetadataStepView;
}

function acoustID(view: ReturnType<typeof renderSetup>["view"]) {
  const source = metadata(view).acoustID;
  if (!source || "Component" in source)
    throw new Error("Missing AcoustID form");
  return source;
}

function summary(view: ReturnType<typeof renderSetup>["view"]) {
  const source = view.result.current.summary;
  if (!source || "Component" in source) throw new Error("Missing summary");
  return source;
}

it("keeps a restored catalog unready without the separate connection proof", () => {
  const { view } = renderSetup(
    { musicBrainzMode: "public" },
    { initialGenreCatalogEndpoint: "public" },
  );

  const providers = metadata(view).providers;
  expect(providers.verification).toBe("untested");
  expect(view.result.current.publication.genres.state).toBe("idle");
  expect(view.result.current.publication.genres.disabled).toBe(true);
  expect(view.result.current.publication.genres.requiresVerification).toBe(
    true,
  );

  act(() => view.result.current.publication.genres.onLoad());
  expect(view.result.current.publication.genres.state).toBe("idle");
});

it("checks Public automatically and only then loads the genre catalog", () => {
  vi.useFakeTimers();
  const { view } = renderSetup({ musicBrainzMode: "" });

  act(() => metadata(view).providers.onModeChange("public"));
  expect(metadata(view).providers.verificationPending).toBe(true);
  expect(view.result.current.publication.genres.disabled).toBe(true);

  act(() => vi.advanceTimersByTime(demoMusicBrainzCheckDelayMs));
  expect(metadata(view).providers.verification).toBe("verified");
  expect(metadata(view).providers.verificationPending).toBe(false);
  expect(view.result.current.publication.genres.disabled).toBe(false);
  expect(view.result.current.publication.genres.requiresVerification).toBe(
    false,
  );

  act(() => view.result.current.publication.genres.onLoad());
  expect(view.result.current.publication.genres.state).toBe("loading");
  act(() => vi.advanceTimersByTime(demoGenreCatalogLoadDelayMs));
  expect(view.result.current.publication.genres.state).toBe("ready");
});

it("checks an edited self-hosted URL on blur and reports invalid input", () => {
  vi.useFakeTimers();
  const { view } = renderSetup({
    musicBrainzMode: "self-hosted",
    musicBrainzURL: "https://initial.example.org/ws/2",
  });
  const providers = () => metadata(view).providers;

  // Blur without an edit does not spend a check.
  act(() => providers().onBaseURLBlur?.());
  expect(providers().verificationPending).toBe(false);

  act(() =>
    providers().onBaseURLChange("https://musicbrainz.example.org/ws/2"),
  );
  expect(providers().verification).toBe("changed");
  act(() => providers().onBaseURLBlur?.());
  expect(providers().verificationPending).toBe(true);

  act(() => vi.advanceTimersByTime(demoMusicBrainzCheckDelayMs));
  expect(providers().verification).toBe("verified");
  expect(providers().verificationError).toBeUndefined();

  act(() => providers().onBaseURLChange("not-a-url"));
  expect(providers().verification).toBe("changed");
  expect(view.result.current.publication.genres.disabled).toBe(true);
  act(() => providers().onBaseURLBlur?.());
  expect(providers().verificationError).toBeTruthy();
  expect(providers().verificationPending).toBe(false);
});

it("does not earn connection proof by continuing the wizard", () => {
  const { view, onSettings } = renderSetup({
    musicBrainzMode: "public",
    outputCandidate: "/srv/music",
    outputChecked: true,
  });

  act(() => view.result.current.wizard.onNext());
  expect(view.result.current.wizard.notice).toMatch(/Could not verify/);
  expect(metadata(view).providers.verification).toBe("untested");
  expect(onSettings).not.toHaveBeenCalled();
});

it("restores persisted proof for the exact configuration on reload", () => {
  const { view } = renderSetup(
    {
      musicBrainzMode: "public",
      musicBrainzVerified: true,
      musicBrainzVerifiedFingerprint: musicBrainzConfigurationFingerprint({
        mode: "public",
      }),
    },
    { initialGenreCatalogEndpoint: "public" },
  );

  expect(metadata(view).providers.verification).toBe("verified");
  expect(view.result.current.publication.genres.state).toBe("ready");
  expect(view.result.current.publication.genres.disabled).toBe(false);
  expect(view.result.current.publication.genres.requiresVerification).toBe(
    false,
  );
});

it("rejects persisted proof for a different configuration", () => {
  const { view } = renderSetup(
    {
      musicBrainzMode: "self-hosted",
      musicBrainzURL: "https://musicbrainz.example.org/ws/2",
      musicBrainzVerified: true,
      musicBrainzVerifiedFingerprint: musicBrainzConfigurationFingerprint({
        mode: "public",
      }),
    },
    {
      initialGenreCatalogEndpoint:
        "self-hosted|https://musicbrainz.example.org/ws/2",
    },
  );

  expect(metadata(view).providers.verification).toBe("untested");
  expect(view.result.current.publication.genres.state).toBe("idle");
  expect(view.result.current.publication.genres.disabled).toBe(true);
});

it("shows no AcoustID tick for a saved key that was never checked", () => {
  const { view } = renderSetup({ hasAcoustIDApplicationKey: true });

  expect(acoustID(view).hasKey).toBe(true);
  expect(acoustID(view).verified).toBe(false);
});

it("mocks a successful AcoustID check on save and clears it afterwards", () => {
  const { view, onSettings } = renderSetup({
    hasAcoustIDApplicationKey: true,
    acoustIDVerified: true,
  });

  act(() => acoustID(view).onDraftChange("write-only-key"));
  expect(acoustID(view).draft).toBe("write-only-key");

  act(() => acoustID(view).onSave());
  expect(onSettings).toHaveBeenLastCalledWith(
    expect.objectContaining({
      hasAcoustIDApplicationKey: true,
      acoustIDVerified: true,
    }),
  );
  expect(acoustID(view).draft).toBe("");
  expect(acoustID(view).verified).toBe(true);

  // An unrelated settings edit keeps the already proven key.
  act(() => view.result.current.publication.options.onFormat("m4a"));
  expect(acoustID(view).verified).toBe(true);
  expect(onSettings).toHaveBeenLastCalledWith(
    expect.objectContaining({
      hasAcoustIDApplicationKey: true,
      acoustIDVerified: true,
    }),
  );

  act(() => acoustID(view).onDelete());
  expect(onSettings).toHaveBeenLastCalledWith(
    expect.objectContaining({
      hasAcoustIDApplicationKey: false,
      acoustIDVerified: false,
    }),
  );
  expect(acoustID(view).verified).toBe(false);
});

it("keeps the auto-complete albums draft tab-local until Continue commits it", () => {
  const onDraftChange = vi.fn();
  const { view, onSettings } = renderSetup(
    {
      pathChecked: true,
      selectedRelease: { ffmpeg: "8.0", fpcalc: "1.5.1" },
      setupVerified: { ffmpeg: "8.0", fpcalc: "1.5.1" },
      outputCandidate: "/srv/music",
      outputChecked: true,
      publicationFormat: "mka",
      musicBrainzMode: "public",
      musicBrainzVerified: true,
      musicBrainzVerifiedFingerprint: musicBrainzConfigurationFingerprint({
        mode: "public",
      }),
    },
    { onDraftChange, initialGenreCatalogEndpoint: "public" },
  );

  expect(view.result.current.publication.options.autoCompleteAlbums).toBe(
    false,
  );
  expect(summary(view).values.publication.autoCompleteAlbums).toBe(false);

  act(() => view.result.current.publication.options.onAutoCompleteAlbums(true));
  expect(onDraftChange).toHaveBeenLastCalledWith({
    publicationAutoCompleteAlbums: true,
  });
  expect(onSettings).not.toHaveBeenCalled();
  expect(view.result.current.publication.options.autoCompleteAlbums).toBe(true);
  expect(summary(view).values.publication.autoCompleteAlbums).toBe(false);

  act(() => view.result.current.wizard.onNext());
  expect(onSettings).toHaveBeenCalledTimes(1);
  expect(onSettings).toHaveBeenLastCalledWith(
    expect.objectContaining({ publicationAutoCompleteAlbums: true }),
  );
  expect(view.result.current.wizard.step).toBe(3);
  expect(summary(view).values.publication.autoCompleteAlbums).toBe(true);
});

it("restores an explicit false auto-complete draft over the committed setting", () => {
  const { view } = renderSetup(
    { publicationAutoCompleteAlbums: true },
    { initialDraft: { publicationAutoCompleteAlbums: false } },
  );

  expect(view.result.current.publication.options.autoCompleteAlbums).toBe(
    false,
  );
  expect(summary(view).values.publication.autoCompleteAlbums).toBe(true);
});

it("blocks a direct Summary route while the publication choice is uncommitted", () => {
  const ready = {
    pathChecked: true,
    selectedRelease: { ffmpeg: "8.0", fpcalc: "1.5.1" },
    setupVerified: { ffmpeg: "8.0", fpcalc: "1.5.1" },
    outputCandidate: "/srv/music",
    outputChecked: true,
    publicationFormat: "mka" as const,
    musicBrainzMode: "public" as const,
    musicBrainzVerified: true,
    musicBrainzVerifiedFingerprint: musicBrainzConfigurationFingerprint({
      mode: "public",
    }),
  };
  const clean = renderSetup(ready, {
    step: 3,
    stepSync: true,
    initialGenreCatalogEndpoint: "public",
  });
  expect(clean.view.result.current.wizard.step).toBe(3);
  clean.view.unmount();

  const dirty = renderSetup(ready, {
    step: 3,
    stepSync: true,
    initialGenreCatalogEndpoint: "public",
    initialDraft: { publicationAutoCompleteAlbums: true },
  });
  expect(dirty.view.result.current.wizard.step).toBe(2);
});
