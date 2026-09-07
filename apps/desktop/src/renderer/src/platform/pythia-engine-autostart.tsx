import { useEffect } from "react";
import { useExperimentalFlag } from "@multica/core/experimental";

// Labs settings dispatches this window event when a flag flips on (see
// labs-tab.tsx). The desktop renderer listens so the Pythia engine
// subprocess comes up the moment its flag is enabled — issue-bound agent
// dispatch (daemon → pythia_runtime → server forecast endpoint) requires
// the engine to be registered, and that path never mounts a lab panel
// that would ensure-up on view.
const LAB_FLAG_ENABLED_EVENT = "multica:lab-flag-enabled";

// One boot attempt per renderer session. If the spawn fails (no python3,
// staged engine missing) the flag watcher resets the guard so the next
// enable event retries; we deliberately do NOT retry on a timer — a
// machine without the runtime would burn CPU forever, and the lab panel
// still reports the boot error where the user can see it.
let attemptedThisSession = false;

/**
 * Mounts once at the App root (next to DesktopAuthSessionBridge). Renders
 * nothing; owns two responsibilities:
 *
 *  1. Boot auto-start — the app launches with pythia_oracle already
 *     enabled (the common case after the first enable) → ensureUp so
 *     issue-dispatched 预演 runs against the real engine instead of the
 *     server's synthetic fallback (0.5.102 finding: engine was never up
 *     because nothing mounted PythiaView).
 *  2. Mid-session enable — the labs settings toggle dispatches
 *     `multica:lab-flag-enabled`; react immediately instead of waiting
 *     for the next app launch or a PythiaView mount.
 */
export function PythiaEngineAutoStart() {
  const pythiaEnabled = useExperimentalFlag("pythia_oracle", false);

  useEffect(() => {
    if (!pythiaEnabled || attemptedThisSession) return;
    attemptedThisSession = true;
    window.experimentalAPI.pythia
      .ensureUp()
      .catch(() => {
        attemptedThisSession = false;
      });
  }, [pythiaEnabled]);

  useEffect(() => {
    const onFlagEnabled = (event: Event) => {
      const detail = (event as CustomEvent<{ key?: string }>).detail;
      if (detail?.key !== "pythia_oracle") return;
      window.experimentalAPI.pythia.ensureUp().catch(() => {
        // Boot error surfaces through the lab panel's status poll.
      });
    };
    window.addEventListener(LAB_FLAG_ENABLED_EVENT, onFlagEnabled);
    return () => window.removeEventListener(LAB_FLAG_ENABLED_EVENT, onFlagEnabled);
  }, []);

  return null;
}
