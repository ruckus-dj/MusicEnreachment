import type {
  IncomingGroupsBody,
  MatchingReview,
} from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import { useI18n } from "../../i18n/I18nProvider";
import { incomingMessages } from "./incomingMessages";

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
  const { locale } = useI18n();
  const copy = incomingMessages[locale];
  const files = new Map(
    (snapshot.files ?? []).map((file) => [file.variant_id, file]),
  );
  return (
    <section aria-labelledby="incoming-overview-title">
      <h2 id="incoming-overview-title">{copy.overviewTitle}</h2>
      <div className="mt-2 overflow-x-auto">
        <table className="w-full border-collapse text-left text-sm">
          <caption className="sr-only">{copy.overviewCaption}</caption>
          <thead>
            <tr>
              <th className="border-b p-2">{copy.group}</th>
              <th className="border-b p-2">{copy.uniqueFilesPaths}</th>
              <th className="border-b p-2">{copy.readyTotal}</th>
              <th className="border-b p-2">{copy.evaluationState}</th>
            </tr>
          </thead>
          <tbody>
            {(snapshot.groups ?? []).map((group, index) => {
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
                      {copy.openGroup(index + 1)}
                    </AppButton>
                    <p>
                      {group.manual
                        ? copy.manualGrouping
                        : copy.automaticGrouping}
                    </p>
                  </td>
                  <td className="border-b p-2">
                    {members.size} / {paths.size}
                    <details className="mt-1">
                      <summary>{copy.filePaths}</summary>
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
                        ? copy.candidatesAuthority(
                            selectedReview.candidates?.length ?? 0,
                            selectedReview.authority_mode,
                          )
                        : copy.evaluationNotReceived
                      : copy.openGroupForEvaluation}
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
