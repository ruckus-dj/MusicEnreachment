import { useId, useState } from "react";
import { apiResponseError, errorMessage } from "../../api/client/errors";
import {
  deleteAcoustidApplicationKey,
  setAcoustidApplicationKey,
} from "../../api/generated/client";
import { AppButton } from "../../components/AppButton";
import "./AcoustIDKeyForm.css";

type Props = {
  hasKey: boolean;
  onChanged: () => unknown;
  onBusyChange?: (busy: boolean) => void;
};

export function AcoustIDKeyForm({ hasKey, onChanged, onBusyChange }: Props) {
  const [draft, setDraft] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const inputId = useId();

  async function mutate(action: "save" | "delete") {
    if (busy || (action === "save" && !draft)) return;
    setBusy(true);
    onBusyChange?.(true);
    setError("");
    setNotice("");
    try {
      const response =
        action === "save"
          ? await setAcoustidApplicationKey({ key: draft })
          : await deleteAcoustidApplicationKey();
      if (response.status !== 204) throw apiResponseError(response);
      setDraft("");
      await onChanged();
      setNotice(
        action === "save"
          ? "Ключ приложения AcoustID сохранён."
          : "Ключ приложения AcoustID удалён.",
      );
    } catch (reason) {
      const message = errorMessage(
        reason,
        "Не удалось сохранить ключ AcoustID.",
      );
      setError(draft ? message.replaceAll(draft, "[скрыто]") : message);
    } finally {
      setBusy(false);
      onBusyChange?.(false);
    }
  }

  return (
    <section
      className="setup-acoustid-key"
      aria-labelledby={`${inputId}-heading`}
    >
      <h2 id={`${inputId}-heading`}>AcoustID (необязательно)</h2>
      <p>
        Ключ приложения включает поиск записей по аудиоотпечатку: отправляется
        отпечаток, а не аудиофайл. Нужен именно application key, а не user API
        key (он используется для отправки отпечатков).
      </p>
      <p>
        <a
          href="https://acoustid.org/new-application"
          target="_blank"
          rel="noreferrer"
        >
          Зарегистрировать приложение AcoustID
        </a>
        {" · "}
        <a
          href="https://acoustid.org/webservice"
          target="_blank"
          rel="noreferrer"
        >
          документация API
        </a>
        {" · "}
        <a href="https://acoustid.biz/" target="_blank" rel="noreferrer">
          условия для коммерческого использования
        </a>
      </p>
      <p>
        Бесплатное использование AcoustID предназначено для некоммерческих
        проектов.
      </p>
      <label htmlFor={inputId}>Ключ приложения AcoustID</label>
      <input
        autoComplete="new-password"
        disabled={busy}
        id={inputId}
        onChange={(event) => setDraft(event.currentTarget.value)}
        type="password"
        value={draft}
      />
      <p role="status">
        {hasKey ? "Ключ сохранён (значение скрыто)." : "Ключ не задан."}
      </p>
      <div className="setup-acoustid-key-actions">
        <AppButton isDisabled={busy || !draft} onPress={() => mutate("save")}>
          Сохранить ключ
        </AppButton>
        {hasKey && (
          <AppButton isDisabled={busy} onPress={() => mutate("delete")}>
            Удалить ключ
          </AppButton>
        )}
      </div>
      {error && <p role="alert">{error}</p>}
      {notice && <p role="status">{notice}</p>}
    </section>
  );
}
