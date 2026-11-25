import { type ReactNode, useEffect, useRef } from "react";
import { AppButton } from "../components/AppButton";
import type { ThemePreferenceStore } from "../components/themePreference";
import { shell } from "../i18n/namespaces/shell";
import type { Locale } from "../i18n/types";
import {
  ProductShell,
  type ProductShellProps,
  shellControls,
} from "./ShellView";
import { shellStartupMessages } from "./shellStartupMessages";

const focusSkipClass =
  "fixed left-3 top-[-80px] z-[200] border-0 bg-[var(--surface)] p-2 focus:top-2";
const alertClass =
  "wrap-anywhere bg-[var(--error-soft)] p-2 text-[var(--error)]";
const actionClass =
  "inline-flex min-h-[34px] items-center rounded border border-[var(--button)] bg-[var(--button)] px-3.5 py-1.5 font-semibold text-[var(--button-text)] no-underline hover:opacity-90 focus-visible:outline-2 focus-visible:outline-[var(--accent)] focus-visible:outline-offset-2";

export type ApplicationFrameState =
  | { kind: "startup" }
  | ({
      kind: "product";
    } & Omit<ProductShellProps, "locale" | "themeStore" | "children">);

export function ApplicationFrame({
  locale,
  themeStore,
  state,
  children,
}: {
  locale: Locale;
  themeStore: ThemePreferenceStore;
  state: ApplicationFrameState;
  children: ReactNode;
}) {
  if (state.kind === "startup") {
    return (
      <ShellStartupFrame locale={locale} themeStore={themeStore}>
        {children}
      </ShellStartupFrame>
    );
  }
  return (
    <ProductShell
      locale={locale}
      themeStore={themeStore}
      brandHref={state.brandHref}
      activeSection={state.activeSection}
      sectionLinks={state.sectionLinks}
      incomingCount={state.incomingCount}
      footer={state.footer}
      setupHref={state.setupHref}
      fallbackSection={state.fallbackSection}
    >
      {children}
    </ProductShell>
  );
}

function ShellStartupFrame({
  locale,
  themeStore,
  children,
}: {
  locale: Locale;
  themeStore: ThemePreferenceStore;
  children: ReactNode;
}) {
  const content = useRef<HTMLElement>(null);
  const text = shell[locale];
  const controls = shellControls(themeStore);
  return (
    <main className="min-h-dvh bg-[var(--canvas)] p-3 text-[var(--ink)]">
      <button
        type="button"
        className={focusSkipClass}
        onClick={() => content.current?.focus()}
      >
        {text.skip}
      </button>
      <header className="m-0 flex flex-wrap items-center justify-between gap-3 bg-[var(--surface)] px-3 py-2">
        <div className="flex min-w-0 flex-col gap-px p-1">
          <strong className="text-[15px] leading-5">MeloTrove</strong>
          <span className="text-[11px] leading-4 text-[var(--muted)]">
            {text.library}
          </span>
        </div>
        <div className="flex items-center gap-2">
          {controls.language}
          {controls.theme}
        </div>
      </header>
      <section
        ref={content}
        id="app-content"
        className="min-w-0 pt-3"
        tabIndex={-1}
      >
        {children}
      </section>
    </main>
  );
}

export type ShellStartupState =
  | { kind: "loading-server" | "loading-sources" | "opening-incoming" }
  | {
      kind: "setup-error" | "sources-error";
      message?: string;
      retry: () => void;
    }
  | {
      kind: "diagnostic";
      message?: string;
      platform: string;
      retry: () => void;
    }
  | { kind: "add-source"; sourcesHref: string };

export function ShellStartupView({
  locale,
  state,
}: {
  locale: Locale;
  state: ShellStartupState;
}) {
  const text = shellStartupMessages[locale];
  const error = useRef<HTMLDivElement>(null);
  const diagnostic = useRef<HTMLHeadingElement>(null);
  const { kind } = state;
  useEffect(() => {
    if (kind === "setup-error") error.current?.focus();
    if (kind === "diagnostic") diagnostic.current?.focus();
  }, [kind]);

  switch (kind) {
    case "loading-server":
      return <p role="status">{text.loadingServer}</p>;
    case "loading-sources":
      return <p role="status">{text.loadingSources}</p>;
    case "opening-incoming":
      return <p role="status">{text.loadingIncoming}</p>;
    case "setup-error":
      return (
        <div ref={error} tabIndex={-1} role="alert" className={alertClass}>
          {state.message ?? text.loadError}{" "}
          <AppButton onPress={state.retry}>{text.retry}</AppButton>
        </div>
      );
    case "diagnostic":
      return (
        <section aria-labelledby="diagnostic-title">
          <h1
            ref={diagnostic}
            tabIndex={-1}
            id="diagnostic-title"
            className="text-xl leading-[26px] font-[650]"
          >
            {text.diagnose}
          </h1>
          <p role="alert" className={alertClass}>
            {state.message || text.unsupportedPlatform}
          </p>
          <p>{state.platform}</p>
          <AppButton onPress={state.retry}>{text.refreshDiagnostics}</AppButton>
        </section>
      );
    case "sources-error":
      return (
        <section aria-labelledby="home-title">
          <h1 id="home-title">{text.home}</h1>
          <p role="alert">{state.message ?? text.sourcesLoadError}</p>
          <AppButton onPress={state.retry}>{text.retry}</AppButton>
        </section>
      );
    case "add-source":
      return (
        <section aria-labelledby="home-title">
          <h1 id="home-title">{text.addSourceTitle}</h1>
          <p>{text.addSourceDescription}</p>
          <a className={actionClass} href={state.sourcesHref}>
            {text.addSource}
          </a>
        </section>
      );
  }
}
