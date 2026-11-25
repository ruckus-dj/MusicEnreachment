import { GitHubTokenFormView } from "./GitHubTokenFormView";
import {
	type GitHubTokenDemoProps,
	useGitHubTokenDemo,
} from "./useGitHubTokenDemo";

export type { GitHubTokenDemoProps } from "./useGitHubTokenDemo";

export function GitHubTokenDemo(props: GitHubTokenDemoProps) {
	return <GitHubTokenFormView {...useGitHubTokenDemo(props)} />;
}
