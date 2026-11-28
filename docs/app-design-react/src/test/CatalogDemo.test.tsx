import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import {
  DemoMeloTroveProviderRefresh,
  demoMeloTroveProvider,
} from "../demo/demoMeloTroveProvider";
import type { DemoLibraryProviderRefresh } from "../demo/providers/library/useDemoLibraryProviderRefresh";
import { MeloTroveUI } from "../ui/app/MeloTroveUI";
import { firstPublicationPreviewMessages } from "../ui/features/library/firstPublicationPreviewMessages";

function CatalogDemo({
  providerRefresh,
}: {
  providerRefresh?: DemoLibraryProviderRefresh;
}) {
  return (
    <DemoMeloTroveProviderRefresh value={providerRefresh}>
      <MeloTroveUI provider={demoMeloTroveProvider} />
    </DemoMeloTroveProviderRefresh>
  );
}

const northernReleaseHref = "#/library/releases/north";
const northernPositionHref = "#/library/releases/north/positions/north-t1";
const signalsPositionHref = "#/library/releases/signals/positions/signals-t1";

afterEach(() => {
  window.history.replaceState(
    null,
    "",
    "#/library?view=artists&publication=all&availability=all",
  );
});

it("renders one keyboard-operable four-option filter radio group", () => {
  window.history.replaceState(
    null,
    "",
    "#/library?view=artists&publication=all&availability=all",
  );
  render(<CatalogDemo />);
  const group = screen.getByRole("radiogroup", { name: "Состояние" });
  const labels = ["Все", "Не хватает", "В наличии", "Опубликовано"];
  const radios = labels.map((name) =>
    within(group).getByRole("radio", { name }),
  );
  expect(screen.getAllByRole("radiogroup")).toHaveLength(1);
  expect(radios[0]).toBeChecked();
  radios[0].focus();
  fireEvent.keyDown(radios[0], { key: "ArrowRight" });
  expect(radios[1]).toBeChecked();
  fireEvent.keyDown(radios[1], { key: "ArrowRight" });
  expect(radios[2]).toBeChecked();
  fireEvent.keyDown(radios[2], { key: "ArrowRight" });
  expect(radios[3]).toBeChecked();
});

it("opens Incoming from the root when the fixture has registered sources", () => {
  window.history.replaceState(null, "", "#/");
  render(<CatalogDemo />);
  expect(window.location.hash).toBe("#/incoming-groups");
});

it("opens the exact release destination from an album row", () => {
  window.history.replaceState(
    null,
    "",
    "#/library?view=albums&publication=all&availability=all",
  );
  render(<CatalogDemo />);
  const grid = screen.getByRole("grid", { name: "Альбомы медиатеки" });
  const links = within(grid).getAllByRole("link", {
    name: "Открыть релиз: Northern Lines",
  });
  expect(
    links.some((link) => link.getAttribute("href") === northernReleaseHref),
  ).toBe(true);
});

it("restores the artist query and filter from the canonical address", () => {
  window.history.replaceState(
    null,
    "",
    "#/library/artists/mira?view=artists&q=Mira&publication=published&availability=all",
  );
  render(<CatalogDemo />);
  const search = screen.getByDisplayValue("Mira");
  expect(search).toBeInTheDocument();
  search.focus();
  expect(screen.getByRole("radio", { name: "Опубликовано" })).toBeChecked();

  act(() => {
    window.history.replaceState(
      null,
      "",
      "#/library/artists/mira?view=artists&q=Arden&publication=all&availability=missing",
    );
    window.dispatchEvent(new HashChangeEvent("hashchange"));
  });
  expect(screen.getByDisplayValue("Arden")).toHaveFocus();
  expect(screen.getByRole("radio", { name: "Не хватает" })).toBeChecked();
  expect(window.location.hash).toBe(
    "#/library/artists/mira?view=artists&q=Arden&publication=all&availability=missing",
  );
});

it("keeps a copied exact offset instead of snapping to the page start", () => {
  const hash =
    "#/library?view=artists&publication=all&availability=all&offset=1";
  window.history.replaceState(null, "", hash);
  render(<CatalogDemo />);
  expect(
    screen.queryByRole("link", { name: /Mira Vale/ }),
  ).not.toBeInTheDocument();
  expect(screen.getByRole("link", { name: /Arden Trio/ })).toBeInTheDocument();
  expect(window.location.hash).toBe(hash);
});

