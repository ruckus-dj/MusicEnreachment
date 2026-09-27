import { useSyncExternalStore } from "react";
import { AppButton } from "../components/AppButton";

type Route = "/" | "/settings";

function currentRoute(): Route {
  return window.location.hash === "#/settings" ? "/settings" : "/";
}

function subscribeToRouteChange(onStoreChange: () => void) {
  window.addEventListener("hashchange", onStoreChange);
  return () => window.removeEventListener("hashchange", onStoreChange);
}

function useRoute(): Route {
  return useSyncExternalStore(subscribeToRouteChange, currentRoute, () => "/");
}

export function AppShell() {
  const route = useRoute();
  return (
    <main className="min-h-screen bg-stone-100 p-3 text-stone-900">
      <header className="flex items-center justify-between border-b border-stone-300 pb-3">
        <div>
          <strong>MusicEnreachment</strong>
          <p className="text-sm">Медиатека</p>
        </div>
        <AppButton
          onPress={() => {
            window.location.hash = "/settings";
          }}
        >
          Настройки
        </AppButton>
      </header>
      <section className="py-8">
        {route === "/settings"
          ? "Настройки будут доступны после Setup Manager."
          : "Приложение готово к настройке."}
      </section>
    </main>
  );
}
