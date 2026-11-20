import { useState } from "react";
import { GitHubTokenFormView } from "./GitHubTokenFormView";
import type { GitHubTokenLocale } from "./toolMessages";

export type GitHubTokenDemoProps = {
	locale: GitHubTokenLocale;
	hasGitHubToken: boolean;
	onGitHubTokenChange: (hasGitHubToken: boolean) => void;
};

// Prototype controller: the draft lives in this component's memory for the
// current page only. It is never stored, logged, or put into the URL, and the
// demo settings keep only whether a placeholder token is configured. Saving
// is simulated and never contacts GitHub.
export function GitHubTokenDemo({
	locale,
	hasGitHubToken,
	onGitHubTokenChange,
}: GitHubTokenDemoProps) {
	const [draft, setDraft] = useState("");
	const [saved, setSaved] = useState(false);

	const save = () => {
		if (!draft.trim()) return;
		onGitHubTokenChange(true);
		setDraft("");
		setSaved(true);
	};
	const remove = () => {
		onGitHubTokenChange(false);
		setDraft("");
		setSaved(false);
	};
	const changeDraft = (nextDraft: string) => {
		setDraft(nextDraft);
		setSaved(false);
	};

	return (
		<GitHubTokenFormView
			locale={locale}
			simulated
			hasToken={hasGitHubToken}
			draft={draft}
			busy={false}
			saved={saved}
			error={null}
			onDraftChange={changeDraft}
			onSave={save}
			onRemove={remove}
		/>
	);
}
