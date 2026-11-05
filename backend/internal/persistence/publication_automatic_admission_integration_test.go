//go:build integration

package persistence

import ()

// This exercises the automatic wrapper's real delivery fence. A plausible but
// unclaimed delivery must roll back the planner's admission and all artifacts.
