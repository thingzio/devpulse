package server

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/thingzio/devpulse/pkg/billing"
	"github.com/thingzio/devpulse/pkg/middleware"
	"github.com/thingzio/devpulse/pkg/tenant"
)

type checkoutRequest struct {
	Period string `json:"period"`
}

func checkoutHandler(db *sql.DB, cfg *billing.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		if tn.Plan == "pro" {
			http.Error(w, "already on pro plan", http.StatusBadRequest)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var req checkoutRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		priceID, err := cfg.PriceIDForPeriod(req.Period)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Create or reuse Stripe customer
		customerID := ""
		if tn.StripeCustomerID != nil {
			customerID = *tn.StripeCustomerID
		} else {
			customerID, err = billing.CreateCustomer(r.Context(), tn.Email, tn.ID)
			if err != nil {
				slog.Error("creating stripe customer", "error", err)
				http.Error(w, "error creating customer", http.StatusInternalServerError)
				return
			}
			if saveErr := tenant.UpdateStripeCustomer(r.Context(), db, tn.ID, customerID); saveErr != nil {
				slog.Error("saving stripe customer id", "error", saveErr)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
		}

		url, err := billing.CreateCheckoutSession(r.Context(), cfg, customerID, priceID, tn.ID)
		if err != nil {
			slog.Error("creating checkout session", "error", err)
			http.Error(w, "error creating checkout session", http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"url": url})
	}
}

func billingPortalHandler(cfg *billing.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		if tn.StripeCustomerID == nil {
			http.Error(w, "no billing account", http.StatusBadRequest)
			return
		}

		url, err := billing.CreatePortalSession(r.Context(), cfg, *tn.StripeCustomerID)
		if err != nil {
			slog.Error("creating portal session", "error", err)
			http.Error(w, "error creating portal session", http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, url, http.StatusFound)
	}
}
