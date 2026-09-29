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
import { SetupManager } from "./SetupManager";

afterEach(cleanup);

function enterDirectories() {
  fireEvent.change(screen.getByLabelText("Tools directory"), {
    target: { value: "/srv/tools" },
  });
  fireEvent.change(screen.getByLabelText("Output directory"), {
    target: { value: "/srv/output" },
  });
}

async function submit(name = "Продолжить") {
  const response = nextResponse();
  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name }));
    await response;
  });
  return response;
}

describe("SetupManager", () => {
  it("requires absolute directories", () => {
    render(<SetupManager onCompleted={() => undefined} />);
    fireEvent.click(screen.getByRole("button", { name: "Продолжить" }));
    expect(screen.getByRole("alert").textContent).toContain("абсолютные");
  });

  it("saves directories before advancing", async () => {
    server.use(
      http.put(
        "/api/setup/runtime",
        () => new HttpResponse(null, { status: 204 }),
      ),
    );
    render(<SetupManager onCompleted={vi.fn()} />);
    enterDirectories();

    const { request } = await submit();

    expect(request.headers.get("content-type")).toBe("application/json");
    expect(await request.json()).toEqual({
      tools_directory: "/srv/tools",
      output_directory: "/srv/output",
      publication_format: "mka",
    });
    expect(screen.getByRole("heading", { level: 2 }).textContent).toBe(
      "Инструменты",
    );
  });

  it.each([
    204, 409,
  ])("saves publication and handles completion status %i", async (status) => {
    server.use(
      http.put(
        "/api/setup/runtime",
        () => new HttpResponse(null, { status: 204 }),
      ),
      http.post("/api/setup/complete", () =>
        status === 204
          ? new HttpResponse(null, { status })
          : HttpResponse.json({ status }, { status }),
      ),
    );
    const onCompleted = vi.fn();
    render(<SetupManager onCompleted={onCompleted} />);
    enterDirectories();
    await submit();
    fireEvent.click(screen.getByRole("button", { name: "Продолжить" }));
    fireEvent.click(screen.getByRole("radio", { name: "Исходный формат" }));

    const publication = await submit();
    expect(await publication.request.json()).toEqual({
      tools_directory: "/srv/tools",
      output_directory: "/srv/output",
      publication_format: "source",
    });
    const completion = await submit("Завершить Setup");

    expect(completion.request.method).toBe("POST");
    expect(completion.response.status).toBe(status);
    expect(onCompleted).toHaveBeenCalledTimes(status === 204 ? 1 : 0);
    expect(screen.queryByRole("alert") !== null).toBe(status !== 204);
  });

  it("keeps the directory step editable when saving fails", async () => {
    server.use(
      http.put("/api/setup/runtime", () =>
        HttpResponse.json({ status: 400 }, { status: 400 }),
      ),
    );
    const onCompleted = vi.fn();
    render(<SetupManager onCompleted={onCompleted} />);
    enterDirectories();

    await submit();

    expect(screen.getByRole("alert")).toBeTruthy();
    expect(screen.getByRole("heading", { level: 2 }).textContent).toBe(
      "Каталоги",
    );
    expect(
      screen
        .getByRole("button", { name: "Продолжить" })
        .hasAttribute("disabled"),
    ).toBe(false);
    expect(onCompleted).not.toHaveBeenCalled();
  });
});
