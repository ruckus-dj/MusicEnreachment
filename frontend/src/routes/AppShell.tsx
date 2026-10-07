import {
  useCallback,
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import { getSetupState } from "../api/generated/client";
import type { SetupStateBody } from "../api/generated/client.schemas";
import { AppButton } from "../components/AppButton";
import { SettingsScreen } from "../features/settings/SettingsScreen";
import { SetupManager } from "../features/setup/SetupManager";
import { SourcesScreen } from "../features/sources/SourcesScreen";

type Route = "/" | "/settings" | "/setup" | "/sources";

// The inventory screen owns its own #/sources/{id} address, so any string
// under #/sources maps to the same shell route.
const sourcesRoute = /^#\/sources(\/[^/]+(\/locations\/[^/]+)?)?$/;
function currentRoute(): Route {
  const hash = window.location.hash;
  if (hash === "#/settings") return "/settings";
  if (hash === "#/setup") return "/setup";
  if (sourcesRoute.test(hash)) return "/sources";
  return "/";
}
function subscribe(onChange: () => void) {
  window.addEventListener("hashchange", onChange);
  return () => window.removeEventListener("hashchange", onChange);
}
function useRoute() {
  return useSyncExternalStore(subscribe, currentRoute, () => "/" as Route);
}

export function AppShell() {
  const route = useRoute();
  const [state, setState] = useState<SetupStateBody>();
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
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
      className={
        !state?.completed ||
        state.platform.diagnostic ||
        !state.platform.supported
          ? "setup-shell"
          : route === "/sources"
            ? "sources-screen sources-shell min-h-screen p-3"
            : "settings-screen settings-shell min-h-screen p-3"
      }
    >
      <header className="flex items-center justify-between border-b border-stone-300 pb-3">
        <div>
          <strong>MeloTrove</strong>
          <p className="text-sm">Медиатека</p>
        </div>
        {state?.completed &&
          !state.platform.diagnostic &&
          state.platform.supported && (
            <nav className="flex gap-2">
              <AppButton
                onPress={() => {
                  window.location.hash = "/sources";
                }}
              >
                Источники
              </AppButton>
              <AppButton
                onPress={() => {
                  window.location.hash = "/settings";
                }}
              >
                Настройки
              </AppButton>
            </nav>
          )}
      </header>
      <section className="py-8">
        {loading ? (
          <p role="status">Загрузка состояния сервера…</p>
        ) : error ? (
          <div ref={errorRef} tabIndex={-1} role="alert">
            {error} <AppButton onPress={load}>Повторить загрузку</AppButton>
          </div>
        ) : state &&
          (state.platform.diagnostic || !state.platform.supported) ? (
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
          <SourcesScreen />
        ) : state ? (
          "Приложение готово к настройке."
        ) : null}
      </section>
    </main>
  );
}
