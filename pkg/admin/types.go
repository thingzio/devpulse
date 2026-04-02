package admin

type upgradeRequest struct {
	Username string `json:"username"`
	Plan     string `json:"plan"`
}

type upgradeResponse struct {
	Username         string `json:"username"`
	Plan             string `json:"plan"`
	MaxRepos         int    `json:"max_repos"`
	MaxEventsPerWeek int    `json:"max_events_per_week"`
}

type tenantSummary struct {
	Username         string `json:"username"`
	Plan             string `json:"plan"`
	MaxRepos         int    `json:"max_repos"`
	MaxEventsPerWeek int    `json:"max_events_per_week"`
	CreatedAt        string `json:"created_at"`
	LastSignIn       string `json:"last_sign_in"`
}

type tenantDetail struct {
	Username         string       `json:"username"`
	Email            string       `json:"email"`
	Plan             string       `json:"plan"`
	MaxRepos         int          `json:"max_repos"`
	MaxEventsPerWeek int          `json:"max_events_per_week"`
	CreatedAt        string       `json:"created_at"`
	LastSignIn       string       `json:"last_sign_in"`
	Repos            []repoDetail `json:"repos"`
}

type repoDetail struct {
	Name         string  `json:"name"`
	Events       int     `json:"events"`
	WeeklyEvents int     `json:"weekly_events"`
	WeeklyPct    float64 `json:"weekly_pct"`
	LastImport   string  `json:"last_import"`
}
