import { useEffect, useRef, useState } from "react";
import {
  Input,
  Label,
  RadioButton,
  RadioField,
  RadioGroup,
  TextField,
} from "react-aria-components";
import {
  checkSetupPaths,
  completeSetup,
  getSetupState,
  saveSetupRuntime,
} from "../../api/generated/client";
import type {
  SaveRuntimeBodyPublicationFormat,
  SetupStateBody,
} from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import { SetupMetadata } from "./SetupMetadata";
import { SetupTools } from "./SetupTools";
import { errorMessage, setupError } from "./setupApi";
import "./setup.css";

const steps = [
  "Платформа",
  "Каталоги",
  "Инструменты",
  "Публикация",
  "Провайдеры метаданных",
  "Итог",
] as const;
const stepKey = "melotrove.setup.step";
function earliest(state: SetupStateBody) {
  const s = state.settings;
  if (!s.tools_directory && !s.output_directory) return 0;
  if (!s.tools_directory || !s.output_directory) return 1;
  if (!s.active_ffmpeg_installation_id || !s.active_fpcalc_installation_id)
    return 2;
  if (!s.publication_format) return 3;
  if (!s.musicbrainz_verified_at) return 4;
  return 5;
}
function restoredStep(state: SetupStateBody) {
  const stored = window.localStorage.getItem(stepKey);
  const step = stored === null ? NaN : Number(stored);
  return Number.isInteger(step) && step >= 0 && step < steps.length
    ? Math.min(step, earliest(state))
    : earliest(state);
}

