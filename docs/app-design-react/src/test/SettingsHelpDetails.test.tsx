import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import {
  type SettingsGeneralModel,
  SettingsGeneralSection,
} from "../ui/features/settings/SettingsGeneralSection";
import { SettingsHelpDetails } from "../ui/features/settings/SettingsHelpDetails";

const noop = () => {};

function generalModel(
  locale: "en" | "ru",
  handlers: {
    cleanup?: (selected: boolean) => void;
    albumCompletion?: (selected: boolean) => void;
  } = {},
): SettingsGeneralModel {
  return {
    locale,
    format: {
      value: "source",
      choices: ["source", "mka", "m4a"],
      onChange: noop,
    },
    concurrency: {
      value: "4",
      invalid: false,
      dirty: false,
      saveDisabled: false,
      onChange: noop,
      onSave: noop,
    },
    matchingThreshold: {
      value: "0.70",
      invalid: false,
      dirty: false,
      saveDisabled: false,
      onChange: noop,
      onSave: noop,
    },
    sha256: { enabled: false, onChange: noop },
    logLevel: { value: "info", onChange: noop },
    cleanup: {
      enabled: false,
      dirty: false,
      onChange: handlers.cleanup ?? noop,
    },
    albumCompletion: {
      enabled: false,
      dirty: false,
      onChange: handlers.albumCompletion ?? noop,
    },
  };
}

describe("SettingsHelpDetails", () => {
  it("opens an accessible panel and collapses it again", () => {
    render(
      <SettingsHelpDetails label="Details">
        <p>Active consumers also block removal.</p>
      </SettingsHelpDetails>,
    );
    const trigger = screen.getByRole("button", { name: "Details" });
    const panel = document.getElementById(
      trigger.getAttribute("aria-controls") ?? "",
    );
    if (!panel) throw new Error("Missing disclosure panel");

    expect(trigger).toHaveAttribute("aria-expanded", "false");
    expect(panel).toHaveAttribute("hidden");
    expect(panel).toHaveTextContent("Active consumers also block removal.");

    fireEvent.click(trigger);
    expect(trigger).toHaveAttribute("aria-expanded", "true");
    expect(panel).not.toHaveAttribute("hidden");
    expect(panel).toHaveAttribute("aria-hidden", "false");

    fireEvent.click(trigger);
    expect(trigger).toHaveAttribute("aria-expanded", "false");
    expect(panel).toHaveAttribute("hidden");
  });

  it("accepts the localized trigger label without changing the panel", () => {
    render(
      <SettingsHelpDetails label="Подробнее">
        <p>Дополнительное пояснение.</p>
      </SettingsHelpDetails>,
    );
    expect(
      screen.getByRole("button", { name: "Подробнее" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Дополнительное пояснение.")).toBeInTheDocument();
  });
});

describe.each(["en", "ru"] as const)("source deletion help (%s)", (locale) => {
  it("keeps the destructive caveat visible and discloses only safeguards", () => {
    render(<SettingsGeneralSection model={generalModel(locale)} />);
    const section = screen.getByRole("region", {
      name: locale === "en" ? "Source deletion" : "Удаление исходников",
    });
    const hint =
      locale === "en"
        ? /Off by default\. An original file is eligible for removal only after every segment assigned to it has been successfully published, including segments not queued yet\./
        : /Выключено по умолчанию\. Исходный файл можно удалить только после успешной публикации всех назначенных ему сегментов, включая ещё не поставленные в очередь\./;
    const details =
      locale === "en"
        ? /Active consumers and retained holds also block removal\./
        : /Активные потребители и сохранённые блокировки также запрещают удаление\./;

    expect(within(section).getByText(hint)).toBeVisible();
    expect(within(section).getAllByText(hint)).toHaveLength(1);
    expect(within(section).getByText(details)).not.toBeVisible();

    fireEvent.click(
      within(section).getByRole("button", {
        name: locale === "en" ? "Details" : "Подробнее",
      }),
    );
    expect(within(section).getByText(details)).toBeVisible();
  });
});

describe.each([
  "en",
  "ru",
] as const)("automatic publication help (%s)", (locale) => {
  it("keeps the off-by-default condition visible", () => {
    render(<SettingsGeneralSection model={generalModel(locale)} />);
    const section = screen.getByRole("region", {
      name: locale === "en" ? "Automatic publication" : "Автопубликация",
    });
    const hint =
      locale === "en"
        ? /Off by default\. First publication starts automatically only when every track position in the edition reaches the matching threshold\./
        : /Выключено по умолчанию\. Первая публикация запускается автоматически, только если каждая позиция издания достигает порога сопоставления\./;
    expect(within(section).getByText(hint)).toBeVisible();
    expect(within(section).getByText(hint)).not.toHaveTextContent(
      locale === "en"
        ? /does not publish previously matched albums/
        : /не публикует уже сопоставленные альбомы/,
    );
  });
});

describe.each([
  "en",
  "ru",
] as const)("settings hint click targets (%s)", (locale) => {
  it("does not change source deletion or automatic publication when their hints are clicked", () => {
    const cleanup = vi.fn();
    const albumCompletion = vi.fn();
    render(
      <SettingsGeneralSection
        model={generalModel(locale, { cleanup, albumCompletion })}
      />,
    );

    const sourceSection = screen.getByRole("region", {
      name: locale === "en" ? "Source deletion" : "Удаление исходников",
    });
    const sourceHintText =
      locale === "en"
        ? /Off by default\. An original file is eligible for removal only after every segment assigned to it has been successfully published, including segments not queued yet\./
        : /Выключено по умолчанию\. Исходный файл можно удалить только после успешной публикации всех назначенных ему сегментов, включая ещё не поставленные в очередь\./;
    const sourceHint = within(sourceSection).getByText(sourceHintText);
    expect(sourceHint.closest("label")).toBeNull();
    expect(
      within(sourceSection).getByRole("checkbox"),
    ).toHaveAccessibleDescription(sourceHintText);
    fireEvent.click(sourceHint);
    expect(cleanup).not.toHaveBeenCalled();

    const albumSection = screen.getByRole("region", {
      name: locale === "en" ? "Automatic publication" : "Автопубликация",
    });
    const albumHintText =
      locale === "en"
        ? /Off by default\. First publication starts automatically only when every track position in the edition reaches the matching threshold\./
        : /Выключено по умолчанию\. Первая публикация запускается автоматически, только если каждая позиция издания достигает порога сопоставления\./;
    const albumHint = within(albumSection).getByText(albumHintText);
    expect(albumHint.closest("label")).toBeNull();
    expect(
      within(albumSection).getByRole("switch"),
    ).toHaveAccessibleDescription(albumHintText);
    fireEvent.click(albumHint);
    expect(albumCompletion).not.toHaveBeenCalled();
  });
});
