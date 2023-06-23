package healthchecker

import "fmt"

// HealthState represents the state of a connection. Their natural ordering is
// for "better" states to be before "worse" states. So Healthy is the lowest
// value and Unhealthy is the highest.
type HealthState int

const (
	Healthy   = HealthState(-1)
	Unknown   = HealthState(0)
	Degraded  = HealthState(1)
	Unhealthy = HealthState(2)
)

//nolint:gochecknoglobals
var (
	statesInOrder = []HealthState{Healthy, Unknown, Degraded, Unhealthy}
)

func (s HealthState) String() string {
	switch s {
	case Healthy:
		return "healthy"
	case Degraded:
		return "degraded"
	case Unhealthy:
		return "unhealthy"
	case Unknown:
		return "unknown"
	default:
		return fmt.Sprintf("HealthState(%d)", s)
	}
}
