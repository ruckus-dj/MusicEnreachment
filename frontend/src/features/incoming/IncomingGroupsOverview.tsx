import type {
  IncomingGroupsBody,
  MatchingReview,
} from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import { useI18n } from "../../i18n/I18nProvider";
import {
  type IncomingGroupName,
  incomingGroupNameText,
} from "./incomingGroupNames";
import { incomingMessages } from "./incomingMessages";

const cellClassName = "border-b p-2 align-top wrap-anywhere";

export function IncomingGroupsOverview({
  snapshot,
  groupNames,
  selectedGroupId,
  review,
  onSelect,
}: {
  snapshot: IncomingGroupsBody;
  groupNames: ReadonlyMap<string, IncomingGroupName>;
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
      <div className="mt-2 min-w-0 overflow-x-auto">
        <table className="w-full min-w-[36rem] table-fixed border-collapse text-left text-sm">
          <caption className="sr-only">{copy.overviewCaption}</caption>
          <colgroup>
            <col className="w-[36%]" />
            <col className="w-[28%]" />
            <col className="w-[14%]" />
            <col className="w-[22%]" />
          </colgroup>
          <thead>
            <tr>
              <th scope="col" className={cellClassName}>
                {copy.group}
              </th>
              <th scope="col" className={cellClassName}>
                {copy.uniqueFilesPaths}
              </th>
              <th scope="col" className={cellClassName}>
                {copy.readyTotal}
              </th>
              <th scope="col" className={cellClassName}>
                {copy.evaluationState}
              </th>
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
              const label = incomingGroupNameText(
                groupNames.get(group.id),
                locale,
              );
              return (
                <tr key={group.id}>
                  <td className={cellClassName}>
                    <AppButton
                      className="max-w-full wrap-anywhere text-left"
                      onPress={() => onSelect(group.id)}
                    >
                      {copy.openGroup(label)}
                    </AppButton>
                    <p>
                      {group.manual
                        ? copy.manualGrouping
                        : copy.automaticGrouping}
                    </p>
                  </td>
                  <td className={cellClassName}>
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
                  <td className={cellClassName}>
                    {[...members].filter((id) => !unready.has(id)).length} /{" "}
                    {members.size}
                  </td>
                  <td className={cellClassName}>
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
