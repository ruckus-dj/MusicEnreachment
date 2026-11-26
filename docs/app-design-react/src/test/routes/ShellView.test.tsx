import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { I18nProvider } from "../../demo/providers/demoPreferences";
import type {
  ThemePreference,
  ThemePreferenceStore,
} from "../../ui/components/themePreference";
import { ProductShell, ShellView } from "../../ui/shell/ShellView";

const brand = { href: "#incoming", label: "MeloTrove", mark: "mt" };
const controls = { language: <span>EN</span>, theme: <span>System</span> };

const sectionLinks = {
  review: "#/incoming-groups",
  library: "#/library",
  local: "#/own-catalog",
  sources: "#/sources",
  operations: "#/operations",
  settings: "#/settings",
} as const;

function multiSectionShell(active: "review" | "library" | "settings") {
  return (
    <ShellView
      brand={brand}
      skipLabel="Skip to content"
      navigationLabel="Main navigation"
      navigation={[
        {
          id: "review",
          label: "Incoming",
          href: sectionLinks.review,
          current: active === "review",
        },
        {
          id: "library",
          label: "Library",
          href: sectionLinks.library,
          current: active === "library",
        },
        {
          id: "settings",
          label: "Settings",
          href: sectionLinks.settings,
          current: active === "settings",
        },
      ]}
      controls={controls}
      footerText="Prototype"
    >
      <h1>Content</h1>
    </ShellView>
  );
}

function memoryThemeStore(initial: ThemePreference) {
  let current = initial;
  const saved: ThemePreference[] = [];
  const store: ThemePreferenceStore = {
    read: () => current,
    reset: () => {
      current = "system";
    },
    save: (preference) => {
      current = preference;
      saved.push(preference);
      return true;
    },
  };
  return { saved, store };
}

beforeEach(() => {
  window.localStorage.setItem("music-design-v2-language", "en");
});

afterEach(() => {
  delete document.documentElement.dataset.theme;
  document.documentElement.removeAttribute("lang");
  vi.unstubAllGlobals();
});

function shell(incomingCount: number, withSetupLink: boolean) {
  return (
    <ShellView
      brand={brand}
      skipLabel="Skip to content"
      navigationLabel="Main navigation"
      navigation={[
        {
          id: "review",
          label: "Incoming",
          href: "#/incoming-groups",
          count: incomingCount,
        },
      ]}
      controls={controls}
      footerText="Prototype"
      setupLink={
        withSetupLink ? { href: "#/setup", label: "First run" } : undefined
      }
    >
      <p>Content</p>
    </ShellView>
  );
}

it("keeps the footer slot and skip focus while the provider count and setup link change", () => {
  const { rerender } = render(shell(0, true));

  const footer = screen.getByText("Prototype");
  const footerSlot = footer.parentElement;
  if (!footerSlot) throw new Error("Missing shell footer slot");
  expect(
    within(footerSlot).getByRole("link", { name: "First run" }),
  ).toHaveAttribute("href", "#/setup");
  const navigation = screen.getByRole("navigation", {
    name: "Main navigation",
  });
  const reviewLink = within(navigation).getByRole("link", {
    name: /Incoming/,
  });
  expect(within(reviewLink).getByText("0")).toBeInTheDocument();

  const content = screen.getByRole("main");
  const mobileBrand = within(screen.getByRole("banner")).getByRole("link", {
    name: "MeloTrove",
  });
  expect(mobileBrand).toHaveAttribute("href", brand.href);
  fireEvent.click(screen.getByRole("button", { name: "Skip to content" }));
  expect(content).toHaveFocus();

  rerender(shell(3, false));
  fireEvent.click(screen.getByRole("button", { name: "Skip to content" }));
  expect(content).toHaveFocus();

  const updatedLink = within(
    screen.getByRole("navigation", { name: "Main navigation" }),
  ).getByRole("link", { name: /Incoming/ });
  expect(within(updatedLink).getByText("3")).toBeInTheDocument();
  expect(within(updatedLink).queryByText("0")).not.toBeInTheDocument();
  const updatedFooter = screen.getByText("Prototype");
  expect(updatedFooter.parentElement).toBe(footerSlot);
  expect(
    within(footerSlot).queryByRole("link", { name: "First run" }),
  ).not.toBeInTheDocument();
});

it("keeps exactly one main, navigation and banner landmark", () => {
  render(multiSectionShell("review"));

  expect(screen.getAllByRole("main")).toHaveLength(1);
  expect(screen.getAllByRole("navigation")).toHaveLength(1);
  expect(screen.getAllByRole("banner")).toHaveLength(1);
  expect(
    screen.getByRole("navigation", { name: "Main navigation" }),
  ).toBeVisible();
  expect(screen.getByRole("main")).toHaveAttribute("id", "content");
});

it("marks only the active navigation item as the current page", () => {
  const view = render(multiSectionShell("review"));
  let navigation = screen.getByRole("navigation", {
    name: "Main navigation",
  });

  expect(
    within(navigation).getAllByRole("link", { current: "page" }),
  ).toHaveLength(1);
  expect(
    within(navigation).getByRole("link", { name: "Incoming" }),
  ).toHaveAttribute("aria-current", "page");
  expect(
    within(navigation).getByRole("link", { name: "Library" }),
  ).not.toHaveAttribute("aria-current");

  view.rerender(multiSectionShell("settings"));
  navigation = screen.getByRole("navigation", { name: "Main navigation" });

  expect(
    within(navigation).getAllByRole("link", { current: "page" }),
  ).toHaveLength(1);
  expect(
    within(navigation).getByRole("link", { name: "Settings" }),
  ).toHaveAttribute("aria-current", "page");
  expect(
    within(navigation).getByRole("link", { name: "Incoming" }),
  ).not.toHaveAttribute("aria-current");
});

it("keeps the skip control keyboard-focusable and moves focus to the main landmark", () => {
  render(multiSectionShell("review"));
  const skip = screen.getByRole("button", { name: "Skip to content" });

  skip.focus();
  expect(skip).toHaveFocus();
  fireEvent.click(skip);

  const main = screen.getByRole("main");
  expect(main).toHaveFocus();
  expect(main).toHaveAttribute("tabindex", "-1");
});

it("applies light and dark theme choices from the shell theme control", () => {
  vi.stubGlobal(
    "matchMedia",
    vi.fn().mockReturnValue({
      matches: false,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }),
  );
  const { saved, store } = memoryThemeStore("system");

  render(
    <I18nProvider>
      <ProductShell
        locale="en"
        brandHref={sectionLinks.review}
        activeSection="settings"
        sectionLinks={sectionLinks}
        incomingCount={2}
        footer={{ kind: "prototype" }}
        themeStore={store}
      >
        <h1>Settings</h1>
      </ProductShell>
    </I18nProvider>,
  );

  const themeButton = () => screen.getByRole("button", { name: /Next theme/ });

  expect(document.documentElement.dataset.theme).toBe("light");
  expect(themeButton()).toHaveAccessibleName(/Theme: System default/);
  fireEvent.click(themeButton());
  expect(saved).toEqual(["light"]);
  expect(document.documentElement.dataset.theme).toBe("light");
  expect(themeButton()).toHaveAccessibleName(/Theme: Light/);

  fireEvent.click(themeButton());
  expect(saved).toEqual(["light", "dark"]);
  expect(document.documentElement.dataset.theme).toBe("dark");
  expect(themeButton()).toHaveAccessibleName(/Theme: Dark/);
});
