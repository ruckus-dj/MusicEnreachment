import { catalogMessage } from "./catalogMessages";
import { incomingMessage } from "./incomingMessages";
import { matchingMessage } from "./matchingMessages";
import {
	publicationMessage,
	formatPublicationMessage,
} from "./publicationMessages";
import type { Locale } from "./i18n/types";

type NoticeParams = Record<string, string | number>;

export type UiNotice =
	| {
			namespace: "matching";
			id:
				| "unavailable"
				| "alreadyAssigned"
				| "assigned"
				| "addFailed"
				| "candidateChanged"
				| "candidateChangeNotice"
				| "planPublished"
				| "linksSavedFilesUnchanged"
				| "missingAssignment"
				| "recordingLinked";
			params?: NoticeParams;
	  }
	| {
			namespace: "publication";
			id: "applySuccess";
			params: { count: number; linkCount: number };
	  }
	| {
			namespace: "incoming";
			id: "automaticPublished";
			params: { count: number };
	  }
	| { namespace: "incoming"; id: "nothingToPublish" }
	| {
			namespace: "library";
			id: "localReleaseSelected";
			params: { title: string };
	  };

export function formatUiNotice(locale: Locale, notice: UiNotice): string {
	switch (notice.namespace) {
		case "matching":
			if (notice.id === "planPublished") {
				return `${matchingMessage(locale, "published", { count: notice.params?.count ?? 0 })}${notice.params?.linkedCount ? ` ${matchingMessage(locale, "linksSaved", { count: notice.params.linkedCount })}` : ""} ${matchingMessage(locale, "unchangedSources")}`;
			}
			if (notice.id === "linksSavedFilesUnchanged") {
				return `${matchingMessage(locale, "linksSaved", { count: notice.params?.count ?? 0 })} ${matchingMessage(locale, "filesUnchanged")}`;
			}
			return matchingMessage(locale, notice.id, notice.params);
		case "publication":
			return [
				formatPublicationMessage(locale, "applySuccess", {
					count: notice.params.count,
				}),
				...(notice.params.linkCount
					? [
							formatPublicationMessage(locale, "linkSuccess", {
								count: notice.params.linkCount,
							}),
						]
					: []),
				publicationMessage(locale, "unchangedSuccess"),
			].join(" ");
		case "incoming":
			return notice.id === "automaticPublished"
				? incomingMessage(locale, "automaticNotice", notice.params)
				: incomingMessage(locale, "noAutomaticNotice");
		case "library":
			return catalogMessage(locale, "localReleaseSelected", notice.params);
	}
}
