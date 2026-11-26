import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  DemoMeloTroveProviderRefresh,
  demoMeloTroveProvider,
} from "../demo/demoMeloTroveProvider";
import { MeloTroveUI } from "../ui/app/MeloTroveUI";

function CatalogDemo() {
  return (
    <DemoMeloTroveProviderRefresh value={undefined}>
      <MeloTroveUI provider={demoMeloTroveProvider} />
    </DemoMeloTroveProviderRefresh>
  );
}

const bothAxesHash =
  "#/library?view=albums&publication=published&availability=available";
const bothAxesWithOffset =
  "#/library?view=albums&publication=published&availability=available&offset=1";

beforeEach(() => {
  vi.useFakeTimers();
  window.history.replaceState(null, "", bothAxesHash);
});

afterEach(() => {
  vi.useRealTimers();
  window.history.replaceState(
    null,
    "",
    "#/library?view=artists&publication=all&availability=all",
  );
});

it("renders independent publication and availability axes from a deep link", () => {
  render(<CatalogDemo />);

  expect(screen.getByRole("row", { name: /Опубликовано/ })).toBeVisible();
  expect(screen.getByRole("row", { name: /В наличии/ })).toBeVisible();
});

it("removes only the touched axis and preserves the other", () => {
  render(<CatalogDemo />);

  fireEvent.click(
    within(screen.getByRole("row", { name: /Опубликовано/ })).getByRole(
      "button",
    ),
  );
  expect(window.location.hash).toBe(
    "#/library?view=albums&publication=all&availability=available",
  );

  act(() => {
    window.dispatchEvent(new HashChangeEvent("hashchange"));
  });
  expect(screen.queryByRole("row", { name: /Опубликовано/ })).toBeNull();
  expect(screen.getByRole("row", { name: /В наличии/ })).toBeVisible();

  fireEvent.click(
    within(screen.getByRole("row", { name: /В наличии/ })).getByRole("button"),
  );
  expect(window.location.hash).toBe(
    "#/library?view=albums&publication=all&availability=all",
  );
});

it("renders an unpublished-only deep link", () => {
  window.history.replaceState(
    null,
    "",
    "#/library?view=albums&publication=unpublished&availability=all",
  );
  render(<CatalogDemo />);

  expect(screen.getByRole("row", { name: /Не опубликовано/ })).toBeVisible();
});

it("resets pending root typing and both axes in one action", () => {
  render(<CatalogDemo />);

  const search = screen.getByRole("searchbox", {
    name: "Поиск в медиатеке",
  });
  fireEvent.change(search, { target: { value: "Arden" } });
  fireEvent.click(screen.getByRole("button", { name: "Сбросить все" }));

  expect(search).toHaveValue("");
  expect(window.location.hash).toBe(
    "#/library?view=albums&publication=all&availability=all",
  );

  act(() => {
    vi.advanceTimersByTime(300);
  });
  expect(window.location.hash).toBe(
    "#/library?view=albums&publication=all&availability=all",
  );
});

it("restores both axes and the prior offset from the canonical address", () => {
  window.history.replaceState(null, "", bothAxesWithOffset);
  render(<CatalogDemo />);

  fireEvent.click(
    within(screen.getByRole("row", { name: /Опубликовано/ })).getByRole(
      "button",
    ),
  );
  expect(window.location.hash).toBe(
    "#/library?view=albums&publication=all&availability=available",
  );
  act(() => {
    window.dispatchEvent(new HashChangeEvent("hashchange"));
  });

  act(() => {
    window.history.replaceState(null, "", bothAxesWithOffset);
    window.dispatchEvent(new HashChangeEvent("hashchange"));
  });

  expect(window.location.hash).toBe(bothAxesWithOffset);
  expect(screen.getByRole("row", { name: /Опубликовано/ })).toBeVisible();
  expect(screen.getByRole("row", { name: /В наличии/ })).toBeVisible();
});
