import { useState } from "react";
import { AppButton } from "../../components/AppButton";

type Step = "Каталоги" | "Инструменты" | "Публикация" | "Итог";
const steps: Step[] = ["Каталоги", "Инструменты", "Публикация", "Итог"];

export function SetupManager({ onCompleted }: { onCompleted: () => void }) {
  const [step, setStep] = useState(0);
  const [toolsDirectory, setToolsDirectory] = useState("");
  const [outputDirectory, setOutputDirectory] = useState("");
  const [format, setFormat] = useState("mka");
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);

  async function saveAndContinue() {
    setError("");
    if (
      step === 0 &&
      (!toolsDirectory.startsWith("/") || !outputDirectory.startsWith("/"))
    ) {
      setError("Укажите абсолютные server paths для обоих каталогов.");
      return;
    }
    setPending(true);
    try {
      if (step === 0 || step === 2) {
        const response = await fetch("/api/setup/runtime", {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            tools_directory: toolsDirectory,
            output_directory: outputDirectory,
            publication_format: format,
          }),
        });
        if (!response.ok) throw new Error("Не удалось сохранить настройки.");
      }
      if (step === steps.length - 1) {
        const response = await fetch("/api/setup/complete", { method: "POST" });
        if (!response.ok)
          throw new Error("Проверьте обязательные условия Setup.");
        onCompleted();
      } else setStep((value) => value + 1);
    } catch (reason) {
      setError(
        reason instanceof Error ? reason.message : "Неизвестная ошибка.",
      );
    } finally {
      setPending(false);
    }
  }

  return (
    <section aria-labelledby="setup-title" className="mx-auto max-w-2xl py-8">
      <h1 id="setup-title" className="text-xl font-semibold">
        Первый запуск
      </h1>
      <ol aria-label="Шаги настройки" className="my-4 flex gap-2 text-sm">
        {steps.map((name, index) => (
          <li
            key={name}
            aria-current={index === step ? "step" : undefined}
            className={index === step ? "font-semibold" : "text-stone-600"}
          >
            {index + 1}. {name}
          </li>
        ))}
      </ol>
      <div className="rounded border border-stone-300 bg-white p-4">
        <h2 className="text-base font-semibold">{steps[step]}</h2>
        {step === 0 && (
          <div className="mt-3 grid gap-3">
            <label>
              Tools directory
              <input
                value={toolsDirectory}
                onChange={(event) => setToolsDirectory(event.target.value)}
                className="mt-1 block w-full rounded border p-2"
                placeholder="/var/lib/melotrove/tools"
              />
            </label>
            <label>
              Output directory
              <input
                value={outputDirectory}
                onChange={(event) => setOutputDirectory(event.target.value)}
                className="mt-1 block w-full rounded border p-2"
                placeholder="/var/lib/melotrove/output"
              />
            </label>
            <p className="text-sm text-stone-600">
              Output-directory должен быть пустым; пути относятся к серверу.
            </p>
          </div>
        )}
        {step === 1 && (
          <p className="mt-3">
            Выберите совместимые версии FFmpeg package и fpcalc. Установка и
            проверка показываются как операция без фиктивного прогресса.
          </p>
        )}
        {step === 2 && (
          <fieldset className="mt-3">
            <legend>Publication format</legend>
            <label className="mr-4">
              <input
                checked={format === "source"}
                onChange={() => setFormat("source")}
                type="radio"
                name="format"
              />{" "}
              Исходный формат
            </label>
            <label>
              <input
                checked={format === "mka"}
                onChange={() => setFormat("mka")}
                type="radio"
                name="format"
              />{" "}
              MKA remux
            </label>
          </fieldset>
        )}
        {step === 3 && (
          <p className="mt-3">
            Завершение повторно проверит каталоги, выбранный формат, MusicBrainz
            и активные installations.
          </p>
        )}
        {error && (
          <p role="alert" className="mt-3 text-sm text-red-700">
            {error}
          </p>
        )}
        <div className="mt-5 flex justify-between">
          <AppButton
            isDisabled={!step || pending}
            onPress={() => setStep((value) => value - 1)}
          >
            Назад
          </AppButton>
          <AppButton isDisabled={pending} onPress={saveAndContinue}>
            {step === 3 ? "Завершить Setup" : "Продолжить"}
          </AppButton>
        </div>
      </div>
    </section>
  );
}
