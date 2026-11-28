import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { demoMeloTroveProvider } from "../demo/demoMeloTroveProvider";
import { MeloTroveUI } from "../ui/app/MeloTroveUI";

const positionHref = "#/library/releases/signals/positions/signals-t5";

afterEach(() => {
  window.history.replaceState(
    null,
    "",
    "#/library?view=artists&publication=all&availability=all",
  );
});

it("opens the demo source tag drawer from the clicked source", () => {
  window.history.replaceState(null, "", positionHref);
  render(<MeloTroveUI provider={demoMeloTroveProvider} />);

  fireEvent.click(screen.getByRole("tab", { name: "Файлы и публикация" }));
  const trigger = screen.getByRole("button", { name: "Посмотреть теги" });
  expect(trigger).toHaveAttribute("aria-haspopup", "dialog");

  fireEvent.click(trigger);
  const dialog = screen.getByRole("dialog", { name: "Теги источника" });
  expect(within(dialog).getByText("05 - Static.flac")).toBeVisible();
  expect(within(dialog).getByRole("tab", { name: "Теги файла" })).toBeVisible();
  expect(
    within(dialog).getByRole("tab", { name: "Сравнить с публикацией" }),
  ).toBeVisible();
  expect(within(dialog).getByText("Static")).toBeVisible();
  expect(within(dialog).getByText("Electronic")).toBeVisible();

  fireEvent.keyDown(dialog, { key: "Escape" });
  expect(screen.queryByRole("dialog", { name: "Теги источника" })).toBeNull();
  expect(trigger).toHaveFocus();
});
