import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { server } from "../../test/server";
import { AcoustIDKeyForm } from "./AcoustIDKeyForm";

describe("AcoustIDKeyForm", () => {
  it("saves the application key, clears the field, and refreshes presence", async () => {
    const secret = "private-application-key";
    const refresh = vi.fn();
    const busyChange = vi.fn();
    server.use(
      http.put(
        "/api/settings/acoustid/application-key",
        async ({ request }) => {
          expect(await request.json()).toEqual({ key: secret });
          return new HttpResponse(null, { status: 204 });
        },
      ),
    );
    render(
      <AcoustIDKeyForm
        hasKey={false}
        onChanged={refresh}
        onBusyChange={busyChange}
      />,
    );

    fireEvent.change(screen.getByLabelText("Ключ приложения AcoustID"), {
      target: { value: secret },
    });
    fireEvent.click(screen.getByRole("button", { name: "Сохранить ключ" }));

    expect(
      await screen.findByText("Ключ приложения AcoustID сохранён."),
    ).toBeVisible();
    expect(screen.getByLabelText("Ключ приложения AcoustID")).toHaveValue("");
    expect(refresh).toHaveBeenCalledOnce();
    expect(busyChange).toHaveBeenNthCalledWith(1, true);
    expect(busyChange).toHaveBeenLastCalledWith(false);
    expect(document.body).not.toHaveTextContent(secret);
  });

  it("deletes a present key and refreshes state", async () => {
    const refresh = vi.fn();
    server.use(
      http.delete(
        "/api/settings/acoustid/application-key",
        () => new HttpResponse(null, { status: 204 }),
      ),
    );
    render(<AcoustIDKeyForm hasKey onChanged={refresh} />);

    fireEvent.change(screen.getByLabelText("Ключ приложения AcoustID"), {
      target: { value: "unsaved-draft" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Удалить ключ" }));

    expect(
      await screen.findByText("Ключ приложения AcoustID удалён."),
    ).toBeVisible();
    expect(screen.getByLabelText("Ключ приложения AcoustID")).toHaveValue("");
    expect(refresh).toHaveBeenCalledOnce();
  });

  it("shows save and delete failures without exposing the submitted key", async () => {
    const secret = "must-not-leak";
    server.use(
      http.put("/api/settings/acoustid/application-key", () =>
        HttpResponse.json({ detail: `rejected ${secret}` }, { status: 400 }),
      ),
      http.delete("/api/settings/acoustid/application-key", () =>
        HttpResponse.json({ detail: "delete failed" }, { status: 500 }),
      ),
    );
    render(<AcoustIDKeyForm hasKey onChanged={vi.fn()} />);

    fireEvent.change(screen.getByLabelText("Ключ приложения AcoustID"), {
      target: { value: secret },
    });
    fireEvent.click(screen.getByRole("button", { name: "Сохранить ключ" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("[скрыто]");
    expect(document.body).not.toHaveTextContent(secret);

    fireEvent.click(screen.getByRole("button", { name: "Удалить ключ" }));
    await waitFor(() =>
      expect(screen.getByRole("alert")).toHaveTextContent("delete failed"),
    );
    expect(document.body).not.toHaveTextContent(secret);
  });

  it("displays key presence only", () => {
    render(<AcoustIDKeyForm hasKey onChanged={vi.fn()} />);
    expect(screen.getByText("Ключ сохранён (значение скрыто).")).toBeVisible();
    expect(screen.getByLabelText("Ключ приложения AcoustID")).toHaveValue("");
  });
});
