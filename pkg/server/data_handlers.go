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

package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/thingzio/devpulse/pkg/data"
)

func minDateAPIHandler(store data.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := storeFromRequest(r, store)
		p := parseInsightParams(r)
		minDate, err := s.GetMinEventDate(r.Context(), p.org, p.repo)
		if err != nil {
			slog.Error("failed to get min event date", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to get min date")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"min_date": minDate})
	}
}

func queryAPIHandler(store data.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := storeFromRequest(r, store)
		q := r.URL.Query().Get("q")
		v := r.URL.Query().Get("v")

		var items []*data.ListItem
		var err error

		const (
			scopeOrg  = "org"
			scopeRepo = "repo"
		)

		ctx := r.Context()
		switch v {
		case scopeOrg:
			items, err = s.GetOrgLike(ctx, q, queryResultLimitDefault)
		case scopeRepo:
			items, err = s.GetRepoLike(ctx, q, queryResultLimitDefault)
		case "entity":
			items, err = s.GetEntityLike(ctx, q, queryResultLimitDefault)
		case "all":
			half := queryResultLimitDefault / 2
			orgs, orgErr := s.GetOrgLike(ctx, q, half)
			if orgErr != nil {
				err = orgErr
				break
			}
			for _, o := range orgs {
				o.Type = scopeOrg
			}
			repos, repoErr := s.GetRepoLike(ctx, q, half)
			if repoErr != nil {
				err = repoErr
				break
			}
			for _, r := range repos {
				r.Type = scopeRepo
			}
			items = append(items, orgs...)
			items = append(items, repos...)
		default:
			items = []*data.ListItem{}
		}

		if err != nil {
			slog.Error("failed to get org like data", "error", err)
			writeError(w, http.StatusInternalServerError, "error querying org like data")
			return
		}

		writeJSON(w, http.StatusOK, items)
	}
}

func developerDataAPIHandler(store data.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := storeFromRequest(r, store)
		percentageAPIHandler(w, r, s.GetDeveloperPercentages)
	}
}

func entityDataAPIHandler(store data.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := storeFromRequest(r, store)
		percentageAPIHandler(w, r, s.GetEntityPercentages)
	}
}

func eventDataAPIHandler(store data.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := storeFromRequest(r, store)
		p := parseInsightParams(r)
		entity := r.URL.Query().Get("e")
		res, err := s.GetEventTypeSeries(r.Context(), p.org, p.repo, optional(entity), p.days)
		if err != nil {
			slog.Error("failed to get event type series", "error", err)
			writeError(w, http.StatusInternalServerError, "error querying event type series")
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

func eventSearchAPIHandler(store data.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := storeFromRequest(r, store)
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		var q data.EventSearchCriteria
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			slog.Error("error binding json", "error", err)
			writeError(w, http.StatusBadRequest, "error binding json")
			return
		}

		if org, repo, ok := parseRepo(q.Repo); ok {
			q.Org = org
			q.Repo = repo
		}

		if q.Type != nil {
			eType := *q.Type
			switch eType {
			case "PR":
				eType = data.EventTypePR
			case "PR-Review":
				eType = data.EventTypePRReview
			case "Issue":
				eType = data.EventTypeIssue
			case "Issue-Comment":
				eType = data.EventTypeIssueComment
			case "Fork":
				eType = data.EventTypeFork
			default:
				eType = ""
			}
			if eType != "" {
				q.Type = &eType
			}
		}

		slog.Debug("event search query", "query", q)

		res, err := s.SearchEvents(r.Context(), &q)
		if err != nil {
			slog.Error("failed to execute event search", "error", err)
			writeError(w, http.StatusInternalServerError, "error querying event type series")
			return
		}

		writeJSON(w, http.StatusOK, res)
	}
}

func entityDevelopersAPIHandler(store data.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := storeFromRequest(r, store)
		entity := r.URL.Query().Get("e")
		if entity == "" {
			writeError(w, http.StatusBadRequest, "entity parameter required")
			return
		}

		res, err := s.GetEntity(r.Context(), entity)
		if err != nil {
			slog.Error("failed to get entity developers", "error", err)
			writeError(w, http.StatusInternalServerError, "error querying entity developers")
			return
		}

		writeJSON(w, http.StatusOK, res)
	}
}

type percentageProvider func(ctx context.Context, entity, org, repo *string, ex []string, days int) ([]*data.CountedItem, error)

func percentageAPIHandler(w http.ResponseWriter, r *http.Request, fn percentageProvider) {
	p := parseInsightParams(r)
	entity := r.URL.Query().Get("e")
	var exclude []string
	if x := r.URL.Query().Get("x"); x != "" {
		exclude = strings.Split(x, arraySelector)
	}

	slog.Debug("event type query", "org", p.org, "repo", p.repo, "entity", entity, "days", p.days)

	res, err := fn(r.Context(), optional(entity), p.org, p.repo, exclude, p.days)
	if err != nil {
		slog.Error("failed to get event type series", "error", err)
		writeError(w, http.StatusInternalServerError, "error querying event type series")
		return
	}

	writeJSON(w, http.StatusOK, mapCountedItemsToSeries(res))
}