it("renders the focused release destination without artist management", () => {
  window.history.replaceState(null, "", northernReleaseHref);
  const view = render(<CatalogDemo />);
  expect(
    screen.getByRole("heading", { name: "Northern Lines" }),
  ).toBeInTheDocument();
  expect(
    screen.getByRole("heading", { name: "Носители и позиции" }),
  ).toBeInTheDocument();
  expect(
    screen.queryByRole("heading", { name: "Сведения об артисте" }),
  ).not.toBeInTheDocument();
  view.unmount();

  window.history.replaceState(
    null,
    "",
    "#/library/releases/hours/positions/north-t1",
  );
  render(<CatalogDemo />);
  expect(
    screen.getByRole("heading", { name: "Small Hours" }),
  ).toBeInTheDocument();
  expect(screen.getByRole("alert")).toHaveTextContent(
    "Позиция не найдена в этом релизе.",
  );
  expect(screen.getByRole("link", { name: "← Полный релиз" })).toHaveAttribute(
    "href",
    "#/library/releases/hours",
  );
});

it("retains an unsaved release title draft across a position visit", async () => {
  window.history.replaceState(null, "", northernReleaseHref);
  render(<CatalogDemo />);
  const editor = screen.getByRole("region", { name: "Метаданные релиза" });
  fireEvent.click(within(editor).getByRole("button", { name: "Изменить" }));
  fireEvent.change(
    within(editor).getByRole("combobox", {
      name: "Название — Состояние переопределения",
    }),
    { target: { value: "value" } },
  );
  const title = editor.querySelector<HTMLInputElement>(
    'input[data-metadata-keys~="album"]',
  );
  if (!title) throw new Error("Missing release title field");
  fireEvent.change(title, { target: { value: "Northern Lines draft" } });
  const trackLink = screen
    .getAllByRole("link", { name: /First Light/ })
    .find((link) => link.getAttribute("href") === northernPositionHref);
  if (!trackLink) throw new Error("Missing Northern Lines position link");
  fireEvent.click(trackLink);
  await screen.findByRole("heading", { level: 1, name: /First Light$/ });
  expect(window.location.hash).toBe(northernPositionHref);

  window.history.back();
  await screen.findByRole("heading", { name: "Northern Lines" });
  expect(
    await screen.findByDisplayValue("Northern Lines draft"),
  ).toBeInTheDocument();
});

it("shows a read-only preview of the selected library position", async () => {
  window.history.replaceState(
    null,
    "",
    "#/library?view=tracks&q=First%20Light&publication=all&availability=all",
  );
  render(<CatalogDemo />);
  const grid = screen.getByRole("grid", { name: "Треки медиатеки" });
  const track = within(grid)
    .getAllByRole("link", { name: "First Light" })
    .find((link) => link.getAttribute("href") === northernPositionHref)
    ?.closest("tr");
  if (!track) throw new Error("Missing Northern Lines position");
  const checkbox = within(track).getByRole("checkbox", {
    name: "Выбрать трек First Light",
  });
  fireEvent.click(checkbox);
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр публикации" }),
  );
  const dialog = await screen.findByRole("dialog", {
    name: "Предпросмотр публикации",
  });
  expect(within(dialog).getByRole("listitem")).toHaveTextContent(
    "First Light · Northern Lines",
  );
  const close = within(dialog).getByRole("button", { name: "Закрыть" });
  expect(within(dialog).getAllByRole("button")).toEqual([close]);
  fireEvent.click(close);
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  expect(checkbox).toBeChecked();
});

it("shows the published track status before opening its file workspace", () => {
  window.history.replaceState(
    null,
    "",
    "#/library/releases/signals/positions/signals-t5",
  );
  render(<CatalogDemo />);

  const overview = screen.getByRole("tabpanel", { name: "Обзор" });
  expect(screen.getAllByText("Опубликовано")[0]).toBeVisible();
  expect(within(overview).getByText("05 - Static.flac")).toBeVisible();
  expect(within(overview).getByText("05 - Static.mka")).toBeVisible();
  expect(
    screen.queryByRole("region", { name: "Метаданные записи" }),
  ).not.toBeInTheDocument();

  fireEvent.click(screen.getByRole("tab", { name: "Файлы и публикация" }));
  expect(screen.getByRole("region", { name: "Публикация" })).toBeVisible();
});

