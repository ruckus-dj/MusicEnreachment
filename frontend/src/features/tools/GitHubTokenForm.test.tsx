import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { describe, expect, it, vi } from "vitest";
import { server } from "../../test/server";
import { GitHubTokenForm } from "./GitHubTokenForm";

describe("GitHubTokenForm", () => {
  it("saves a token and clears the password field without showing its value", async () => {
    const secret = "ghp-never-render-this";
    const changed = vi.fn();
    server.use(
      http.put("/api/settings/github/token", async ({ request }) => {
        expect(await request.json()).toEqual({ token: secret });
        return new HttpResponse(null, { status: 204 });
      }),
    );
    render(<GitHubTokenForm hasToken={false} onChanged={changed} />);
    const input = screen.getByLabelText("Personal access token");
    fireEvent.change(input, { target: { value: secret } });
    fireEvent.click(
      screen.getByRole("button", { name: "Проверить и сохранить" }),
    );

    await waitFor(() => expect(input).toHaveValue(""));
    expect(changed).toHaveBeenCalledOnce();
    expect(document.body).not.toHaveTextContent(secret);
    expect(screen.getByText("Токен проверен и сохранён.")).toBeInTheDocument();
  });

  it("retains the draft after a save failure and deletes only the stored token", async () => {
    server.use(
      http.put("/api/settings/github/token", () =>
        HttpResponse.json(
          { detail: "GitHub rejected the token" },
          { status: 400 },
        ),
      ),
      http.delete(
        "/api/settings/github/token",
        () => new HttpResponse(null, { status: 204 }),
      ),
    );
    const changed = vi.fn();
    render(<GitHubTokenForm hasToken onChanged={changed} />);
    const input = screen.getByLabelText("Personal access token");
    fireEvent.change(input, { target: { value: "private-draft" } });
    fireEvent.click(
      screen.getByRole("button", { name: "Проверить и сохранить" }),
    );
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    expect(input).toHaveValue("private-draft");
    expect(document.body).not.toHaveTextContent("private-draft");
    fireEvent.click(
      screen.getByRole("button", { name: "Удалить сохранённый токен" }),
    );
    await waitFor(() => expect(changed).toHaveBeenCalledOnce());
    expect(input).toHaveValue("");
  });
});
