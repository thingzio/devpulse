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
	Email            string `json:"email"`
	Name             string `json:"name"`
	Plan             string `json:"plan"`
	MaxRepos         int    `json:"max_repos"`
	MaxEventsPerWeek int    `json:"max_events_per_week"`
	CreatedAt        string `json:"created_at"`
	LastSignIn       string `json:"last_sign_in"`
}

type tenantDetail struct {
	Username         string       `json:"username"`
	Email            string       `json:"email"`
	Name             string       `json:"name"`
	Company          string       `json:"company"`
	Location         string       `json:"location"`
	Bio              string       `json:"bio"`
	Plan             string       `json:"plan"`
	MaxRepos         int          `json:"max_repos"`
	MaxEventsPerWeek int          `json:"max_events_per_week"`
	CreatedAt        string       `json:"created_at"`
	LastSignIn       string       `json:"last_sign_in"`
	Repos            []repoDetail `json:"repos"`
}

type repoDetail struct {
	Name           string  `json:"name"`
	Events         int     `json:"events"`
	WeeklyEvents   int     `json:"weekly_events"`
	WeeklyPct      float64 `json:"weekly_pct"`
	LastImport     string  `json:"last_import"`
	PRTotal        int     `json:"pr_total"`
	PRMissingSize  int     `json:"pr_missing_size"`
	Contributors   int     `json:"contributors"`
	Scored         int     `json:"scored"`
	BackfillDays   int     `json:"backfill_days"`
	BackfillTarget int     `json:"backfill_target"`
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

type tokenStatus struct {
	Login          string `json:"login"`
	InstallationID int64  `json:"installation_id"`
	Limit          int    `json:"limit"`
	Used           int    `json:"used"`
	Remaining      int    `json:"remaining"`
	ResetAt        string `json:"reset_at,omitempty"`
	Error          string `json:"error,omitempty"`
}

type inviteResponse struct {
	Username         string `json:"username"`
	GitHubID         int64  `json:"github_id"`
	Plan             string `json:"plan"`
	MaxRepos         int    `json:"max_repos"`
	MaxEventsPerWeek int    `json:"max_events_per_week"`
}

type platformStats struct {
	Tenants           int   `json:"tenants"`
	TenantsFree       int   `json:"tenants_free"`
	TenantsStarter    int   `json:"tenants_starter"`
	TenantsPro        int   `json:"tenants_pro"`
	TenantsEnterprise int   `json:"tenants_enterprise"`
	Repos             int   `json:"repos"`
	Events            int64 `json:"events"`
	Contributors      int   `json:"contributors"`
	Installations     int   `json:"installations"`
	ReposWithErrors   int   `json:"repos_with_errors"`
}

type statsDelta struct {
	Tenants         *int     `json:"tenants,omitempty"`
	TenantsPct      *float64 `json:"tenants_pct,omitempty"`
	Repos           *int     `json:"repos,omitempty"`
	ReposPct        *float64 `json:"repos_pct,omitempty"`
	Events          *int64   `json:"events,omitempty"`
	EventsPct       *float64 `json:"events_pct,omitempty"`
	Contributors    *int     `json:"contributors,omitempty"`
	ContribPct      *float64 `json:"contributors_pct,omitempty"`
	Installations   *int     `json:"installations,omitempty"`
	InstallPct      *float64 `json:"installations_pct,omitempty"`
	ReposWithErrors *int     `json:"repos_with_errors,omitempty"`
	ErrorsPct       *float64 `json:"repos_with_errors_pct,omitempty"`
}

type errorRepo struct {
	Org       string `json:"org"`
	Repo      string `json:"repo"`
	Errors    int    `json:"errors"`
	LastError string `json:"last_error"`
}

type summaryResponse struct {
	Date       string        `json:"date"`
	Current    platformStats `json:"current"`
	DoD        *statsDelta   `json:"dod,omitempty"`
	WoW        *statsDelta   `json:"wow,omitempty"`
	MoM        *statsDelta   `json:"mom,omitempty"`
	ErrorRepos []errorRepo   `json:"error_repos"`
	UpdatedAt  string        `json:"updated_at"`
}

type reportResponse struct {
	Sent  bool   `json:"sent"`
	Error string `json:"error,omitempty"`
}

type reportConfig struct {
	SendAPIKey    string
	ToEmail       string
	FromEmail     string
	SubjectPrefix string
}
