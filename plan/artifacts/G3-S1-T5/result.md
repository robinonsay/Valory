task_id       G3-S1-T5
status        done
artifact_path internal/agent/generation_lifecycle_integration_test.go ; plan/artifacts/G3-S1-T5/
acceptance    pass — all 8 scenarios implemented; 11 test functions GREEN live against real PG16
              under migration 024 schema, with SET ROLE valory_app via AcquireAsUser
deviations    Tree storm-bound test (scenario 1 tree): the test manually resets the course to
              'syllabus_approved' between dispatchTreeCourse iterations (with a comment explaining
              why). This is required because seedTreeAndGenerateRoot transitions the course to
              'generating' before calling GenerateLayer; a production retry of a tree course in
              the 'generating' state goes through pollLayeredGeneration, not pollAndGenerate.
              Resetting to 'syllabus_approved' between iterations simulates the operator re-trigger
              path and exercises the EXACT same handleFailedRun → IncrementAttemptCount →
              SetCourseTerminal guard that the flat-course test covers. The test is non-vacuous:
              removing SetCourseTerminal causes course to stay 'syllabus_approved' indefinitely
              and the loop dispatches maxAttempts+2 times, violating the run count bound.

Live test run (2026-06-14):

  go test -tags integration -count=1 -p 1 -timeout 300s -run TestGenLifecycle ./internal/agent/ -v

  --- PASS: TestGenLifecycle_StormBound_FlatCourse_RunsLimitedToMaxAttempts (0.09s)
  --- PASS: TestGenLifecycle_StormBound_TreeCourse_RunsLimitedToMaxAttempts (0.05s)
  --- PASS: TestGenLifecycle_IdempotentDispatch_ForcedRace_ExactlyOneRun (0.01s)
  --- PASS: TestGenLifecycle_BackoffSpacing_CourseExcludedDuringWindow_ThenReeligible (0.01s)
  --- PASS: TestGenLifecycle_MaxAttempts_Terminal_D22_DoesNotBlockNewCourse (0.03s)
  --- PASS: TestGenLifecycle_Recovery_RecoverGenerationFailed_ReturnsToEligibility (0.01s)
  --- PASS: TestGenLifecycle_TokenCapPreFlight_FlatCourse_ZeroPaidCalls (0.01s)
  --- PASS: TestGenLifecycle_TokenCapPreFlight_TreeCourse_ZeroPaidCalls (0.01s)
  --- PASS: TestGenLifecycle_D21_D24_TreeResetFiresOnlyAtContentLayer (0.02s)
  --- PASS: TestGenLifecycle_D21_ResetDoesNotFireAtSectionGoalLayer (0.01s)
  --- PASS: TestGenLifecycle_HappyPath_FlatCourse_ReachesActive (0.02s)
  --- PASS: TestGenLifecycle_HappyPath_TreeSeed_ReachesAwaitingLayerApproval (0.03s)
  PASS
  ok  github.com/valory/valory/internal/agent 0.580s

  go build ./... && go vet ./... → clean (no output)

Acceptance criteria check:
  [x] All 8 scenarios implemented (11 test functions — 2 scenarios have flat+tree variants)
  [x] flat AND tree covered for storm-bound (1) and cost fail-fast (6)
  [x] SET ROLE valory_app via AcquireAsUser throughout; no superuser connections for RLS-sensitive paths
  [x] Real migrations through 024 (IntegrationPool applies all migrations; agent_run_type enum used)
  [x] Zero paid API calls (zeroTransport asserted; errorTransport for storm/backoff tests)
  [x] @{"req":[...]} annotations cite REQ-AGENT-064/065/066/067/068/069 across the suite
  [x] go build ./... + go vet ./... clean; 11/11 tests GREEN live
