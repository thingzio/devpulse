// Copyright 2026 Thingz LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package ghutil

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"log/slog"

	"github.com/google/go-github/v83/github"
	"github.com/thingzio/devpulse/pkg/data"
)

var usernameRegEx = regexp.MustCompile(`@([A-Za-z0-9_]+)`)

func MapUserToDeveloper(u *github.User) *data.Developer {
	return &data.Developer{
		Username:   Trim(u.Login),
		FullName:   Trim(u.Name),
		Email:      Deref(u.Email),
		AvatarURL:  Deref(u.AvatarURL),
		ProfileURL: Deref(u.HTMLURL),
		Entity:     Trim(u.Company),
	}
}

func Deref(s *string) string {
	if s != nil {
		return strings.TrimSpace(*s)
	}
	return ""
}

func Trim(s *string) string {
	if s != nil {
		return strings.ReplaceAll(strings.TrimSpace(*s), "@", "")
	}
	return ""
}

func ParseDate(t *time.Time) string {
	if t != nil {
		return t.Format("2006-01-02")
	}
	return time.Now().UTC().Format("2006-01-02")
}

func RateInfo(r *github.Rate) string {
	if r == nil {
		return ""
	}
	return fmt.Sprintf("rate:%d/%d until:%s", r.Remaining, r.Limit, r.Reset.Format("15:04"))
}

func GetGitHubDeveloper(ctx context.Context, client *http.Client, username string) (*data.Developer, error) {
	if username == "" {
		return nil, errors.New("username is required")
	}

	usr, resp, err := github.NewClient(client).Users.Get(ctx, username)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %s: %w", username, err)
	}

	slog.Debug("got details for user", "username", username, "rate", RateInfo(&resp.Rate))

	return MapUserToDeveloper(usr), nil
}

func GetLabels(labels []*github.Label) []string {
	if labels == nil {
		return make([]string, 0)
	}

	r := make([]string, 0)
	for _, l := range labels {
		if l != nil {
			r = append(r, strings.ToLower(Trim(l.Name)))
		}
	}
	return r
}

func GetUsernames(users ...*github.User) []string {
	if users == nil {
		return make([]string, 0)
	}

	r := make([]string, 0)
	for _, u := range users {
		if u != nil {
			r = append(r, Trim(u.Login))
		}
	}
	return r
}

func ParseUsers(body *string) []string {
	if body == nil {
		return make([]string, 0)
	}
	return usernameRegEx.FindAllString(*body, -1)
}
