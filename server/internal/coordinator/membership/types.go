package membership

import "time"

type State uint8

const (
	StateUnknown State = iota
	StateHealthy
	StateSuspect
	StateDead
	StateDraining
)

type Member struct {
	LogicalZoneID       string
	IncarnationID       string
	Endpoint            string
	Namespace           string
	PodName             string
	PodUID              string
	ResourceVersion     string
	State               State
	ConsecutiveFailures int
	ObservedAt          time.Time
}

type Observation Member

type Snapshot struct {
	AvailabilityVersion uint64
	Members             []Member
}
