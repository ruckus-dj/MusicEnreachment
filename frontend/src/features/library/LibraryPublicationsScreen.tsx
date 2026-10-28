import { useEffect, useState } from "react";
import { apiResponseError } from "../../api/client/errors";
import { useListLibraryPublications } from "../../api/generated/client";
import type { LibraryPublicationListItemResponse } from "../../api/generated/client.schemas";
import { useHashLocation } from "../../navigation/hashLocation";
import "./LibraryPublicationsScreen.css";

const PAGE_SIZE = 50;

interface PublicationsRoute {
  query: string;
  offset: number;
}

function parseRoute(hash: string): PublicationsRoute {
  const path = hash.startsWith("#") ? hash.slice(1) : hash;
  const [pathname, search = ""] = path.split("?", 2);
  if (pathname !== "/publications") return { query: "", offset: 0 };
  const params = new URLSearchParams(search);
  const requestedOffset = Number(params.get("offset") ?? 0);
  return {
    query: params.get("q") ?? "",
    offset:
      Number.isSafeInteger(requestedOffset) && requestedOffset > 0
        ? requestedOffset
        : 0,
  };
}

function publicationsHref(route: PublicationsRoute) {
  const params = new URLSearchParams();
  if (route.query) params.set("q", route.query);
  if (route.offset > 0) params.set("offset", String(route.offset));
  const search = params.toString();
  return `#/publications${search ? `?${search}` : ""}`;
}

function responseError(response: { status: number; data?: unknown }) {
  return apiResponseError(
    response,
    `Не удалось загрузить текущие публикации (HTTP ${response.status}).`,
  ).message;
}

function artistNames(item: LibraryPublicationListItemResponse) {
  return item.artists
    .map((artist) => artist.credit_name + artist.join_phrase)
    .join("")
    .trim();
}

function displayTimestamp(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.getTime())
    ? value
    : new Intl.DateTimeFormat("ru", {
        dateStyle: "medium",
        timeStyle: "short",
      }).format(date);
}

export function LibraryPublicationsScreen() {
  const hash = useHashLocation();
  const route = parseRoute(hash);
  const [search, setSearch] = useState(route.query);
  useEffect(() => setSearch(route.query), [route.query]);

  const query = useListLibraryPublications({
    limit: PAGE_SIZE,
    offset: route.offset,
    q: route.query || undefined,
  });
  const failedResponse =
    query.data && query.data.status !== 200 ? responseError(query.data) : "";
  const error =
    failedResponse ||
    (query.error instanceof Error
      ? query.error.message
      : query.error
        ? "Не удалось загрузить текущие публикации."
        : "");
  const page = query.data?.status === 200 ? query.data.data : undefined;

  function updateRoute(next: Partial<PublicationsRoute>) {
    window.location.hash = publicationsHref({ ...route, ...next });
  }

  return (
    <main className="library-publications">
      <header className="library-publications__header">
        <div>
          <h1>Текущие публикации</h1>
          <p>Список сохранённых публикаций и записанных для них сведений.</p>
        </div>
      </header>

      <form
        className="library-publications__search"
        onSubmit={(event) => {
          event.preventDefault();
          updateRoute({ query: search.trim(), offset: 0 });
        }}
      >
        <label htmlFor="publication-search">Поиск по публикациям</label>
        <input
          id="publication-search"
          type="search"
          value={search}
          onChange={(event) => setSearch(event.currentTarget.value)}
        />
        <button type="submit">Найти</button>
      </form>

      {query.isLoading ? (
        <p className="library-publications__state" role="status">
          Загрузка публикаций…
        </p>
      ) : error ? (
        <section
          className="library-publications__state library-publications__state--error"
          role="alert"
        >
          <p>{error}</p>
          <button type="button" onClick={() => void query.refetch()}>
            Повторить
          </button>
        </section>
      ) : page && page.items.length > 0 ? (
        <>
          <div className="library-publications__table-wrap">
            <table>
              <caption className="library-publications__sr-only">
                Текущие публикации из ответа сервера
              </caption>
              <thead>
                <tr>
                  <th scope="col">Релиз и позиция</th>
                  <th scope="col">Аудио</th>
                  <th scope="col">Записанный путь</th>
                  <th scope="col">Зафиксировано</th>
                </tr>
              </thead>
              <tbody>
                {page.items.map((item) => (
                  <PublicationRow item={item} key={item.position_id} />
                ))}
              </tbody>
            </table>
          </div>
          <nav
            className="library-publications__pagination"
            aria-label="Страницы публикаций"
          >
            <span>
              {route.offset + 1}–{route.offset + page.items.length} из{" "}
              {page.total}
            </span>
            <button
              type="button"
              disabled={route.offset === 0}
              onClick={() =>
                updateRoute({ offset: Math.max(0, route.offset - PAGE_SIZE) })
              }
            >
              Назад
            </button>
            <button
              type="button"
              disabled={route.offset + PAGE_SIZE >= page.total}
              onClick={() => updateRoute({ offset: route.offset + PAGE_SIZE })}
            >
              Далее
            </button>
          </nav>
        </>
      ) : page ? (
        <section className="library-publications__state">
          <h2>
            {page.total > 0
              ? "На странице нет публикаций"
              : "Публикаций не найдено"}
          </h2>
          <p>
            {page.total > 0
              ? "Для текущих параметров страницы сервер не вернул записей."
              : route.query
                ? "Для этого запроса нет текущих публикаций."
                : "В медиатеке пока нет текущих публикаций."}
          </p>
          {page.total > 0 && (
            <a href={publicationsHref({ query: route.query, offset: 0 })}>
              К первой странице
            </a>
          )}
          <a href="#/library">Открыть медиатеку</a>
          <a href="#/sources">Открыть источники</a>
        </section>
      ) : null}
    </main>
  );
}

function PublicationRow({
  item,
}: {
  item: LibraryPublicationListItemResponse;
}) {
  const positionName = `${item.medium}.${item.displayed_number || item.position} ${item.title || "Без названия"}`;
  const artists = artistNames(item);
  return (
    <tr>
      <td>
        <a
          className="library-publications__release"
          href={`#/library/releases/${encodeURIComponent(item.release_id)}`}
        >
          {item.release_title || "Без названия"}
        </a>
        {artists && (
          <span className="library-publications__sub">{artists}</span>
        )}
        <a
          className="library-publications__position"
          href={`#/library/releases/${encodeURIComponent(item.release_id)}/positions/${encodeURIComponent(item.position_id)}`}
        >
          {positionName}
        </a>
      </td>
      <td>
        <strong>{item.audio.codec}</strong>
        <span className="library-publications__sub">
          Контейнер: {item.audio.container}
        </span>
      </td>
      <td>
        <span className="library-publications__path-label">
          Записанный корень
        </span>
        <span className="library-publications__path">
          {item.output_root ?? "Не указан в записи"}
        </span>
        <span className="library-publications__path-label">
          Относительный путь
        </span>
        <span className="library-publications__path">{item.relative_path}</span>
      </td>
      <td>
        <time dateTime={item.validated_at}>
          {displayTimestamp(item.validated_at)}
        </time>
      </td>
    </tr>
  );
}
