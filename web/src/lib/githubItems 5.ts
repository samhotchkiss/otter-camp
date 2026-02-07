export type GitHubItemKind = 'issue' | 'pull_request';

export interface GitHubItemView {
  kind: GitHubItemKind;
  state: 'open' | 'closed';
  draft?: boolean;
  merged?: boolean;
}

export function getGitHubItemBadge(item: GitHubItemView): string {
  if (item.kind === 'pull_request') {
    if (item.merged) {
      return 'Merged';
    }
    if (item.draft) {
      return 'Draft PR';
    }
    if (item.state === 'closed') {
      return 'Closed PR';
    }
    return 'Open PR';
  }

  if (item.state === 'closed') {
    return 'Closed Issue';
  }

  return 'Open Issue';
}
