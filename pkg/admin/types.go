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
	Name          string  `json:"name"`
	Events        int     `json:"events"`
	WeeklyEvents  int     `json:"weekly_events"`
	WeeklyPct     float64 `json:"weekly_pct"`
	LastImport    string  `json:"last_import"`
	PRTotal       int     `json:"pr_total"`
	PRMissingSize int     `json:"pr_missing_size"`
	Contributors  int     `json:"contributors"`
	Scored        int     `json:"scored"`
}

type resetErrorsRequest struct {
	Org  string `json:"org"`
	Repo string `json:"repo"`
}

type resetErrorsResponse struct {
	Org   string `json:"org"`
	Repo  string `json:"repo"`
	Reset int64  `json:"reset"`
}

type inviteRequest struct {
	Username string `json:"username"`
	Plan     string `json:"plan"`
}

type inviteResponse struct {
	Username         string `json:"username"`
	GitHubID         int64  `json:"github_id"`
	Plan             string `json:"plan"`
	MaxRepos         int    `json:"max_repos"`
	MaxEventsPerWeek int    `json:"max_events_per_week"`
}
