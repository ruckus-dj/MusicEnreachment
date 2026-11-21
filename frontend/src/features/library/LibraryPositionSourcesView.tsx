import { Radio, RadioGroup } from "react-aria-components";
import { AppButton } from "../../components/AppButton";

export type LibraryPositionSource = {
  id: string;
  label: string;
  summary?: string;
  selected: boolean;
  authority?: {
    decision: string;
    compatible: boolean;
    policy: string;
    locations: readonly {
      id: string;
      path: string;
      href: string;
      details: string;
    }[];
  };
};

const copy = {
  en: {
    section: "Audio sources for position",
    heading: "Audio sources",
    details: "Audio source details",
    variant: "Variant",
    decision: "Decision",
    compatibility: "Compatibility",
    policy: "Policy",
    locations: "Locations",
    selected: "Selected",
    alternative: "Alternative",
    compatible: "Compatible",
    incompatible: "Incompatible",
    unspecified: "Not specified",
    noLocations: "No source locations are specified.",
    noSources: "No saved source details are available for this position.",
    noLinkedSources: "No linked audio sources.",
    publicationSource: "Choose the audio source for publication",
    publicationHelp:
      "Choosing a source does not change the current publication.",
    choose: "Choose for publication",
    chosen: "Selected for publication",
    inspect: "Inspect tags",
    inspecting: "Inspecting tags",
  },
  ru: {
    section: "Источники аудио позиции",
    heading: "Источники аудио",
    details: "Сведения об источниках аудио",
    variant: "Вариант",
    decision: "Решение",
    compatibility: "Совместимость",
    policy: "Политика",
    locations: "Расположения",
    selected: "Выбран",
    alternative: "Альтернатива",
    compatible: "Совместим",
    incompatible: "Несовместим",
    unspecified: "Не указано",
    noLocations: "Расположения источника не указаны.",
    noSources: "Для этой позиции нет сохранённых сведений об источниках.",
    noLinkedSources: "Связанных источников аудио нет.",
    publicationSource: "Выберите источник аудио для публикации",
    publicationHelp: "Выбор источника не меняет текущую публикацию.",
    choose: "Выбрать для публикации",
    chosen: "Выбран для публикации",
    inspect: "Посмотреть теги",
    inspecting: "Сравниваем теги",
  },
} as const;

const cell =
  "border-b border-[var(--line)] px-2 py-1.5 align-top wrap-anywhere";

export function LibraryPositionSourcesView({
  locale,
  position,
  sources,
  choice,
  inspection,
}: {
  locale: "en" | "ru";
  position: string;
  sources: readonly LibraryPositionSource[];
  choice?: { value: string; onChange: (id: string) => void };
  inspection?: { value?: string; onChange: (id: string) => void };
}) {
  const text = copy[locale];
  const authority = sources.some((source) => source.authority);
  const table = sources.length ? (
    <section className="min-w-0 overflow-x-auto" aria-label={text.details}>
      <table className="w-full border-collapse text-left text-sm">
        <thead>
          <tr className="bg-[var(--raised)]">
            <th scope="col" className={cell}>
              {text.variant}
            </th>
            {authority &&
              [
                text.decision,
                text.compatibility,
                text.policy,
                text.locations,
              ].map((label) => (
                <th key={label} scope="col" className={cell}>
                  {label}
                </th>
              ))}
            {inspection && (
              <th scope="col" className={cell}>
                {text.inspect}
              </th>
            )}
          </tr>
        </thead>
        <tbody>
          {sources.map((row) => {
            const evidence = row.authority;
            const name = (
              <>
                <strong className="block">{row.label}</strong>
                {row.summary && (
                  <span className="block text-xs text-[var(--muted)]">
                    {row.summary}
                  </span>
                )}
              </>
            );
            return (
              <tr key={row.id}>
                <th scope="row" className={cell}>
                  {choice ? (
                    <Radio
                      value={row.id}
                      className="group flex min-w-0 cursor-pointer items-center gap-2 data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--focus)]"
                    >
                      <span
                        aria-hidden="true"
                        className="size-4 shrink-0 rounded-full border border-[var(--control)] group-data-[selected]:border-[var(--accent)] group-data-[selected]:bg-[var(--accent)]"
                      />
                      <span className="min-w-0">
                        {name}
                        <span className="block text-xs text-[var(--muted)]">
                          {row.selected ? text.chosen : text.choose}
                        </span>
                      </span>
                    </Radio>
                  ) : (
                    name
                  )}
                </th>
                {authority && (
                  <>
                    <td className={cell}>
                      {row.selected ? text.selected : text.alternative} ·{" "}
                      {evidence?.decision || text.unspecified}
                    </td>
                    <td className={cell}>
                      {evidence
                        ? evidence.compatible
                          ? text.compatible
                          : text.incompatible
                        : text.unspecified}
                    </td>
                    <td className={cell}>
                      {evidence?.policy || text.unspecified}
                    </td>
                    <td className={cell}>
                      {evidence?.locations.length ? (
                        <ul className="m-0 list-none space-y-1 p-0">
                          {evidence.locations.map((location) => (
                            <li key={location.id}>
                              <a
                                className="text-[var(--accent)] underline"
                                href={location.href}
                              >
                                {location.path}
                              </a>
                              <span> · {location.details}</span>
                            </li>
                          ))}
                        </ul>
                      ) : (
                        text.noLocations
                      )}
                    </td>
                  </>
                )}
                {inspection && (
                  <td className={cell}>
                    <AppButton
                      type="button"
                      aria-pressed={inspection.value === row.id}
                      onPress={() => inspection.onChange(row.id)}
                    >
                      {inspection.value === row.id
                        ? text.inspecting
                        : text.inspect}
                    </AppButton>
                  </td>
                )}
              </tr>
            );
          })}
        </tbody>
      </table>
    </section>
  ) : (
    <p>{choice ? text.noLinkedSources : text.noSources}</p>
  );
  return (
    <section
      aria-label={`${text.section} ${position}`}
      className="grid min-w-0 gap-2 rounded border border-[var(--line)] p-3"
    >
      <h3 className="m-0 font-semibold">{text.heading}</h3>
      {choice ? (
        <>
          <p className="m-0 text-xs text-[var(--muted)]">
            {text.publicationHelp}
          </p>
          <RadioGroup
            aria-label={text.publicationSource}
            value={choice.value}
            onChange={choice.onChange}
          >
            {table}
          </RadioGroup>
        </>
      ) : (
        table
      )}
    </section>
  );
}
