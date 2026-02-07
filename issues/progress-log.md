# Progress Log

## [2026-02-07 15:05:26 MST] Session resumed
- Prioritized: fix global chat flashing/remount behavior before spec implementation.
- Remaining:
  - Validate + push flashing fix
  - Break down Spec 001 into GitHub issues with tests
  - Implement Spec 001 issues
  - Break down Spec 002 into GitHub issues with tests
  - Implement Spec 002 issues

## [2026-02-07 15:06:36 MST] Completed chat flashing fix
- Refactored global chat surface reload dependencies to use stable conversation identity (type/key/thread/project/issue IDs) instead of full conversation object metadata.
- Added regression tests in web/src/components/chat/GlobalChatSurface.test.tsx:
  - Metadata-only conversation updates do not trigger refetch.
  - Switching to a different conversation does trigger refetch.
- Validation:
  - web: npm test -- GlobalChatSurface.test.tsx --run
  - web: npm run build:typecheck
- Remaining:
  - Decompose Spec 001 into small GitHub issues with test lists
  - Implement Spec 001 issues in order (commit + push each)
  - Decompose Spec 002 into small GitHub issues with test lists
  - Implement Spec 002 issues in order (commit + push each)

