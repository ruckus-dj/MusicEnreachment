import { useId } from "react";
import { AppButton } from "../../components/AppButton";

export type SetupGenreCatalogState = "idle" | "loading" | "ready" | "failed";

export type SetupGenreCatalogLocale = "ru" | "en";

export interface SetupGenreCatalogViewProps {
  locale: SetupGenreCatalogLocale;
  state: SetupGenreCatalogState;
  count?: number;
  disabled?: boolean;
  requiresVerification?: boolean;
  onLoad: () => void;
}

const messages = {
  en: {
    title: "MusicBrainz genre catalog",
    description:
      "A complete catalog for the selected MusicBrainz endpoint is required before continuing.",
    idle: "Not loaded. Load the complete catalog to continue.",
    requiresVerification: "Check the MusicBrainz connection before loading.",
    loading: "Loading the complete catalog…",
    ready: (count: number) =>
      `Ready · ${count.toLocaleString("en")} genres available.`,
    readyWithoutCount: "Ready · the complete catalog is available.",
    failed: "The catalog could not be loaded. Retry before continuing.",
    load: "Load catalog",
    retry: "Retry",
    pendingAction: "Loading…",
  },
  ru: {
    title: "Каталог жанров MusicBrainz",
    description:
      "Для продолжения нужен полный каталог выбранного адреса MusicBrainz.",
    idle: "Не загружен. Загрузите полный каталог, чтобы продолжить.",
    requiresVerification:
      "Перед загрузкой проверьте подключение к MusicBrainz.",
    loading: "Загрузка полного каталога…",
    ready: (count: number) =>
      `Готово · доступно жанров: ${count.toLocaleString("ru")}.`,
    readyWithoutCount: "Готово · полный каталог доступен.",
    failed:
      "Не удалось загрузить каталог. Повторите попытку, чтобы продолжить.",
    load: "Загрузить каталог",
    retry: "Повторить",
    pendingAction: "Загрузка…",
  },
} as const;

const idleClassName = "m-0 text-sm text-[var(--muted)]";
const readyClassName = "m-0 text-sm text-[var(--ink)]";
const failedClassName = "m-0 text-sm wrap-anywhere text-[var(--error)]";

export function SetupGenreCatalogView({
  locale,
  state,
  count,
  disabled,
  requiresVerification,
  onLoad,
}: SetupGenreCatalogViewProps) {
  const copy = messages[locale];
  const titleId = useId();
  const statusText =
    state === "ready"
      ? typeof count === "number"
        ? copy.ready(count)
        : copy.readyWithoutCount
      : state === "idle" && requiresVerification
        ? copy.requiresVerification
        : copy[state];
  const statusClassName =
    state === "failed"
      ? failedClassName
      : state === "ready"
        ? readyClassName
        : idleClassName;
  return (
    <section
      aria-labelledby={titleId}
      className="grid min-w-0 gap-3 rounded-lg border border-[var(--line)] bg-[var(--raised)] p-4 text-sm text-[var(--ink)]"
    >
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="grid gap-1">
          <h3 id={titleId} className="m-0 text-sm font-semibold">
            {copy.title}
          </h3>
          <p className="m-0 max-w-[75ch] text-xs text-[var(--muted)]">
            {copy.description}
          </p>
        </div>
        {state !== "ready" && (
          <AppButton
            isDisabled={disabled || requiresVerification || state === "loading"}
            onPress={onLoad}
          >
            {state === "loading"
              ? copy.pendingAction
              : state === "failed"
                ? copy.retry
                : copy.load}
          </AppButton>
        )}
      </div>
      <p
        role={state === "failed" ? "alert" : "status"}
        className={statusClassName}
      >
        {statusText}
      </p>
    </section>
  );
}
