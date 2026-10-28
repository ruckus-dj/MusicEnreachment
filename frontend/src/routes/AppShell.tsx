import { useCallback, useEffect, useRef, useState } from "react";
import { getSetupState, useListSources } from "../api/generated/client";
import type { SetupStateBody } from "../api/generated/client.schemas";
import { AppButton } from "../components/AppButton";
import { ThemeSelect } from "../components/ThemeSelect";
import { IncomingGroupReviewScreen } from "../features/incoming/IncomingGroupReviewScreen";
import { LibraryReleaseScreen } from "../features/library/LibraryReleaseScreen";
import { OperationsScreen } from "../features/operations/OperationsScreen";
import "../features/incoming/incoming.css";
import "./app-shell.css";
import { SettingsScreen } from "../features/settings/SettingsScreen";
import { SetupManager } from "../features/setup/SetupManager";
import { SourcesScreen } from "../features/sources/SourcesScreen";
import { parseSourcesRoute } from "../features/sources/sourcesRoute";
import { useHashLocation } from "../navigation/hashLocation";

type Route =
  | "/"
  | "/settings"
  | "/setup"
  | "/sources"
  | "/incoming-groups"
  | "/operations"
  | `/library/releases/${string}`;

// The local release editor is addressed by the release UUID only. The backend
// validates the identifier, so the shell accepts a plain canonical UUID and
// leaves any other #/library/releases value to the landing fallback.
const libraryReleaseRoute =
  /^#\/library\/releases\/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$/i;
function currentRoute(hash: string, isSourcesRoute: boolean): Route {
  if (hash === "#/settings") return "/settings";
  if (hash === "#/setup") return "/setup";
  if (isSourcesRoute) return "/sources";
  if (hash === "#/incoming-groups") return "/incoming-groups";
  if (hash === "#/operations") return "/operations";
  const libraryRelease = libraryReleaseRoute.exec(hash);
  if (libraryRelease) return `/library/releases/${libraryRelease[1]}`;
  return "/";
}
export function AppShell() {
  const hash = useHashLocation();
  const sourcesRoute = parseSourcesRoute(hash);
  const route = currentRoute(hash, sourcesRoute !== undefined);
  const [state, setState] = useState<SetupStateBody>();
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const productAvailable =
    state?.completed && !state.platform.diagnostic && state.platform.supported;
  const errorRef = useRef<HTMLDivElement>(null);
  const diagnosticRef = useRef<HTMLHeadingElement>(null);
  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const response = await getSetupState({ cache: "no-store" });
      if (response.status !== 200)
        throw new Error(response.data.detail || "Не удалось загрузить Setup.");
      setState(response.data);
    } catch (reason) {
      setError(
        reason instanceof Error
          ? reason.message
          : "Не удалось загрузить Setup.",
      );
    } finally {
      setLoading(false);
    }
  }, []);
  useEffect(() => {
    void load();
  }, [load]);
  useEffect(() => {
    if (!state || state.platform.diagnostic || !state.platform.supported)
      return;
    if (!state.completed && route !== "/setup") window.location.hash = "/setup";
    if (state.completed && route === "/setup") window.location.hash = "/";
  }, [state, route]);

  useEffect(() => {
    if (!loading && error) errorRef.current?.focus();
  }, [loading, error]);
  useEffect(() => {
    if (
      !loading &&
      (state?.platform.diagnostic || state?.platform.supported === false)
    )
      diagnosticRef.current?.focus();
  }, [loading, state]);

  return (
    <main
      className={`app-shell ${
        !productAvailable
          ? "setup-shell"
          : route === "/incoming-groups"
            ? "settings-screen settings-shell incoming-screen"
            : route === "/sources"
              ? "sources-screen sources-shell"
              : "settings-screen settings-shell"
      }`}
    >
      {productAvailable ? (
        <>
          <aside className="app-sidebar" aria-label="Основная навигация">
            <a className="app-brand" href="#/" aria-label="MeloTrove — главная">
              <strong>MeloTrove</strong>
              <span>Медиатека</span>
            </a>
            <ShellNavigation route={route} />
          </aside>
          <div className="app-workspace">
            <header className="app-topbar">
              <a className="app-brand app-brand--compact" href="#/">
                <strong>MeloTrove</strong>
                <span>Медиатека</span>
              </a>
              <ThemeSelect />
            </header>
            <div className="app-mobile-nav">
              <ShellNavigation route={route} />
            </div>
            <section className="app-content">{renderRouteContent()}</section>
          </div>
        </>
      ) : (
        <>
          <header className="app-setup-header">
            <div className="app-brand">
              <strong>MeloTrove</strong>
              <span>Медиатека</span>
            </div>
            <ThemeSelect />
          </header>
          <section className="app-content app-content--setup">
            {renderRouteContent()}
          </section>
        </>
      )}
    </main>
  );

  function renderRouteContent() {
    return loading ? (
      <p role="status">Загрузка состояния сервера…</p>
    ) : error ? (
      <div ref={errorRef} tabIndex={-1} role="alert">
        {error} <AppButton onPress={load}>Повторить загрузку</AppButton>
      </div>
    ) : state && (state.platform.diagnostic || !state.platform.supported) ? (
      <section aria-labelledby="diagnostic-title">
        <h1 ref={diagnosticRef} tabIndex={-1} id="diagnostic-title">
          Диагностика платформы
        </h1>
        <p role="alert">
          {state.platform.reason || "Платформа не поддерживается."}
        </p>
        <p>
          {state.platform.goos}/{state.platform.goarch}
        </p>
        <AppButton onPress={load}>Обновить диагностику</AppButton>
      </section>
    ) : state && !state.completed ? (
      <SetupManager
        initialState={state}
        onCompleted={() => {
          void load();
        }}
      />
    ) : state && route === "/settings" ? (
      <SettingsScreen />
    ) : state && route === "/sources" ? (
      <SourcesScreen route={sourcesRoute} />
    ) : state && route === "/incoming-groups" ? (
      <IncomingGroupReviewScreen />
    ) : state && route === "/operations" ? (
      <OperationsScreen />
    ) : state && route.startsWith("/library/releases/") ? (
      <LibraryReleaseScreen
        releaseId={decodeURIComponent(route.split("/")[3] ?? "")}
      />
    ) : state ? (
      <RootLanding />
    ) : null;
  }
}