it("keeps independent position and recording drafts while switching scopes and tabs", () => {
  window.history.replaceState(null, "", `${northernPositionHref}?tab=metadata`);
  render(<CatalogDemo />);

  const position = () =>
    screen.getByRole("region", { name: "Метаданные позиции" });
  const recording = () =>
    screen.getByRole("region", { name: "Метаданные записи" });
  const positionScope = () =>
    screen.getByRole("radio", { name: /позиция в издании|только этот трек/i });
  const recordingScope = () =>
    screen.getByRole("radio", { name: "Запись во всех связанных изданиях" });
  const titleInput = (editor: HTMLElement) =>
    editor.querySelector<HTMLInputElement>(
      'input[data-metadata-keys~="title"]',
    );
  const editTitle = (editor: HTMLElement, value: string) => {
    fireEvent.click(within(editor).getByRole("button", { name: "Изменить" }));
    fireEvent.change(
      within(editor).getByRole("combobox", {
        name: "Название — Состояние переопределения",
      }),
      { target: { value: "value" } },
    );
    const input = titleInput(editor);
    if (!input) throw new Error("Missing title field");
    fireEvent.change(input, { target: { value } });
  };

  fireEvent.click(positionScope());
  expect(
    screen.queryByRole("region", { name: "Жанры записи" }),
  ).not.toBeInTheDocument();
  editTitle(position(), "Position-only draft");
  fireEvent.click(recordingScope());
  expect(screen.getByRole("region", { name: "Жанры записи" })).toBeVisible();
  expect(
    screen.queryByRole("region", { name: "Метаданные позиции" }),
  ).toBeNull();
  expect(titleInput(recording())?.value).not.toBe("Position-only draft");
  editTitle(recording(), "Recording-only draft");

  fireEvent.click(screen.getByRole("tab", { name: "Обзор" }));
  fireEvent.click(screen.getByRole("tab", { name: "Метаданные" }));
  expect(titleInput(recording())?.value).toBe("Recording-only draft");
  fireEvent.click(positionScope());
  expect(titleInput(position())?.value).toBe("Position-only draft");
});

it("separates comparison from republishing and keeps provider refresh across routes", async () => {
  window.history.replaceState(null, "", signalsPositionHref);
  let failRefresh: (() => void) | undefined;
  const refresh = vi.fn(
    () =>
      new Promise<{ state: "failed"; safeError: string }>((resolve) => {
        failRefresh = () =>
          resolve({ state: "failed", safeError: "Provider unavailable" });
      }),
  );
  render(<CatalogDemo providerRefresh={{ signals: refresh }} />);
  fireEvent.click(screen.getByRole("tab", { name: "Файлы и публикация" }));
  const publication = within(
    screen.getByRole("region", { name: "Публикация" }),
  );
  fireEvent.click(
    publication.getByRole("button", { name: "Сравнить с текущей" }),
  );
  const comparison = within(
    screen.getByRole("dialog", { name: "Сравнение с текущей публикацией" }),
  );
  expect(window.location.hash).toBe(`${signalsPositionHref}?tab=publication`);
  expect(
    comparison.queryByRole("button", { name: /^Опубликовать/ }),
  ).toBeNull();
  fireEvent.click(comparison.getByRole("button", { name: "Закрыть" }));
  expect(screen.queryByRole("dialog")).toBeNull();

  const prepare = () =>
    screen.getByRole("button", { name: "Подготовить повторную публикацию" });
  fireEvent.click(prepare());
  expect(
    within(screen.getByRole("dialog")).getByRole("button", {
      name: "Опубликовать повторно",
    }),
  ).toBeEnabled();
  act(() => {
    window.location.hash = signalsPositionHref;
  });
  await screen.findByRole("tab", { name: "Обзор", selected: true });
  expect(screen.queryByRole("dialog")).toBeNull();
  fireEvent.click(screen.getByRole("tab", { name: "Файлы и публикация" }));

  fireEvent.click(
    screen.getByRole("button", { name: "Обновить данные MusicBrainz" }),
  );
  const expectPending = async () =>
    expect(
      await screen.findByRole("button", {
        name: "Обновление MusicBrainz выполняется…",
      }),
    ).toBeDisabled();
  await expectPending();
  act(() => {
    window.location.hash = "#/library/releases/signals";
  });
  await screen.findByRole("heading", { name: "Signals", level: 1 });
  await expectPending();
  act(() => {
    window.location.hash = signalsPositionHref;
  });
  await act(async () => failRefresh?.());
  expect(await screen.findByText("Provider unavailable")).toBeVisible();
  expect(refresh).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("tab", { name: "Файлы и публикация" }));
  expect(prepare()).toBeEnabled();
});

