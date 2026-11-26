import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ArtistScreen } from "../demo/providers/library/ArtistScreen";
import {
  originalArtists,
  originalFiles,
  originalReleases,
} from "../demo/providers/library/catalogData";
import { ApplicationRouteOutlet } from "../ui/app/ApplicationRouteOutlet";

const artistHash =
  "#/library/artists/mira?view=artists&q=Mira&publication=published&availability=all";
const bothAxesHash =
  "#/library/artists/mira?view=artists&publication=published&availability=available&offset=50";
const clearedHash =
  "#/library/artists/mira?view=artists&publication=all&availability=all";

function renderArtist() {
  return render(
    <ApplicationRouteOutlet
      route="library-artist"
      provider={ArtistScreen}
      providerProps={{
        artistId: "mira",
        artists: originalArtists,
        releases: originalReleases,
        files: originalFiles,
      }}
    />,
  );
}

beforeEach(() => {
  vi.useFakeTimers();
  window.history.replaceState(null, "", artistHash);
});

afterEach(() => {
  vi.useRealTimers();
  window.history.replaceState(
    null,
    "",
    "#/library?view=artists&publication=all&availability=all",
  );
});

it("resets combined query and filter in one navigation and ignores pending typing", () => {
  renderArtist();

  expect(screen.getByRole("row", { name: /Поиск: «Mira»/ })).toBeVisible();
  expect(screen.getByRole("radio", { name: "Опубликовано" })).toBeChecked();

  fireEvent.change(screen.getByDisplayValue("Mira"), {
    target: { value: "Arden" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Сбросить все" }));

  expect(screen.getByRole("searchbox")).toHaveValue("");
  expect(window.location.hash).toBe(clearedHash);
  expect(screen.queryByRole("row", { name: /Поиск:/ })).toBeNull();

  act(() => {
    vi.advanceTimersByTime(300);
  });
  expect(window.location.hash).toBe(clearedHash);
});

it("removing the publication tag preserves availability and pending typing", () => {
  window.history.replaceState(null, "", bothAxesHash);
  renderArtist();

  expect(screen.getByRole("row", { name: /Опубликовано/ })).toBeVisible();
  expect(screen.getByRole("row", { name: /В наличии/ })).toBeVisible();

  fireEvent.change(screen.getByRole("searchbox"), {
    target: { value: "Arden" },
  });
  fireEvent.click(
    within(screen.getByRole("row", { name: /Опубликовано/ })).getByRole(
      "button",
    ),
  );

  expect(window.location.hash).toBe(
    "#/library/artists/mira?view=artists&q=Arden&publication=all&availability=available",
  );
});

it("removing the availability tag preserves publication", () => {
  window.history.replaceState(null, "", bothAxesHash);
  renderArtist();

  fireEvent.click(
    within(screen.getByRole("row", { name: /В наличии/ })).getByRole("button"),
  );

  expect(window.location.hash).toBe(
    "#/library/artists/mira?view=artists&publication=published&availability=all",
  );
});

it("renders an unpublished-only deep link and removes only that axis", () => {
  window.history.replaceState(
    null,
    "",
    "#/library/artists/mira?view=artists&publication=unpublished&availability=all",
  );
  renderArtist();

  const tag = screen.getByRole("row", { name: /Не опубликовано/ });
  expect(tag).toBeVisible();
  expect(screen.getByRole("radio", { name: "Все" })).toBeChecked();

  fireEvent.click(within(tag).getByRole("button"));
  expect(window.location.hash).toBe(
    "#/library/artists/mira?view=artists&publication=all&availability=all",
  );
});

it("restores both axes and the prior offset from the canonical address", () => {
  window.history.replaceState(null, "", bothAxesHash);
  renderArtist();

  expect(screen.getByRole("row", { name: /Опубликовано/ })).toBeVisible();
  expect(screen.getByRole("row", { name: /В наличии/ })).toBeVisible();

  fireEvent.click(
    within(screen.getByRole("row", { name: /Опубликовано/ })).getByRole(
      "button",
    ),
  );
  expect(window.location.hash).toBe(
    "#/library/artists/mira?view=artists&publication=all&availability=available",
  );
  act(() => {
    window.dispatchEvent(new HashChangeEvent("hashchange"));
  });

  act(() => {
    window.history.replaceState(null, "", bothAxesHash);
    window.dispatchEvent(new HashChangeEvent("hashchange"));
  });

  expect(window.location.hash).toBe(bothAxesHash);
  expect(screen.getByRole("row", { name: /Опубликовано/ })).toBeVisible();
  expect(screen.getByRole("row", { name: /В наличии/ })).toBeVisible();
});

it("returns focus to the host search when the last tag is removed", () => {
  window.history.replaceState(
    null,
    "",
    "#/library/artists/mira?view=artists&publication=published&availability=all",
  );
  renderArtist();

  const tag = screen.getByRole("row", { name: /Опубликовано/ });
  tag.focus();
  fireEvent.keyDown(tag, { key: "Delete" });

  act(() => {
    window.dispatchEvent(new HashChangeEvent("hashchange"));
  });

  expect(screen.getByRole("searchbox")).toHaveFocus();
});