function RootLanding() {
  const sourcesQuery = useListSources({
    fetch: { cache: "no-store" },
    query: { retry: false },
  });
  const response = sourcesQuery.data;
  const sources = response?.status === 200 ? response.data.sources || [] : null;
  const errorMessage =
    sourcesQuery.error instanceof Error
      ? sourcesQuery.error.message
      : response && response.status !== 200
        ? response.data.detail || "Не удалось загрузить список источников."
        : "Не удалось загрузить список источников.";

  if (sourcesQuery.isPending) return <p role="status">Загрузка источников…</p>;

  if (sourcesQuery.isError || sources === null)
    return (
      <section aria-labelledby="home-title">
        <h1 id="home-title">Главная</h1>
        <p role="alert">{errorMessage}</p>
        <AppButton onPress={() => void sourcesQuery.refetch()}>
          Повторить загрузку
        </AppButton>
      </section>
    );

  return sources.length === 0 ? (
    <section aria-labelledby="home-title">
      <h1 id="home-title">Добавьте музыкальный каталог</h1>
      <p>
        Зарегистрируйте каталог с музыкой в разделе «Источники», чтобы начать
        работу с медиатекой. Это можно сделать сейчас или позже.
      </p>
      <a href="#/sources">Перейти к источникам</a>
    </section>
  ) : (
    <section aria-labelledby="home-title">
      <h1 id="home-title">Медиатека готова к работе</h1>
      <p>Выберите, с чего продолжить.</p>
      <ul>
        <li>
          <a href="#/incoming-groups">Открыть входящие</a>
        </li>
        <li>
          <a href="#/sources">Настроить источники</a>
        </li>
      </ul>
    </section>
  );
}

function ShellNavigation({ route }: { route: Route }) {
  const items = [
    {
      label: "Входящие",
      href: "#/incoming-groups",
      active: route === "/incoming-groups",
    },
    { label: "Источники", href: "#/sources", active: route === "/sources" },
    {
      label: "Операции",
      href: "#/operations",
      active: route === "/operations",
    },
    { label: "Настройки", href: "#/settings", active: route === "/settings" },
  ];
  return (
    <nav className="app-navigation" aria-label="Разделы приложения">
      {items.map((item) => (
        <a
          key={item.href}
          href={item.href}
          aria-current={item.active ? "page" : undefined}
        >
          {item.label}
        </a>
      ))}
    </nav>
  );
}
