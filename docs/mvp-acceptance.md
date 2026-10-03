# MVP acceptance criteria — status

Each criterion of PRD §18 ([`work-agent-prd-updated.md`](work-agent-prd-updated.md)) with the tests that prove it. The `cmd/work-agent` tests drive the real CLI against stub `gh` / `orca` / `git` / `claude` binaries and reload `state.json` on every run, so each Tick in them is also a restart. **Live** marks what the #17 dogfood also showed on a real repo, Orca and Claude.

| # | Criterion | Proven by | Status |
| --- | --- | --- | --- |
| 1 | Tick discovers a newly assigned Eligible issue; an assigned issue without the Eligibility Label is ignored | `cmd/work-agent`: `TestTickListsEligibleIssuesInAllowlistedRepos`, `TestTickAddsOnlyNewlyEligibleIssues` | ✅ Live |
| 2 | Exactly one Workspace per issue | `TestTickCreatesAWorkspaceAndWakesClaudeForEachOwnedIssue`; `internal/workspace`: `TestCreateForIssueMakesOneOrcaWorktreeLinkedToTheIssue` | ✅ Live |
| 3 | Re-running the Tick creates no duplicates | `TestReRunningTheTickCreatesNoDuplicatesAndKeepsState`, `TestRepeatedTicksKeepExactlyOneWorkspacePerOwnedIssue`; `internal/workflow`: `TestReconcileAppliesEachEventOnce` | ✅ Live |
| 4 | Woken with `<entrySkill> issue <repo>#<n>` and a daemon-owned session ID | `TestTickCreatesAWorkspaceAndWakesClaudeForEachOwnedIssue`, `TestTickUsesTheConfiguredEntrySkill`; `internal/workspace`: `TestWakeStartsClaudeInTheWorkspaceWithItsSessionIDAndThePrompt` | ✅ Live |
| 5 | A PR is associated with its Work Item | `TestAPRForTheIssueIsLinkedToItsWorkItem`, `TestAPROpenedBySomeoneElseIsNotLinked`; `internal/github`: `TestPullRequestCoversIssue` | ✅ |
| 6 | A submitted review or quiet comment batch Wakes the same Workspace once, resuming the conversation | `TestASubmittedReviewWakesTheSameWorkspaceOnceResumingTheConversation`, `TestAQuietCommentBatchWakesOnce`, `TestFeedbackFromAuthorsWithoutWriteAccessNeverWakes` | ✅ (live run needs a second reviewer — #37) |
| 7 | Re-observing the same review/comments does nothing | the #6 tests (Tick twice more, still one Wake); `TestReconcileAppliesEachEventOnce` | ✅ |
| 8 | A Settled CI failure Wakes once per head SHA; running checks never Wake | `TestASettledCIFailureWakesTheSameWorkspaceOncePerHeadSHA`, `TestStillRunningChecksNeverWake`; `internal/github`: `TestSettle` | ✅ |
| 9 | A restart keeps associations, Held Wakes and dedupe state | `internal/state`: `TestSavedStateSurvivesARestart`; `TestACIFailureWakeIsHeldWhileTheAgentIsWorkingAcrossRestarts`; `cmd/work-agent-tray`: `TestAManualRestartAfterACrashCreatesNoDuplicateWorkspacesOrWakes` | ✅ |
| 10 | A merged PR moves the Work Item to `DONE` without Waking | `TestAMergedPRMovesTheItemToDoneWithoutWakingClaude`, `TestAClosedPRMovesTheItemToDone`, `TestAMergeWhileTheDaemonWasStoppedIsReconciledOnTheNextTick` | ✅ |
| 11 | Another developer's review request makes one Review Request and Review Workspace per head SHA, after CI is Settled | `TestAReviewRequestGetsOneReviewWorkspaceAndReviewWakeOnceCIIsSettled`, `TestAReviewWorkspaceIsNeverBuiltFromAHeadWhoseCIIsNotSettled`; `internal/workspace`: `TestCreateForReviewFetchesThePullRefAndBranchesANewWorktreeFromIt` | ✅ (live run needs a second developer — #37) |
| 12 | Woken with `<entrySkill> review <repo>#<pr>`, routed to `/code-review PR#<pr>` by default | the #11 CLI test; `skills`: `TestDefaultRoutingCoversEveryWakeReason` | ✅ |
| 13 | Re-observing the same request/head creates or Wakes nothing | the #11 CLI test; `internal/workflow`: `TestReObservingATrackedReviewRequestDoesNothing` | ✅ |
| 14 | A new head while still requested Wakes the existing Review Workspace once, after the Quiet Period | `TestANewHeadWhileRequestedWakesTheExistingReviewWorkspaceOnce`, `TestRapidPushesInsideTheQuietPeriodProduceOneReReview` | ✅ |
| 15 | Review Workspaces cannot push to the author's branch | `internal/workspace`: `TestAReviewWorkspaceCannotPushToTheAuthorsBranch` (no upstream; bare `git push` moves nothing) | ✅ with a gap: explicit `git push origin HEAD:<branch>` — #38 |
| 16 | Review submission stays Operator-gated | `internal/process`: `TestCheckRejectsMutatingGitHubCalls`; `skills`: `TestSkillStatesPushPolicy`, `TestForbiddenActionsAppearOnlyAsGuardrails` | ✅ for the daemon; the agent's side is Entry Skill policy — #38 |
| 17 | No code path can merge, force push or deploy | `internal/process`: `policy_test.go` (all), `TestRunRefusesDisallowedCommandsWithoutStartingAProcess`; `TestThePRLifecycleReachesReadyToMergeAndRegressesOnANewHead` | ✅ with gaps: `orca` arguments unrestricted, no `os/exec` import guard — #38 |
| 18 | Removing the Eligibility Label pauses; re-adding resumes | `TestRemovingTheEligibilityLabelPausesAndReAddingResumes`, `TestReassigningAwayFromTheOperatorPauses` | ✅ |
| 19 | With Orca closed, a Tick records events, Holds, notifies, and completes once Orca returns | `TestWithOrcaDownTheTickRecordsEventsHoldsAndCompletesWhenOrcaReturns`; `internal/core`: `TestOrcaUnavailableIsNotifiedOncePerOutage` | ✅ |
| 20 | A Wake is Held while the agent is `working` | `TestACIFailureWakeIsHeldWhileTheAgentIsWorkingAcrossRestarts`; `internal/workflow`: `TestAWakeForAWorkingAgentIsLeftToBeHeldAsAgentWorking` | ✅ |
| 21 | Refuses to start if `gh` cannot act as the Operator | `TestTickRefusesWhenGHIsNotLoggedInAsTheOperator`, `TestTickRefusesWhenTheTokenBelongsToSomeoneElse`, `TestEveryGitHubCallActsAsTheOperator` | ✅ for the CLI; no tray-app test — #38 |

Follow-ups: [#37](https://github.com/dwatts1772/work-agent-daemon/issues/37) (a solo Operator's PR cannot reach `READY_TO_MERGE`; no feedback or Review Request without a second write-access account) and [#38](https://github.com/dwatts1772/work-agent-daemon/issues/38) (the test gaps above).
