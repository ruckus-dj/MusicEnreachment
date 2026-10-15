import type {
  IncomingGroupsBody,
  MatchingReview,
} from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";

export function IncomingGroupsOverview({
  snapshot,
  selectedGroupId,
  review,
  onSelect,
}: {
  snapshot: IncomingGroupsBody;
  selectedGroupId: string;
  review?: MatchingReview;
  onSelect: (groupId: string) => void;
}) {
  const files = new Map(
    (snapshot.files ?? []).map((file) => [file.variant_id, file]),
  );
  return (
    <section aria-labelledby="incoming-overview-title">
      <h2 id="incoming-overview-title">Входящие группы</h2>
      <div className="mt-2 overflow-x-auto">
        <table className="w-full border-collapse text-left text-sm">
          <caption className="sr-only">
            Файлы, пути, готовность источников и сохранённая оценка групп
          </caption>
          <thead>
            <tr>
              <th className="border-b p-2">Группа</th>
              <th className="border-b p-2">Уникальные файлы / пути</th>
              <th className="border-b p-2">Готово / всего</th>
              <th className="border-b p-2">Состояние оценки</th>
            </tr>
          </thead>
          <tbody>
            {(snapshot.groups ?? []).map((group) => {
              const unready = new Set(group.unready_members ?? []);
              const members = new Set([...(group.members ?? []), ...unready]);
              const paths = new Map(
                [...members].flatMap((id) =>
                  (files.get(id)?.locations ?? []).map((location) => [
                    `${location.root_id}:${location.relative_path}`,
                    `${location.configured_path} / ${location.relative_path}`,
                  ]),
                ),
              );
              const selectedReview =
                group.id === selectedGroupId ? review : undefined;
              return (
                <tr key={group.id}>
                  <td className="border-b p-2">
                    <AppButton onPress={() => onSelect(group.id)}>
                      Открыть {group.id}
                    </AppButton>
                    <p>
                      {group.manual
                        ? "Ручная группировка"
                        : "Автоматическая группировка"}
                    </p>
                  </td>
                  <td className="border-b p-2">
                    {members.size} / {paths.size}
                    <details className="mt-1">
                      <summary>Пути файлов</summary>
                      <ul>
                        {[...paths].map(([key, label]) => (
                          <li key={key}>{label}</li>
                        ))}
                      </ul>
                    </details>
                  </td>
                  <td className="border-b p-2">
                    {[...members].filter((id) => !unready.has(id)).length} /{" "}
                    {members.size}
                  </td>
                  <td className="border-b p-2">
                    {selectedReview
                      ? selectedReview.evaluated
                        ? `${selectedReview.candidates?.length ?? 0} кандидатов · ${selectedReview.authority_mode}`
                        : "Оценка ещё не получена"
                      : "Откройте группу для чтения оценки"}
                    {group.diagnostics?.map((diagnostic) => (
                      <p key={`${diagnostic.variant_id}:${diagnostic.code}`}>
                        {diagnostic.code}
                      </p>
                    ))}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </section>
  );
}