it("omits demo publication shortcuts and keeps the position preview contextual", () => {
  const hash = northernPositionHref;
  window.history.replaceState(null, "", hash);
  render(<CatalogDemo />);
  fireEvent.click(screen.getByRole("tab", { name: "Файлы и публикация" }));
  const publication = within(
    screen.getByRole("region", { name: "Публикация" }),
  );
  expect(
    publication.queryByRole("button", { name: "Обновить метаданные" }),
  ).toBeNull();
  const otherActions = publication.getByText("Другие действия");
  const details = otherActions.closest("details");
  if (!details) throw new Error("Missing secondary publication actions");
  expect(details.open).toBe(false);
  details.open = true;
  expect(
    within(details).getByRole("button", { name: "Удалить публикацию" }),
  ).toBeVisible();
  const copy = firstPublicationPreviewMessages.ru;
  expect(
    publication.queryByRole("region", { name: copy.tagDifferences }),
  ).not.toBeInTheDocument();
  fireEvent.click(
    publication.getByRole("button", { name: "Сравнить с текущей" }),
  );
  const dialog = within(screen.getByRole("dialog"));
  const disclosure = dialog
    .getByText("Технические детали и теги")
    .closest("details");
  if (!disclosure) throw new Error("Missing comparison details");
  expect(disclosure.open).toBe(false);
  disclosure.open = true;
  expect(
    dialog.getByRole("region", { name: copy.tagDifferences }),
  ).toBeInTheDocument();
  expect(dialog.queryByRole("button", { name: /^Опубликовать/ })).toBeNull();
  expect(window.location.hash).toBe(`${hash}?tab=publication`);
});

it("retains matching search and preparation while creating a Local release", async () => {
  const matchingHash =
    "#/incoming-groups/north/matching?release=p-north&prepare=p-japan";
  window.history.replaceState(null, "", matchingHash);
  render(<CatalogDemo />);
  const search = within(screen.getByRole("region", { name: "Поиск изданий" }));
  fireEvent.change(
    search.getByRole("textbox", { name: "Артист и название издания" }),
    { target: { value: "Northern Lines" } },
  );
  fireEvent.click(search.getByRole("button", { name: "Искать" }));
  await search.findByRole("button", {
    name: /^Выбрать «Northern Lines» \(MusicBrainz,/,
  });
  fireEvent.change(
    screen.getByRole("textbox", {
      name: "MBID или официальная ссылка на издание",
    }),
    { target: { value: "p-north-draft" } },
  );

  await act(async () => {
    fireEvent.click(screen.getByRole("link", { name: "Создать свой релиз" }));
  });
  expect(
    await screen.findByRole("heading", { name: "Создать свой релиз" }),
  ).toBeInTheDocument();

  expect(window.location.hash).toBe(
    "#/incoming-groups/north/matching/local-release?release=p-north&prepare=p-japan",
  );
  const matchingScreen = document.querySelector<HTMLElement>(
    "[data-matching-screen]",
  );
  if (!matchingScreen) throw new Error("Missing matching screen");
  expect(matchingScreen).toHaveAttribute("hidden");
  expect(
    screen.getByRole("heading", { name: "Создать свой релиз" }),
  ).toHaveFocus();
  expect(
    screen.queryByRole("textbox", { name: "Артист и название издания" }),
  ).not.toBeInTheDocument();
  expect(
    within(matchingScreen).getByDisplayValue("Northern Lines"),
  ).toBeInTheDocument();
  expect(
    within(matchingScreen).getByDisplayValue("p-north-draft"),
  ).toBeInTheDocument();

  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: "Отмена" }));
  });
  const discard = screen.queryByRole("button", { name: "Отменить релиз" });
  if (discard)
    await act(async () => {
      fireEvent.click(discard);
    });
  expect(
    await screen.findByRole("heading", {
      name: "Сопоставление · Northern Lines",
    }),
  ).toBeInTheDocument();

  expect(window.location.hash).toBe(matchingHash);
  expect(matchingScreen).not.toHaveAttribute("hidden");
  expect(
    screen.getByRole("heading", { name: "Сопоставление · Northern Lines" }),
  ).toHaveFocus();
  expect(
    screen.getByRole("button", {
      name: /^Выбрать «Northern Lines» \(MusicBrainz,/,
    }),
  ).toBeInTheDocument();
  expect(
    screen.getByRole("textbox", { name: "Артист и название издания" }),
  ).toHaveValue("Northern Lines");
  expect(
    screen.getByRole("textbox", {
      name: "MBID или официальная ссылка на издание",
    }),
  ).toHaveValue("p-north-draft");

  await act(async () => {
    fireEvent.click(screen.getByRole("link", { name: "Все входящие" }));
  });
  expect(
    await screen.findByRole("heading", {
      name: "Разбор входящих групп",
    }),
  ).toBeInTheDocument();
  expect(window.location.hash).toBe("#/incoming-groups?group=north");

  await act(async () => {
    window.history.back();
  });
  expect(
    await screen.findByRole("heading", {
      name: "Сопоставление · Northern Lines",
    }),
  ).toBeInTheDocument();
  expect(window.location.hash).toBe(matchingHash);
  expect(
    screen.getByRole("textbox", {
      name: "MBID или официальная ссылка на издание",
    }),
  ).toHaveValue("p-north-draft");
});
