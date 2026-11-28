import { fireEvent, render, screen, within } from "@testing-library/react";
import { beforeEach, expect, it } from "vitest";
import { MeloTroveUI } from "../../../ui/app/MeloTroveUI";
import { demoMeloTroveProvider } from "../../demoMeloTroveProvider";
import { I18nProvider } from "../demoPreferences";
import {
  demoPrimaryArtistPublication,
  incomingPublicationQueueHref,
  incomingPublicationReadyHref,
} from "./incomingPublicationFixtures";

function renderDemo(hash = "#/incoming-groups", locale: "ru" | "en" = "ru") {
  window.history.replaceState(null, "", hash);
  window.localStorage.setItem("music-design-v2-language", locale);
  return render(
    <I18nProvider>
      <MeloTroveUI provider={demoMeloTroveProvider} />
    </I18nProvider>,
  );
}

beforeEach(() => {
  window.history.replaceState(null, "", "#/incoming-groups");
});

it("adds matching and both publication queues into the Incoming count", () => {
  renderDemo();
  const nav = screen.getByRole("link", { name: /^Входящие/ });
  expect(within(nav).getByText("5")).toBeVisible();
  expect(screen.getByRole("tab", { name: "Сопоставление: 3" })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  fireEvent.click(screen.getByRole("tab", { name: "К публикации: 2" }));
  expect(
    screen.getByRole("tab", { name: "Требуют внимания: 1" }),
  ).toHaveAttribute("aria-selected", "true");
  fireEvent.click(screen.getByRole("tab", { name: "Готовы к публикации: 1" }));
  expect(screen.getByText("Small Hours")).toBeVisible();
  expect(within(nav).getByText("5")).toBeVisible();
});

it("takes the real primary-artist blocker from the queue to a ready track", async () => {
  renderDemo(incomingPublicationQueueHref);
  fireEvent.click(
    screen.getByRole("button", { name: "Треки издания «Northern Lines · JP»" }),
  );
  const tracks = screen.getByRole("table", {
    name: "Треки издания «Northern Lines · JP»",
  });
  const track = within(tracks).getByRole("link", { name: "Still Water" });
  expect(track).toHaveAttribute("href", demoPrimaryArtistPublication.trackHref);
  fireEvent.click(track);

  await screen.findByRole("heading", { level: 1, name: /Still Water/ });
  expect(
    screen.getByRole("tab", { name: "Файлы и публикация" }),
  ).toHaveAttribute("aria-selected", "true");
  expect(
    screen.getByText("Не указан основной исполнитель издания"),
  ).toBeVisible();
  expect(
    screen.queryByRole("button", { name: "Предпросмотр публикации" }),
  ).toBeNull();

  const fix = screen.getByRole("link", { name: "Выбрать артиста" });
  expect(fix).toHaveAttribute("href", demoPrimaryArtistPublication.resolveHref);
  fireEvent.click(fix);
  const artist = await screen.findByRole("combobox", {
    name: "Основной исполнитель релиза",
  });
  expect(artist).toHaveValue("");
  fireEvent.change(artist, { target: { value: "arden" } });
  expect(
    screen.getByRole("link", { name: "К публикации трека" }),
  ).toBeVisible();
  const back = screen.getByRole("link", { name: "Назад к очереди публикации" });
  expect(back).toHaveAttribute("href", incomingPublicationReadyHref);
  fireEvent.click(back);

  expect(
    await screen.findByRole("tab", { name: "Готовы к публикации: 2" }),
  ).toHaveAttribute("aria-selected", "true");
  expect(
    screen.getByRole("tab", { name: "Требуют внимания: 0" }),
  ).toBeVisible();
  expect(
    screen.getByText(
      "Основной исполнитель выбран · можно опубликовать вручную",
    ),
  ).toBeVisible();
  const readyRow = screen
    .getByRole("button", { name: "Треки издания «Northern Lines · JP»" })
    .closest("tr");
  if (!readyRow) throw new Error("Missing ready edition");
  expect(within(readyRow).getByText("Arden Trio")).toBeVisible();
  fireEvent.click(screen.getByRole("link", { name: "Открыть трек" }));
  await screen.findByRole("heading", { level: 1, name: /Still Water/ });
  expect(
    screen.queryByText("Не указан основной исполнитель издания"),
  ).toBeNull();
  expect(
    screen.getByRole("button", { name: "Предпросмотр публикации" }),
  ).toBeVisible();
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр публикации" }),
  );
  const preview = await screen.findByRole("dialog");
  expect(preview).toHaveTextContent(/\/Arden Trio\/.*Still Water\.mka/);
  const publish = within(preview).getByRole("button", { name: "Опубликовать" });
  expect(publish).toBeEnabled();
  fireEvent.click(publish);
  const status = screen.getByRole("region", { name: "Состояние позиции" });
  expect(
    await within(status).findByText("Опубликовано", {}, { timeout: 2500 }),
  ).toBeVisible();
  fireEvent.click(
    screen.getByRole("link", { name: "Назад к очереди публикации" }),
  );
  expect(
    await screen.findByRole("tab", { name: "Готовы к публикации: 1" }),
  ).toBeVisible();
  expect(screen.queryByText("Northern Lines · JP")).not.toBeInTheDocument();
});

it("offers the release field directly from the English queue", () => {
  renderDemo(incomingPublicationQueueHref, "en");
  expect(screen.getByRole("tab", { name: "Needs attention: 1" })).toBeVisible();
  expect(screen.getByText("Release primary artist is missing")).toBeVisible();
  expect(
    screen.getByRole("link", { name: "Choose primary artist" }),
  ).toHaveAttribute("href", demoPrimaryArtistPublication.resolveHref);
});
