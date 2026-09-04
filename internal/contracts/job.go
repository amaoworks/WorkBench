package contracts

import (
	"context"
	"time"
)

type JobID string
type ScheduleKind string
type OverlapPolicy string
type MisfirePolicy string

const (
	ScheduleCron     ScheduleKind = "cron"
	ScheduleInterval ScheduleKind = "interval"
	ScheduleOnce     ScheduleKind = "once"

	OverlapSkip OverlapPolicy = "skip"

	MisfireSkip    MisfirePolicy = "skip"
	MisfireRunOnce MisfirePolicy = "run_once"
)

type RetryPolicy struct {
	MaxAttempts int
	InitialWait time.Duration
	MaxWait     time.Duration
}

type ScheduleSpec struct {
	Kind       ScheduleKind
	Expression string
	Interval   time.Duration
	RunAt      time.Time
}

type JobDefinition struct {
	ID            JobID
	Module        ModuleID
	Schedule      ScheduleSpec
	TimeZone      string
	Timeout       time.Duration
	OverlapPolicy OverlapPolicy
	MisfirePolicy MisfirePolicy
	Retry         RetryPolicy
	Handler       func(context.Context, JobRun) error
}

type JobRun struct {
	ID          string
	JobID       JobID
	ScheduledAt time.Time
	Attempt     int
}