export function SetupManager({
  onCompleted,
  initialState,
}: {
  onCompleted: () => void;
  initialState: SetupStateBody;
}) {
  const [state, setState] = useState(initialState);
  const [step, setStep] = useState(() => restoredStep(initialState));
  const [toolsDirectory, setToolsDirectory] = useState(
    initialState.settings.tools_directory,
  );
  const [outputDirectory, setOutputDirectory] = useState(
    initialState.settings.output_directory,
  );
  const [format, setFormat] = useState<SaveRuntimeBodyPublicationFormat | "">(
    initialState.settings.publication_format === "source" ||
      initialState.settings.publication_format === "mka"
      ? initialState.settings.publication_format
      : "",
  );
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const heading = useRef<HTMLHeadingElement>(null);
  const alert = useRef<HTMLParagraphElement>(null);
  function navigate(next: number) {
    setError("");
    setNotice("");
    setStep(next);
    window.localStorage.setItem(stepKey, String(next));
  }
  useEffect(() => {
    if (steps[step]) heading.current?.focus();
  }, [step]);
  useEffect(() => {
    if (error) alert.current?.focus();
  }, [error]);
  async function refresh() {
    const response = await getSetupState({ cache: "no-store" });
    if (response.status !== 200) throw setupError(response);
    setState(response.data);
    setToolsDirectory(response.data.settings.tools_directory);
    setOutputDirectory(response.data.settings.output_directory);
    if (
      response.data.settings.publication_format === "source" ||
      response.data.settings.publication_format === "mka"
    )
      setFormat(response.data.settings.publication_format);
    return response.data;
  }
  async function advance() {
    setPending(true);
    setError("");
    setNotice("");
    try {
      if (step === 1) {
        if (!toolsDirectory || !outputDirectory)
          throw new Error("Укажите оба каталога сервера.");
        const checked = await checkSetupPaths({
          tools_directory: toolsDirectory,
          output_directory: outputDirectory,
        });
        if (checked.status !== 200) throw setupError(checked);
        const saved = await saveSetupRuntime({
          tools_directory: toolsDirectory,
          output_directory: outputDirectory,
        });
        if (saved.status !== 204) throw setupError(saved);
        await refresh();
      }
      if (step === 2) {
        const fresh = await refresh();
        if (
          !fresh.settings.active_ffmpeg_installation_id ||
          !fresh.settings.active_fpcalc_installation_id
        )
          throw new Error("Активируйте проверенные FFmpeg и fpcalc.");
      }
      if (step === 3) {
        if (!format) throw new Error("Выберите формат публикации.");
        const saved = await saveSetupRuntime({ publication_format: format });
        if (saved.status !== 204) throw setupError(saved);
        await refresh();
      }
      if (step === 5) {
        const fresh = await refresh();
        if (!fresh.configuration_health.healthy)
          throw new Error(
            fresh.configuration_health.problems?.join(", ") ||
              "Настройка не готова.",
          );
        const completed = await completeSetup();
        if (completed.status !== 204) throw setupError(completed);
        window.localStorage.removeItem(stepKey);
        onCompleted();
        return;
      }
      navigate(step + 1);
    } catch (reason) {
      setError(errorMessage(reason));
    } finally {
      setPending(false);
    }
  }
  return (
    <section aria-labelledby="setup-title" className="mx-auto max-w-6xl py-3">
      <h1 id="setup-title" className="text-xl font-semibold">
        Первый запуск
      </h1>
      <ol aria-label="Шаги настройки" className="setup-stepper">
        {steps.map((name, index) => (
          <li key={name} aria-current={index === step ? "step" : undefined}>
            {index + 1}. {name}
          </li>
        ))}
      </ol>
      <div className="setup-layout">
        <div className="setup-panel">
          <h2 ref={heading} tabIndex={-1} className="font-semibold">
            {steps[step]}
          </h2>
          {step === 0 && (
            <p>
              Сервер: {state.platform.goos}/{state.platform.goarch}.{" "}
              {state.platform.supported
                ? "Платформа поддерживается."
                : state.platform.reason}
            </p>
          )}
          {step === 1 && (
            <div className="grid gap-3">
              <TextField value={toolsDirectory} onChange={setToolsDirectory}>
                <Label>Tools directory</Label>
                <Input className="block w-full border p-2" />
              </TextField>
              <TextField value={outputDirectory} onChange={setOutputDirectory}>
                <Label>Output directory</Label>
                <Input className="block w-full border p-2" />
              </TextField>
              <p>
                Пути относятся к серверу, а не к этому компьютеру. Output
                directory должен быть пустым.
              </p>
            </div>
          )}
          {step === 2 && <SetupTools onActivated={refresh} />}
          {step === 3 && (
            <RadioGroup
              value={format}
              onChange={(value) => {
                if (value === "source" || value === "mka") setFormat(value);
              }}
            >
              <Label>Publication format</Label>
              <RadioField value="source">
                <RadioButton>Исходный формат</RadioButton>
              </RadioField>
              <RadioField value="mka">
                <RadioButton>MKA remux</RadioButton>
              </RadioField>
            </RadioGroup>
          )}
          {step === 4 && (
            <SetupMetadata
              state={state}
              refresh={refresh}
              onContinue={() => navigate(5)}
            />
          )}
          {step === 5 && (
            <div>
              <dl className="setup-summary">
                <div>
                  <dt>Платформа</dt>
                  <dd>
                    {state.platform.goos}/{state.platform.goarch}
                  </dd>
                </div>
                <div>
                  <dt>Инструменты</dt>
                  <dd>{state.settings.tools_directory}</dd>
                </div>
                <div>
                  <dt>Публикация</dt>
                  <dd>{state.settings.output_directory}</dd>
                </div>
                <div>
                  <dt>Формат</dt>
                  <dd>{state.settings.publication_format}</dd>
                </div>
                <div>
                  <dt>FFmpeg</dt>
                  <dd>
                    {state.settings.active_ffmpeg_installation_id ||
                      "Не активен"}
                  </dd>
                </div>
                <div>
                  <dt>fpcalc</dt>
                  <dd>
                    {state.settings.active_fpcalc_installation_id ||
                      "Не активен"}
                  </dd>
                </div>
                <div>
                  <dt>MusicBrainz</dt>
                  <dd>
                    {state.settings.musicbrainz_verified_at || "Не проверен"}
                  </dd>
                </div>
                <div>
                  <dt>LRCLIB</dt>
                  <dd>
                    {state.settings.lrclib_enabled ? "Включён" : "Выключен"}
                  </dd>
                </div>
              </dl>
              <p>
                Серверная проверка:{" "}
                {state.configuration_health.healthy ? "Готово" : "Не готово"}
              </p>
              <ul>
                {state.configuration_health.problems?.map((problem) => (
                  <li key={problem}>{problem}</li>
                ))}
              </ul>
              <AppButton
                isDisabled={pending}
                onPress={() => {
                  void verifySummary();
                }}
              >
                Обновить проверку
              </AppButton>
            </div>
          )}
          {error && (
            <p ref={alert} tabIndex={-1} role="alert">
              {error}
            </p>
          )}
          {notice && <p role="status">{notice}</p>}
          {pending && <p role="status">Выполняется запрос…</p>}
          <div className="mt-5 flex justify-between">
            <AppButton
              isDisabled={step === 0 || pending}
              onPress={() => navigate(step - 1)}
            >
              Назад
            </AppButton>
            {step !== 4 && (
              <AppButton isDisabled={pending} onPress={advance}>
                {step === 5 ? "Завершить Setup" : "Продолжить"}
              </AppButton>
            )}
          </div>
        </div>
        <aside className="setup-panel setup-help" aria-label="О текущем шаге">
          <h2>Важно знать</h2>
          {step === 0 && (
            <p>
              Платформа зафиксирована сервером и не меняется в браузере.
              Проверьте OS и архитектуру перед настройкой.
            </p>
          )}
          {step === 1 && (
            <p>
              Укажите постоянные каталоги внутри сервера или контейнера. Каталог
              публикации должен быть пустым; путь к инструментам сохраняется
              между перезапусками.
            </p>
          )}
          {step === 2 && (
            <p>
              FFmpeg включает ffmpeg и ffprobe в одной установке. Выбирайте
              проверенные версии обоих пакетов, а затем явно активируйте их.
            </p>
          )}
          {step === 3 && (
            <p>
              Исходный формат сохраняет контейнер. MKA выполняет remux без
              перекодирования.
            </p>
          )}
          {step === 4 && (
            <p>
              Проверка MusicBrainz обязательна для завершения. LRCLIB не требует
              сетевой проверки на этом шаге.
            </p>
          )}
          {step === 5 && (
            <p>
              Итог загружается с сервера. Завершение повторно проверит все
              обязательные условия; ошибку можно исправить, вернувшись к нужному
              шагу.
            </p>
          )}
        </aside>
      </div>
    </section>
  );
  async function verifySummary() {
    setPending(true);
    setError("");
    try {
      await refresh();
      setNotice("Состояние сервера обновлено.");
    } catch (reason) {
      setError(errorMessage(reason));
    } finally {
      setPending(false);
    }
  }
}
