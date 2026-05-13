// Package poll runs the hourly (configurable via HMI_UPDATE_CRON) registry
// poll loop that compares current vs upstream digests for every watched
// container.
//
// Phase 1 ships the interface only; the body lands in phase 3 (DETECT-09..12).
package poll

// Poller orchestrates the periodic digest-comparison sweep. Plan-04's
// internal/api server depends on this interface so the HTTP handlers can
// surface "last polled" status without depending on the cron scheduler
// directly.
//
// TODO(phase-3): implement — robfig/cron/v3 + the single-consumer state
// update channel land here.
type Poller interface{}
