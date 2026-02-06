import { describe, expect, it } from 'vitest';

import { getGitHubItemBadge } from './githubItems';

describe('getGitHubItemBadge', () => {
  it('renders PR-specific badges without issue assumptions', () => {
    expect(getGitHubItemBadge({ kind: 'pull_request', state: 'open', draft: true, merged: false })).toBe('Draft PR');
    expect(getGitHubItemBadge({ kind: 'pull_request', state: 'closed', merged: true })).toBe('Merged');
    expect(getGitHubItemBadge({ kind: 'pull_request', state: 'closed', merged: false })).toBe('Closed PR');
    expect(getGitHubItemBadge({ kind: 'pull_request', state: 'open', merged: false })).toBe('Open PR');
  });

  it('keeps issue badges distinct from PR badges', () => {
    expect(getGitHubItemBadge({ kind: 'issue', state: 'open' })).toBe('Open Issue');
    expect(getGitHubItemBadge({ kind: 'issue', state: 'closed' })).toBe('Closed Issue');
  });
});
