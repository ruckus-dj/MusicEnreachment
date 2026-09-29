import { useState } from "react";
import { updateSettings } from "../../api/generated/client";
import type { UpdateSettingsBodyPublicationFormat } from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";

const catalogCheckedKey = "melotrove.catalog-checked-at";

export function SettingsScreen() {
  const [toolsDirectory, setToolsDirectory] = useState("");
  const [outputDirectory, setOutputDirectory] = useState("");
  const [format, setFormat] =
    useState<UpdateSettingsBodyPublicationFormat>("mka");
  const [catalogMessage, setCatalogMessage] = useState(() => catalogStatus());
  const [notice, setNotice] = useState("");
  async function save() {
    if (toolsDirectory) {
      setNotice("Смена Tools directory требует отдельной операции переноса.");
      return;
    }
    try {
      const response = await updateSettings({
        output_directory: outputDirectory,
        publication_format: format,
      });
      setNotice(
        response.status === 204
          ? "Настройки сохранены."
          : "Не удалось сохранить настройки.",
      );
    } catch (reason) {
      setNotice(
        reason instanceof Error
          ? reason.message
          : "Не удалось сохранить настройки.",
      );
    }
  }
  function refreshCatalog() {
    globalThis.localStorage?.setItem(
      catalogCheckedKey,
      new Date().toISOString(),
    );
    setCatalogMessage("Каталог обновлён вручную. Установка не запущена.");
  }
  return (
    <section
      aria-labelledby="settings-title"
      className="mx-auto max-w-2xl py-8"
    >
      <h1 id="settings-title" className="text-xl font-semibold">
        Настройки
      </h1>
      <p className="mt-1 text-sm text-stone-600">
        Изменения применяются отдельно от завершённого Setup.
      </p>
      <div className="mt-5 grid gap-5">
        <fieldset className="rounded border bg-white p-4">
          <legend className="px-1 font-semibold">Runtime</legend>
          <label className="block">
            Tools directory
            <input
              value={toolsDirectory}
              onChange={(event) => setToolsDirectory(event.target.value)}
              className="mt-1 block w-full rounded border p-2"
            />
          </label>
          <label className="mt-3 block">
            Output directory
            <input
              value={outputDirectory}
              onChange={(event) => setOutputDirectory(event.target.value)}
              className="mt-1 block w-full rounded border p-2"
            />
          </label>
          <label className="mt-3 block">
            Publication format
            <select
              value={format}
              onChange={(event) =>
                setFormat(event.target.value === "source" ? "source" : "mka")
              }
              className="mt-1 block rounded border p-2"
            >
              <option value="mka">MKA remux</option>
              <option value="source">Исходный формат</option>
            </select>
          </label>
          <AppButton
            onPress={save}
            className="mt-4 rounded border border-stone-400 px-3 py-1 text-sm"
          >
            Сохранить
          </AppButton>
        </fieldset>
        <section
          className="rounded border bg-white p-4"
          aria-labelledby="tools-title"
        >
          <h2 id="tools-title" className="font-semibold">
            Managed tools
          </h2>
          <p className="mt-2 text-sm">{catalogMessage}</p>
          <div className="mt-3 flex gap-2">
            <AppButton onPress={refreshCatalog}>Refresh catalog</AppButton>
            <AppButton isDisabled>Установить версию</AppButton>
            <AppButton isDisabled>Активировать версию</AppButton>
            <AppButton isDisabled>Перенести каталог</AppButton>
          </div>
          <p className="mt-3 text-sm text-stone-600">
            Установка, activation, delete и move доступны только после успешного
            server preflight; активная версия не удаляется.
          </p>
        </section>
      </div>
      {notice && (
        <p role="status" className="mt-4">
          {notice}
        </p>
      )}
    </section>
  );
}
function catalogStatus() {
  const previous = globalThis.localStorage?.getItem(catalogCheckedKey);
  if (!previous) return "Каталог ещё не проверялся.";
  const elapsed = Date.now() - Date.parse(previous);
  return elapsed < 24 * 60 * 60 * 1000
    ? `Последняя проверка: ${new Date(previous).toLocaleString()}. Ручной Refresh доступен всегда.`
    : "Проверка каталога доступна.";
}
