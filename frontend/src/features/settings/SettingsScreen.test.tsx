import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it, vi } from "vitest";
import { nextResponse, server } from "../../test/server";
import { SettingsScreen } from "./SettingsScreen";

afterEach(cleanup);

describe("SettingsScreen", () => {
  it("allows a manual catalog refresh", () => {
    render(<SettingsScreen />);
    fireEvent.click(screen.getByRole("button", { name: "Refresh catalog" }));
    expect(screen.getByText(/обновлён вручную/)).toBeTruthy();
  });

  it("saves runtime settings after the setup mutation route closes", async () => {
    server.use(
      http.put("/api/setup/runtime", () =>
        HttpResponse.json({ status: 404 }, { status: 404 }),
      ),
      http.put(
        "/api/settings/runtime",
        () => new HttpResponse(null, { status: 204 }),
      ),
    );
    render(<SettingsScreen />);
    fireEvent.change(screen.getByLabelText("Output directory"), {
      target: { value: "/srv/output" },
    });
    fireEvent.change(screen.getByLabelText("Publication format"), {
      target: { value: "source" },
    });
    const response = nextResponse();

    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Сохранить" }));
      await response;
    });

    const { request, response: result } = await response;
    expect(new URL(request.url).pathname).toBe("/api/settings/runtime");
    expect(await request.json()).toEqual({
      output_directory: "/srv/output",
      publication_format: "source",
    });
    expect(result.status).toBe(204);
    expect(screen.getByRole("status").textContent).toBe("Настройки сохранены.");
  });

  it("reports rejected settings without claiming success", async () => {
    server.use(
      http.put("/api/settings/runtime", () =>
        HttpResponse.json({ status: 400 }, { status: 400 }),
      ),
    );
    render(<SettingsScreen />);
    const response = nextResponse();

    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Сохранить" }));
      await response;
    });

    expect(screen.getByRole("status").textContent).toBe(
      "Не удалось сохранить настройки.",
    );
  });

  it("does not silently discard a tools-directory edit", async () => {
    const save = vi.fn(() => new HttpResponse(null, { status: 204 }));
    server.use(http.put("/api/settings/runtime", save));
    render(<SettingsScreen />);
    fireEvent.change(screen.getByLabelText("Tools directory"), {
      target: { value: "/srv/new-tools" },
    });

    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Сохранить" }));
    });

    expect(save).not.toHaveBeenCalled();
    expect(screen.getByRole("status").textContent).toContain(
      "операции переноса",
    );
  });
});
