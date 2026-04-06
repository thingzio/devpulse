package data

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventSearchCriteria_String(t *testing.T) {
	q := EventSearchCriteria{PageSize: 10, Page: 1}
	s := q.String()
	assert.Contains(t, s, "page_size")
}

func TestEventSearchCriteria_String_AllFields(t *testing.T) {
	org := "myorg"
	repo := "myrepo"
	user := "alice"
	typ := "pr"
	from := "2025-01-01"
	to := "2025-06-01"
	entity := "acme"
	mention := "bob"
	label := "bug"

	q := EventSearchCriteria{
		FromDate: &from,
		ToDate:   &to,
		Type:     &typ,
		Org:      &org,
		Repo:     &repo,
		Username: &user,
		Entity:   &entity,
		Mention:  &mention,
		Label:    &label,
		Page:     2,
		PageSize: 25,
	}
	s := q.String()
	require.NotEmpty(t, s)
	assert.Contains(t, s, "myorg")
	assert.Contains(t, s, "myrepo")
	assert.Contains(t, s, "alice")
	assert.Contains(t, s, "pr")
	assert.Contains(t, s, "2025-01-01")
	assert.Contains(t, s, "bug")
}

func TestEventSearchCriteria_String_Empty(t *testing.T) {
	q := EventSearchCriteria{}
	s := q.String()
	assert.NotEmpty(t, s) // still produces valid JSON with zero values
	assert.Contains(t, s, "{")
}
