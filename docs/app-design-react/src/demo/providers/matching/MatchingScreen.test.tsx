import { fireEvent, render, screen, within } from "@testing-library/react";
import { useState } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { IncomingWorkspaceView } from "../../../ui/features/incoming/IncomingWorkspaceView";
import { I18nProvider, useI18n } from "../demoPreferences";
import {
  createIncomingState,
  type IncomingReleases,
  incomingFixtures,
} from "../incoming/incomingModel";
import type { CatalogArtist } from "../library/catalogData";
import {
  createLocalProviderState,
  localProviderReleases,
} from "../local-catalog/localProviderModel";
import {
  type MatchingScreenProps,
  useMatchingScreenDemoProvider,
} from "./useMatchingScreenDemoProvider";

function MatchingRoute(props: MatchingScreenProps) {
  return (
    <IncomingWorkspaceView matching={useMatchingScreenDemoProvider(props)} />
  );
}

const originalScrollIntoView = Object.getOwnPropertyDescriptor(
  HTMLElement.prototype,
  "scrollIntoView",
);
let previousLanguagePreference: string | null = null;

function MatchingHarness({
  artists,
  providers,
  releaseId,
  localReleaseId,
}: {
  artists?: CatalogArtist[];
  providers?: IncomingReleases;
  releaseId?: string;
  localReleaseId?: string;
} = {}) {
  const [state, setState] = useState(createIncomingState);
  return (
    <>
      <button
        type="button"
        onClick={() =>
          setState((current) => ({
            ...current,
            inventoryVersion: current.inventoryVersion + 1,
          }))
        }
      >
        Change inventory
      </button>
      <MatchingRoute
        state={state}
        onState={setState}
        artists={artists}
        providers={providers}
        releaseId={releaseId}
        localReleaseId={localReleaseId}
      />
    </>
  );
}

function LinkSaveHarness({
  initialReleases,
  providers,
  releaseId,
  onReleases,
}: {
  initialReleases: IncomingReleases;
  providers?: IncomingReleases;
  releaseId?: string;
  onReleases?: (releases: IncomingReleases) => void;
}) {
  const [state, setState] = useState(createIncomingState);
  const [releases, setReleases] = useState(initialReleases);
  return (
    <MatchingRoute
      state={state}
      onState={setState}
      releases={releases}
      providers={providers}
      releaseId={releaseId}
      onReleases={(next) => {
        setReleases(next);
        onReleases?.(next);
      }}
    />
  );
}

function publishedSnapshot(releases: IncomingReleases) {
  return JSON.stringify(
    releases.map((release) => ({
      id: release.id,
      tracks: release.tracks.map((track) => ({
        id: track.id,
        published: track.published,
      })),
    })),
  );
}

function LocaleSwitch() {
  const { setPreference } = useI18n();
  return (
    <button type="button" onClick={() => setPreference("en")}>
      Switch locale
    </button>
  );
}

