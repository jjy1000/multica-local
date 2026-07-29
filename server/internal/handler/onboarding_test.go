package handler

// ---------------------------------------------------------------------------
// 0.3.66 (M3 PR1 + PR2): BootstrapOnboarding* (PR1) and
// JoinCloudWaitlist (PR2) handlers and their tests were removed.
// JoinCloudWaitlist's newWaitlistTestUser / newWaitlistRequest
// helpers are gone with it. The only test file left for onboarding
// is onboarding_test.go itself, which now serves as a structural
// placeholder until a future contributor adds coverage for the
// active onboarding paths (PatchOnboarding / CompleteOnboarding /
// starter content state).
// ---------------------------------------------------------------------------
