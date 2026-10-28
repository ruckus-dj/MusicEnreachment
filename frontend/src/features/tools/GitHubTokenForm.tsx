import { useEffect, useId, useRef, useState } from "react";
import { apiResponseError, errorMessage } from "../../api/client/errors";
import { deleteGithubToken, setGithubToken } from "../../api/generated/client";
import { AppButton } from "../../components/AppButton";
import "./github-token.css";

const tokenGuide =
  "https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens";
const tokenCreate =
  "https://github.com/settings/personal-access-tokens/new?name=MeloTrove+release+reader&description=Read-only+public+release+metadata&expires_in=90";

export function GitHubTokenForm({
  hasToken,
  onChanged,
}: {
  hasToken: boolean;
  onChanged?: () => void | Promise<void>;
}) {
  const [draft, setDraft] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const headingId = useId();
  const inputId = useId();
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  async function save() {
    if (!draft || busy) return;
    setBusy(true);
    setError("");
    setSaved(false);
    try {
      const response = await setGithubToken({ token: draft });
      if (response.status !== 204) throw apiResponseError(response);
      if (!mounted.current) return;
      setDraft("");
      setSaved(true);
      try {
        await onChanged?.();
      } catch {
        if (mounted.current) {
          setError(
            "Токен сохранён, но не удалось обновить состояние страницы.",
          );
        }
      }
    } catch (reason) {
      if (mounted.current) {
        setError(
          errorMessage(reason, "Не удалось сохранить токен GitHub.").replaceAll(
            draft,
            "[скрыто]",
          ),
        );
      }
    } finally {
      if (mounted.current) setBusy(false);
    }
  }

  async function remove() {
    if (busy) return;
    setBusy(true);
    setError("");
    setSaved(false);
    try {
      const response = await deleteGithubToken();
      if (response.status !== 204) throw apiResponseError(response);
      if (!mounted.current) return;
      setDraft("");
      setSaved(false);
      await onChanged?.();
    } catch (reason) {
      if (mounted.current)
        setError(errorMessage(reason, "Не удалось удалить токен GitHub."));
    } finally {
      if (mounted.current) setBusy(false);
    }
  }

  return (
    <section className="github-token" aria-labelledby={headingId}>
      <h2 id={headingId}>GitHub: лимит запросов к релизам (необязательно)</h2>
      <p>
        Без токена GitHub обычно разрешает 60 запросов в час на общий IP-адрес;
        с токеном — обычно 5000 в час на пользователя. Лимиты могут меняться и
        быть общими для других приложений.
      </p>
      <p>
        Рекомендуем fine-grained personal access token: выберите свой resource
        owner, доступ только к публичным репозиториям и не добавляйте разрешения
        — для чтения метаданных публичных релизов достаточно неявного Metadata:
        read, а разрешения <code>repo</code> и <code>public_repo</code> не
        нужны. MeloTrove не проверяет и не может подтвердить, что у токена нет
        дополнительных прав. Достаточно бесплатного аккаунта GitHub: платная
        подписка не требуется.
      </p>
      <p>
        Токен хранится только на сервере, в базе данных MeloTrove, и попадает в
        её резервные копии. Защищайте резервные копии так же, как сам токен.
      </p>
      <p>
        Токен отправляется только в запросах к GitHub API: он не записывается в
        журналы и не передаётся при скачивании файлов.
      </p>
      <p>
        <a href={tokenCreate} target="_blank" rel="noreferrer">
          Создать fine-grained token
        </a>{" "}
        (срок 90 дней предложен как пример, его можно изменить) ·{" "}
        <a href={tokenGuide} target="_blank" rel="noreferrer">
          официальное руководство GitHub
        </a>
        .
      </p>
      <ol>
        <li>
          В GitHub откройте Settings → Developer settings → Personal access
          tokens → Fine-grained tokens и нажмите Generate new token.
        </li>
        <li>
          Выберите свой resource owner, Public repositories и не добавляйте
          дополнительные permissions.
        </li>
        <li>
          Скопируйте токен (GitHub показывает его один раз) и вставьте сюда.
          Разрешения <code>repo</code> и <code>public_repo</code> не добавляйте:
          они не нужны.
        </li>
      </ol>
      <label htmlFor={inputId}>Personal access token</label>
      <input
        id={inputId}
        type="password"
        autoComplete="new-password"
        value={draft}
        disabled={busy}
        onChange={(event) => {
          setDraft(event.currentTarget.value);
          setError("");
          setSaved(false);
        }}
      />
      <div className="github-token-actions">
        <AppButton isDisabled={busy || !draft} onPress={() => void save()}>
          Проверить и сохранить
        </AppButton>
        {hasToken && (
          <AppButton isDisabled={busy} onPress={() => void remove()}>
            Удалить сохранённый токен
          </AppButton>
        )}
      </div>
      <p role="status">
        {hasToken
          ? "Токен сохранён (значение не отображается)."
          : "Токен не сохранён."}
      </p>
      {saved && <p role="status">Токен проверен и сохранён.</p>}
      {busy && <p role="status">Выполняется запрос…</p>}
      {error && <p role="alert">{error}</p>}
      {hasToken && (
        <p>
          Удаление из MeloTrove не отзывает токен в GitHub. Для отзыва откройте{" "}
          <a
            href="https://github.com/settings/personal-access-tokens"
            target="_blank"
            rel="noreferrer"
          >
            настройки токенов GitHub
          </a>
          .
        </p>
      )}
    </section>
  );
}
