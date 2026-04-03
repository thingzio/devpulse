package plan

const (
	Free       = "free"
	Starter    = "starter"
	Pro        = "pro"
	Enterprise = "enterprise"
)

type Limits struct {
	MaxRepos           int
	MaxEventsPerWeek   int
	MaxDataRangeMonths int
	AILevel            int
	DeepReputation     bool
	PDFExport          bool
	CSVExport          bool
}

var All = map[string]Limits{
	Free:       {MaxRepos: 1, MaxEventsPerWeek: 500, MaxDataRangeMonths: 3, AILevel: 0, DeepReputation: false, PDFExport: false, CSVExport: false},
	Starter:    {MaxRepos: 5, MaxEventsPerWeek: 2500, MaxDataRangeMonths: 12, AILevel: 1, DeepReputation: false, PDFExport: true, CSVExport: false},
	Pro:        {MaxRepos: 25, MaxEventsPerWeek: 15000, MaxDataRangeMonths: 36, AILevel: 2, DeepReputation: true, PDFExport: true, CSVExport: true},
	Enterprise: {MaxRepos: 0, MaxEventsPerWeek: 0, MaxDataRangeMonths: 0, AILevel: 2, DeepReputation: true, PDFExport: true, CSVExport: true}, // 0 = unlimited
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
