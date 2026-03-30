package ghutil

import (
	"testing"
	"time"

	"github.com/google/go-github/v83/github"
	"github.com/stretchr/testify/assert"
)

func TestParsingBody(t *testing.T) {
	tests := map[string]int{
		"plain string with no name":     0,
		"@username up front":            1,
		"some username on the end @foo": 1,
		"@foo username on the end @bar": 2,
	}

	for input, expected := range tests {
		names := ParseUsers(&input)
		assert.Len(t, names, expected)
	}
}

func TestParseDate(t *testing.T) {
	now := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
	assert.Equal(t, "2025-06-15", ParseDate(&now))
	assert.NotEmpty(t, ParseDate(nil))
}

func TestTrim(t *testing.T) {
	s := " @hello "
	assert.Equal(t, "hello", Trim(&s))
	assert.Equal(t, "", Trim(nil))
}

func TestGetLabels_Nil(t *testing.T) {
	assert.Empty(t, GetLabels(nil))
}

func TestGetLabels_WithValues(t *testing.T) {
	name1 := "Bug"
	name2 := "Feature"
	labels := []*github.Label{
		{Name: &name1},
		{Name: &name2},
		nil,
	}
	result := GetLabels(labels)
	assert.Len(t, result, 2)
	assert.Equal(t, "bug", result[0])
	assert.Equal(t, "feature", result[1])
}

func TestGetUsernames_Nil(t *testing.T) {
	assert.Empty(t, GetUsernames(nil))
}

func TestGetUsernames_WithValues(t *testing.T) {
	login1 := "user1"
	login2 := "user2"
	users := []*github.User{
		{Login: &login1},
		nil,
		{Login: &login2},
	}
	result := GetUsernames(users...)
	assert.Len(t, result, 2)
}

func TestParseUsers_NilBody(t *testing.T) {
	assert.Empty(t, ParseUsers(nil))
}

func TestMapUserToDeveloper(t *testing.T) {
	login := "testuser"
	name := "Test User"
	email := "test@example.com"
	avatar := "https://avatar.url"
	htmlURL := "https://github.com/testuser"
	company := "@TestCorp"
	u := &github.User{
		Login:     &login,
		Name:      &name,
		Email:     &email,
		AvatarURL: &avatar,
		HTMLURL:   &htmlURL,
		Company:   &company,
	}
	dev := MapUserToDeveloper(u)
	assert.Equal(t, "testuser", dev.Username)
	assert.Equal(t, "Test User", dev.FullName)
	assert.Equal(t, "test@example.com", dev.Email)
	assert.Equal(t, "TestCorp", dev.Entity)
}

func TestRateInfo_Nil(t *testing.T) {
	assert.Equal(t, "", RateInfo(nil))
}

func TestRateInfo_WithRate(t *testing.T) {
	r := &github.Rate{
		Remaining: 4999,
		Limit:     5000,
		Reset:     github.Timestamp{Time: time.Date(2025, 6, 15, 14, 30, 0, 0, time.UTC)},
	}
	info := RateInfo(r)
	assert.Contains(t, info, "4999")
	assert.Contains(t, info, "5000")
}

func TestMapGitHubUserToDeveloperListItem(t *testing.T) {
	login := "testuser"
	company := "TestCo"
	u := &github.User{
		Login:   &login,
		Company: &company,
	}
	item := MapGitHubUserToDeveloperListItem(u)
	assert.Equal(t, "testuser", item.Username)
	assert.Equal(t, "TestCo", item.Entity)
}

func TestGetUsernames_EmptySlice(t *testing.T) {
	result := GetUsernames([]*github.User{}...)
	assert.Empty(t, result)
}

func TestGetLabels_EmptySlice(t *testing.T) {
	result := GetLabels([]*github.Label{})
	assert.Empty(t, result)
}

func TestTrim_EmptyString(t *testing.T) {
	s := ""
	assert.Equal(t, "", Trim(&s))
}

func TestTrim_AtSign(t *testing.T) {
	s := "@org"
	assert.Equal(t, "org", Trim(&s))
}

func TestDeref(t *testing.T) {
	s := "  hello  "
	assert.Equal(t, "hello", Deref(&s))
	assert.Equal(t, "", Deref(nil))
	empty := ""
	assert.Equal(t, "", Deref(&empty))
}

func TestMapRepo(t *testing.T) {
	name := "myrepo"
	full := "org/myrepo"
	desc := "a repo"
	url := "https://github.com/org/myrepo"
	r := &github.Repository{Name: &name, FullName: &full, Description: &desc, HTMLURL: &url}
	got := MapRepo(r)
	assert.Equal(t, "myrepo", got.Name)
	assert.Equal(t, "org/myrepo", got.FullName)
	assert.Equal(t, "a repo", got.Description)
	assert.Equal(t, "https://github.com/org/myrepo", got.URL)
}

func TestMapRepo_NilFields(t *testing.T) {
	got := MapRepo(&github.Repository{})
	assert.Equal(t, "", got.Name)
	assert.Equal(t, "", got.FullName)
}

func TestMapOrg(t *testing.T) {
	login := "myorg"
	company := "My Company"
	desc := "org desc"
	url := "https://api.github.com/orgs/myorg"
	o := &github.Organization{Login: &login, Company: &company, Description: &desc, URL: &url}
	got := MapOrg(o)
	assert.Equal(t, "myorg", got.Name)
	assert.Equal(t, "My Company", got.Company)
	assert.Equal(t, "org desc", got.Description)
	assert.Equal(t, "https://api.github.com/orgs/myorg", got.URL)
}
