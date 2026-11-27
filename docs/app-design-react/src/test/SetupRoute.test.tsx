import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { demoMeloTroveProvider } from "../demo/demoMeloTroveProvider";
import { MeloTroveUI } from "../ui/app/MeloTroveUI";

afterEach(() => {
  window.sessionStorage.clear();
  window.history.replaceState(null, "", "#/");
});

it("guards direct step links and restores the selected step and release on remount", async () => {
  localStorage.setItem("music-design-v2-language", "ru");
  window.history.replaceState(null, "", "#/setup?step=summary");
  const element = <MeloTroveUI provider={demoMeloTroveProvider} />;
  const first = render(element);
  const heading = (name: string) =>
    screen.getByRole("heading", { level: 2, name });
  expect(heading("Каталоги")).toBeVisible();
  await waitFor(() =>
    expect(window.location.hash).toBe("#/setup?step=directories"),
  );

  fireEvent.click(screen.getByRole("button", { name: "Продолжить" }));
  await waitFor(() => expect(heading("Инструменты")).toBeVisible());
  expect(window.location.hash).toBe("#/setup?step=tools");
  const releasePicker = () =>
    screen.getByRole("button", { name: /Версия · ffmpeg/ });
  fireEvent.click(releasePicker());
  fireEvent.click(await screen.findByRole("option", { name: "8.0" }));
  expect(releasePicker()).toHaveTextContent("8.0");

  first.unmount();
  render(element);

  expect(heading("Инструменты")).toBeVisible();
  expect(releasePicker()).toHaveTextContent("8.0");
  expect(window.location.hash).toBe("#/setup?step=tools");
});
