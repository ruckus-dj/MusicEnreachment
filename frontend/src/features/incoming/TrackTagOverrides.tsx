export type TrackTagOverrideDraft = {
  title: { enabled: boolean; value: string };
  artists: { enabled: boolean; values: TrackTagOverrideArtistDraft[] };
};

export type TrackTagOverrideArtistDraft = { id: string; name: string };

function localArtistID() {
  return crypto.randomUUID();
}

export function emptyTrackTagOverride(): TrackTagOverrideDraft {
  return {
    title: { enabled: false, value: "" },
    artists: { enabled: false, values: [] },
  };
}

export function TrackTagOverrides({
  title,
  trackId,
  override,
  disabled,
  onChange,
}: {
  title: string;
  trackId: string;
  override: TrackTagOverrideDraft;
  disabled: boolean;
  onChange: (next: TrackTagOverrideDraft) => void;
}) {
  return (
    <fieldset className="mt-3 space-y-2 rounded border border-stone-300 p-2">
      <legend className="px-1 font-medium">Теги позиции · {title}</legend>
      <p className="text-xs text-stone-600">
        Без переопределения используются теги провайдера. Изменения здесь —
        локальная ручная правка метаданных, не изменение связей файлов.
      </p>
      <label className="flex items-center gap-2 text-sm">
        <input
          type="checkbox"
          checked={override.title.enabled}
          disabled={disabled}
          onChange={(event) =>
            onChange({
              ...override,
              title: {
                ...override.title,
                enabled: event.currentTarget.checked,
              },
            })
          }
        />
        Переопределить название для {title} ({trackId})
      </label>
      {override.title.enabled ? (
        <label className="block text-sm">
          Название
          <input
            className="mt-1 block w-full rounded border border-stone-400 bg-white p-2"
            value={override.title.value}
            disabled={disabled}
            onChange={(event) =>
              onChange({
                ...override,
                title: { ...override.title, value: event.currentTarget.value },
              })
            }
          />
        </label>
      ) : null}
      <label className="flex items-center gap-2 text-sm">
        <input
          type="checkbox"
          checked={override.artists.enabled}
          disabled={disabled}
          onChange={(event) =>
            onChange({
              ...override,
              artists: {
                ...override.artists,
                enabled: event.currentTarget.checked,
              },
            })
          }
        />
        Переопределить список исполнителей для {title} ({trackId})
      </label>
      {override.artists.enabled ? (
        <div className="space-y-2">
          {override.artists.values.map((artist, index) => (
            <div key={artist.id} className="flex gap-2">
              <label className="min-w-0 flex-1 text-sm">
                Исполнитель {index + 1}
                <input
                  className="mt-1 block w-full rounded border border-stone-400 bg-white p-2"
                  value={artist.name}
                  disabled={disabled}
                  onChange={(event) => {
                    const values = [...override.artists.values];
                    values[index] = {
                      ...artist,
                      name: event.currentTarget.value,
                    };
                    onChange({
                      ...override,
                      artists: { ...override.artists, values },
                    });
                  }}
                />
              </label>
              <button
                type="button"
                className="mt-5 rounded border border-stone-400 px-2 text-sm"
                disabled={disabled}
                aria-label={`Удалить исполнителя ${index + 1} для ${title}`}
                onClick={() =>
                  onChange({
                    ...override,
                    artists: {
                      ...override.artists,
                      values: override.artists.values.filter(
                        (_, item) => item !== index,
                      ),
                    },
                  })
                }
              >
                Удалить
              </button>
            </div>
          ))}
          <button
            type="button"
            className="rounded border border-stone-400 px-2 py-1 text-sm"
            disabled={disabled}
            onClick={() =>
              onChange({
                ...override,
                artists: {
                  ...override.artists,
                  values: [
                    ...override.artists.values,
                    { id: localArtistID(), name: "" },
                  ],
                },
              })
            }
          >
            Добавить исполнителя для {title} ({trackId})
          </button>
          {!override.artists.values.length ? (
            <p className="text-xs text-stone-600">
              Пустой список — явное переопределение без исполнителей.
            </p>
          ) : null}
        </div>
      ) : null}
    </fieldset>
  );
}