it("switches between Local and MusicBrainz search results without losing the draft", async () => {
  render(
    <MatchingHarness
      providers={[
        ...incomingFixtures.providerReleases,
        ...localProviderReleases(createLocalProviderState()),
      ]}
    />,
  );
  const before = screen.getByRole("region", {
    name: "Позиции · Northern Lines",
  });
  const search = within(screen.getByRole("region", { name: "Поиск изданий" }));
  expect(search.queryByRole("list")).not.toBeInTheDocument();
  fireEvent.change(
    search.getByRole("textbox", { name: "Артист и название издания" }),
    { target: { value: "Northern Lines" } },
  );
  fireEvent.click(search.getByRole("button", { name: "Искать" }));
  const local = await search.findByRole("button", {
    name: /^Выбрать «Northern Lines» \(Local,/,
  });
  const remote = search.getAllByRole("button", {
    name: /^Выбрать «Northern Lines» \(MusicBrainz,/,
  })[0];
  expect(local).toHaveAccessibleName(/Исполнитель: Mira Vale.*Треков: 1\b/);
  expect(remote).toHaveAccessibleName(/Исполнитель: Mira Vale.*Треков: 11\b/);
  expect(search.getByRole("button", { pressed: true })).toHaveAccessibleName(
    /MusicBrainz/,
  );
  fireEvent.click(local);
  expect(
    screen.getByRole("region", {
      name: "Позиции · Northern Lines",
    }),
  ).toBe(before);
  expect(screen.getByText(/Назначено позиций: 0 из /)).toBeInTheDocument();
  fireEvent.click(
    screen.getByRole("button", { name: "Выбрать 01 - First Light.flac" }),
  );
  fireEvent.click(
    within(before).getAllByRole("button", {
      name: /^Добавить 01 - First Light\.flac к позиции/,
    })[0],
  );
  expect(screen.getByText(/Назначено позиций: 1 из /)).toBeInTheDocument();
  fireEvent.click(remote);
  expect(search.getByRole("button", { pressed: true })).toHaveAccessibleName(
    /MusicBrainz/,
  );
  fireEvent.click(
    search.getByRole("button", { name: /^Выбрать «Northern Lines» \(Local,/ }),
  );
  expect(search.getByRole("button", { pressed: true })).toHaveAccessibleName(
    /Local/,
  );
  expect(screen.getByText(/Назначено позиций: 1 из /)).toBeInTheDocument();
});

it("routes a provider edition and writes assignments into that edition's draft", () => {
  render(<MatchingHarness releaseId="p-japan" />);
  const positions = screen.getByRole("region", {
    name: "Позиции · Northern Lines",
  });
  expect(screen.getByText(/Назначено позиций: 0 из 11/)).toBeInTheDocument();
  fireEvent.click(
    screen.getByRole("button", { name: "Выбрать 01 - First Light.flac" }),
  );
  fireEvent.click(
    within(positions).getAllByRole("button", {
      name: /^Добавить 01 - First Light\.flac к позиции/,
    })[0],
  );
  expect(screen.getByText(/Назначено позиций: 1 из 11/)).toBeInTheDocument();
});

it("prefers an exact Local route and keeps its draft across a provider switch", () => {
  const localReleases = localProviderReleases(createLocalProviderState());
  const local = localReleases[0];
  if (!local) throw new Error("Missing Local matching fixture");
  const providers = [...incomingFixtures.providerReleases, ...localReleases];
  const { rerender } = render(
    <MatchingHarness
      providers={providers}
      releaseId="p-japan"
      localReleaseId={local.id}
    />,
  );
  const localPositions = screen.getByRole("region", {
    name: "Позиции · Northern Lines",
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Выбрать 01 - First Light.flac" }),
  );
  fireEvent.click(
    within(localPositions).getAllByRole("button", {
      name: /^Добавить 01 - First Light\.flac к позиции/,
    })[0],
  );
  expect(
    screen.getByText(
      new RegExp(`Назначено позиций: 1 из ${local.tracks.length}`),
    ),
  ).toBeInTheDocument();
  rerender(
    <MatchingHarness
      providers={providers}
      releaseId="p-japan"
      localReleaseId={undefined}
    />,
  );
  expect(screen.getByText(/Назначено позиций: 0 из 11/)).toBeInTheDocument();
  rerender(
    <MatchingHarness
      providers={providers}
      releaseId="p-japan"
      localReleaseId={local.id}
    />,
  );
  expect(
    screen.getByText(
      new RegExp(`Назначено позиций: 1 из ${local.tracks.length}`),
    ),
  ).toBeInTheDocument();
});

it("keeps search recoverable for an unknown Local route", () => {
  const localReleases = localProviderReleases(createLocalProviderState());
  render(
    <MatchingHarness
      providers={[...incomingFixtures.providerReleases, ...localReleases]}
      localReleaseId="missing-local-release"
    />,
  );
  expect(screen.getByRole("alert")).toHaveTextContent(
    "Цель сопоставления не найдена",
  );
  expect(
    screen.getByRole("region", { name: "Поиск изданий" }),
  ).toBeInTheDocument();
});

afterEach(() => {
  if (previousLanguagePreference === null)
    window.localStorage.removeItem("music-design-v2-language");
  else
    window.localStorage.setItem(
      "music-design-v2-language",
      previousLanguagePreference,
    );
  if (originalScrollIntoView)
    Object.defineProperty(
      HTMLElement.prototype,
      "scrollIntoView",
      originalScrollIntoView,
    );
  else delete (HTMLElement.prototype as Partial<HTMLElement>).scrollIntoView;
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

beforeEach(() => {
  previousLanguagePreference = window.localStorage.getItem(
    "music-design-v2-language",
  );
  window.localStorage.setItem("music-design-v2-language", "ru");
});

it("opens the link preview without replacing the selected pair inspector and focuses its heading on each open", () => {
  const scrollIntoView = vi.fn();
  Object.defineProperty(HTMLElement.prototype, "scrollIntoView", {
    configurable: true,
    value: scrollIntoView,
  });
  vi.stubGlobal(
    "matchMedia",
    vi.fn().mockReturnValue({ matches: true, addEventListener: vi.fn() }),
  );

  const { container } = render(<MatchingHarness />);
  const inspector = screen.getByRole("region", {
    name: "Доказательства текущей оценки",
  });
  const openPreview = () =>
    fireEvent.click(
      screen.getByRole("button", { name: "Предпросмотр назначений" }),
    );
  expect(screen.getByText(/Назначено файлов: 7/)).toBeInTheDocument();
  openPreview();
  expect(container.querySelector(".matching-commit")).not.toBeInTheDocument();
  expect(
    screen.getByRole("region", { name: "Доказательства текущей оценки" }),
  ).toBe(inspector);

  const heading = screen.getByRole("heading", {
    name: "Предпросмотр назначений",
  });
  expect(heading).toHaveFocus();
  expect(scrollIntoView).toHaveBeenLastCalledWith({
    behavior: "auto",
    block: "start",
  });
  expect(
    screen.queryByRole("region", { name: "Сравнить метаданные" }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByRole("checkbox", { name: /Включить/ }),
  ).not.toBeInTheDocument();
  expect(
    within(
      screen.getByRole("region", { name: "Предпросмотр назначений" }),
    ).getByText(/Назначено файлов: 7/),
  ).toBeInTheDocument();
  expect(
    screen.getByText(/Во входящих останутся файлы: 2/),
  ).toBeInTheDocument();

  fireEvent.click(screen.getByRole("button", { name: "Вернуться к объекту" }));
  expect(container.querySelector(".matching-commit")).toBeInTheDocument();
  openPreview();
  expect(
    screen.getByRole("heading", { name: "Предпросмотр назначений" }),
  ).toHaveFocus();
  expect(scrollIntoView).toHaveBeenCalledTimes(2);
});

it("reserves a bounded inspector grid row and keeps the target list independently scrollable on wide desktop", () => {
  const { container } = render(<MatchingHarness />);
  const main = container.querySelector<HTMLElement>("[data-matching-screen]");
  const layout = container.querySelector<HTMLElement>(
    "[data-matching-workspace]",
  );
  const inspector = container.querySelector<HTMLElement>(
    "[data-matching-inspector]",
  );
  const target = container.querySelector<HTMLElement>(
    "[data-matching-positions]",
  );
  const sources = container.querySelector<HTMLElement>(
    "[data-matching-sources]",
  );
  const search = container.querySelector<HTMLElement>(
    "[data-matching-candidates]",
  );
  expect(main?.className).toContain("min-[1400px]:h-full");
  expect(main?.className).toContain("min-[1400px]:flex-col");
  expect(layout?.className).toContain(
    "min-[1400px]:h-[clamp(520px,72dvh,880px)]",
  );
  expect(layout?.className).toContain("min-[1400px]:shrink-0");
  expect(layout?.className).toContain(
    "min-[1400px]:grid-rows-[minmax(0,1fr)_minmax(180px,min(32dvh,340px))]",
  );
  expect(inspector).not.toBeNull();
  expect(inspector?.parentElement).toBe(layout);
  expect(layout?.className).toContain(
    "[&>[data-matching-inspector]]:col-span-full",
  );
  expect(layout?.previousElementSibling).toBe(search);
  expect(inspector?.className).not.toContain("min-[1400px]:fixed");
  expect(target?.parentElement).toBe(layout);
  expect(sources?.parentElement).toBe(layout);
  expect(target?.className).toContain("min-[1400px]:overflow-y-auto");
  expect(target?.className).toContain("min-[1400px]:min-h-0");
});

it("does not move focus back to the preview after a locale change", () => {
  const scrollIntoView = vi.fn();
  Object.defineProperty(HTMLElement.prototype, "scrollIntoView", {
    configurable: true,
    value: scrollIntoView,
  });
  vi.stubGlobal(
    "matchMedia",
    vi.fn().mockReturnValue({ matches: false, addEventListener: vi.fn() }),
  );

  render(
    <I18nProvider>
      <LocaleSwitch />
      <MatchingHarness />
    </I18nProvider>,
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр назначений" }),
  );
  const heading = screen.getByRole("heading", {
    name: "Предпросмотр назначений",
  });
  const switchLocale = screen.getByRole("button", { name: "Switch locale" });
  switchLocale.focus();
  fireEvent.click(switchLocale);

  expect(switchLocale).toHaveFocus();
  expect(heading).not.toHaveFocus();
  expect(scrollIntoView).toHaveBeenCalledTimes(1);
});

it("blocks saving a stale link preview without a confirmation dialog", () => {
  render(<MatchingHarness />);
  const openPreview = () =>
    fireEvent.click(
      screen.getByRole("button", { name: "Предпросмотр назначений" }),
    );
  openPreview();

  expect(screen.getByRole("button", { name: "Сохранить связи" })).toBeEnabled();
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

  fireEvent.click(screen.getByRole("button", { name: "Change inventory" }));
  expect(screen.getByRole("alert")).toHaveTextContent("Предпросмотр устарел");
  expect(
    screen.getByRole("button", { name: "Сохранить связи" }),
  ).toBeDisabled();
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

  fireEvent.click(screen.getByRole("button", { name: "Вернуться к объекту" }));
  openPreview();
  fireEvent.click(screen.getByRole("button", { name: "Сохранить связи" }));
  expect(
    screen.getByRole("heading", { name: "Связи сопоставления сохранены" }),
  ).toHaveFocus();
  expect(
    screen.queryByRole("heading", { name: "Предпросмотр назначений" }),
  ).not.toBeInTheDocument();
});

it("saves only matching links and leaves published objects byte-identical", () => {
  const initialReleases = structuredClone(incomingFixtures.releases);
  const publishedBefore = publishedSnapshot(initialReleases);
  const projectNorth = incomingFixtures.releases.find(
    (release) => release.id === "north",
  );
  if (!projectNorth) throw new Error("Missing project north fixture");
  const providers = [...incomingFixtures.providerReleases, projectNorth];
  let savedReleases: IncomingReleases | undefined;

  const { rerender } = render(
    <LinkSaveHarness
      initialReleases={initialReleases}
      providers={providers}
      onReleases={(next) => {
        savedReleases = next;
      }}
    />,
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр назначений" }),
  );
  expect(
    screen.queryByRole("region", { name: "Сравнить метаданные" }),
  ).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Сохранить связи" }));

  expect(
    screen.getByRole("heading", { name: "Связи сопоставления сохранены" }),
  ).toHaveFocus();
  const remaining = screen.getByRole("region", {
    name: "Остались во входящих",
  });
  expect(
    within(remaining).getByText("session fragment.flac"),
  ).toBeInTheDocument();
  expect(
    within(remaining).getByText("03 - Northbound (live?).flac"),
  ).toBeInTheDocument();
  expect(publishedSnapshot(savedReleases ?? [])).toBe(publishedBefore);

  fireEvent.click(screen.getByRole("button", { name: "Проверить публикацию" }));
  expect(
    screen.getByRole("heading", { name: "Публикация по сохранённым связям" }),
  ).toHaveFocus();
  expect(
    screen.queryByRole("radio", { name: "Принудительная" }),
  ).not.toBeInTheDocument();
  expect(publishedSnapshot(savedReleases ?? [])).toBe(publishedBefore);
  fireEvent.click(screen.getByRole("button", { name: "К сохранённым связям" }));
  expect(
    screen.getByRole("button", { name: "Проверить публикацию" }),
  ).toHaveFocus();

  fireEvent.click(screen.getByRole("button", { name: "Проверить публикацию" }));
  expect(
    screen.getByRole("heading", { name: "Публикация по сохранённым связям" }),
  ).toHaveFocus();
  rerender(
    <LinkSaveHarness
      initialReleases={initialReleases}
      providers={providers}
      releaseId="north"
      onReleases={(next) => {
        savedReleases = next;
      }}
    />,
  );
  expect(
    screen.queryByRole("heading", {
      name: "Публикация по сохранённым связям",
    }),
  ).not.toBeInTheDocument();
  expect(
    screen.getByRole("heading", { name: "Связи сопоставления сохранены" }),
  ).toBeInTheDocument();

  fireEvent.click(
    screen.getByRole("button", { name: "Продолжить сопоставление" }),
  );
  expect(
    screen.getByRole("button", { name: "Выбрать session fragment.flac" }),
  ).toBeInTheDocument();
  expect(
    screen.getByRole("button", {
      name: "Выбрать 03 - Northbound (live?).flac",
    }),
  ).toBeInTheDocument();
});
