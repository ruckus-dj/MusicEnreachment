import { Button } from "react-aria-components";

export function AppShell() {
  return (
    <main className="min-h-screen bg-stone-100 p-3 text-stone-900">
      <header className="flex items-center justify-between border-b border-stone-300 pb-3">
        <div>
          <strong>MusicEnreachment</strong>
          <p className="text-sm">Медиатека</p>
        </div>
        <Button className="rounded border border-stone-400 px-3 py-1">
          Настройки
        </Button>
      </header>
      <section className="py-8">Приложение готово к настройке.</section>
    </main>
  );
}
