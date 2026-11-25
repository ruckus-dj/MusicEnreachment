import { type ComponentProps, useEffect, useState } from "react";
import type { GitHubTokenFormView } from "../../../ui/features/tools/GitHubTokenFormView";
import type { GitHubTokenLocale } from "../../../ui/features/tools/toolMessages";

export type GitHubTokenDemoProps = {
	locale: GitHubTokenLocale;
	hasGitHubToken: boolean;
	onGitHubTokenChange: (hasGitHubToken: boolean) => void;
};

// Only token presence is saved; the secret draft is transient panel state.
export function useGitHubTokenDemo(
	{ locale, hasGitHubToken, onGitHubTokenChange }: GitHubTokenDemoProps,
	active = true,
): ComponentProps<typeof GitHubTokenFormView> {
	const [draft, setDraft] = useState("");
	const [saved, setSaved] = useState(false);
	useEffect(() => {
		if (active) return;
		setDraft("");
		setSaved(false);
	}, [active]);

	return {
		locale,
		simulated: true,
		hasToken: hasGitHubToken,
		draft,
		saved,
		busy: false,
		error: null,
		onDraftChange: (next) => {
			setDraft(next);
			setSaved(false);
		},
		onSave: () => {
			if (!draft.trim()) return;
			onGitHubTokenChange(true);
			setDraft("");
			setSaved(true);
		},
		onRemove: () => {
			onGitHubTokenChange(false);
			setDraft("");
			setSaved(false);
		},
	};
}
