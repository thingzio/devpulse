package plan

const (
	Free       = "free"
	Pro        = "pro"
	Enterprise = "enterprise"
)

type Limits struct {
	MaxRepos         int
	MaxEventsPerWeek int
}

var All = map[string]Limits{
	Free:       {MaxRepos: 3, MaxEventsPerWeek: 1000},
	Pro:        {MaxRepos: 15, MaxEventsPerWeek: 15000},
	Enterprise: {MaxRepos: 0, MaxEventsPerWeek: 0}, // 0 = unlimited
}

// Get returns the limits for a plan name and whether it exists.
func Get(name string) (Limits, bool) {
	l, ok := All[name]
	return l, ok
}

// FreeLimits returns the free plan limits.
func FreeLimits() Limits {
	return All[Free]
}
