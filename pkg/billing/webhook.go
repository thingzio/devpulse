package billing

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/webhook"

	"github.com/thingzio/devpulse/pkg/tenant"
)

const maxWebhookBodyBytes = 1 << 16 // 64KB

// Pro plan limits — must match devpulse-admin planLimits.
var proLimits = [2]int{25, 20000}

// WebhookHandler returns an http.HandlerFunc that processes Stripe webhook events.
func WebhookHandler(db *sql.DB, cfg *Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBodyBytes))
		if err != nil {
			slog.Error("reading webhook body", "error", err)
			http.Error(w, "error reading body", http.StatusBadRequest)
			return
		}

		event, err := webhook.ConstructEvent(body, r.Header.Get("Stripe-Signature"), cfg.WebhookSecret)
		if err != nil {
			slog.Warn("invalid webhook signature", "error", err)
			http.Error(w, "invalid signature", http.StatusBadRequest)
			return
		}

		ctx := r.Context()
		switch event.Type {
		case "checkout.session.completed":
			handleCheckoutCompleted(ctx, db, event)
		case "customer.subscription.updated":
			handleSubscriptionUpdated(ctx, db, event)
		case "customer.subscription.deleted":
			handleSubscriptionDeleted(ctx, db, event)
		case "invoice.payment_failed":
			handlePaymentFailed(event)
		default:
			slog.Debug("unhandled webhook event", "type", event.Type)
		}

		w.WriteHeader(http.StatusOK)
	}
}

func handleCheckoutCompleted(ctx context.Context, db *sql.DB, event stripe.Event) {
	var session stripe.CheckoutSession
	if err := json.Unmarshal(event.Data.Raw, &session); err != nil {
		slog.Error("unmarshaling checkout session", "error", err)
		return
	}

	if session.Customer == nil || session.Subscription == nil {
		slog.Warn("checkout session missing customer or subscription")
		return
	}

	customerID := session.Customer.ID
	subID := session.Subscription.ID
	periodEnd := SubscriptionPeriodEnd(session.Subscription)

	t, err := tenant.GetTenantByStripeCustomer(ctx, db, customerID)
	if err != nil {
		slog.Error("looking up tenant by stripe customer",
			"customer_id", customerID, "error", err)
		return
	}

	if err := tenant.UpdateSubscription(ctx, db, t.ID, subID, "pro",
		proLimits[0], proLimits[1], periodEnd); err != nil {
		slog.Error("updating subscription after checkout",
			"tenant_id", t.ID, "error", err)
		return
	}

	slog.Info("checkout completed",
		"tenant_id", t.ID, "subscription_id", subID,
		"period_end", periodEnd)
}

func handleSubscriptionUpdated(ctx context.Context, db *sql.DB, event stripe.Event) {
	var sub stripe.Subscription
	if err := json.Unmarshal(event.Data.Raw, &sub); err != nil {
		slog.Error("unmarshaling subscription", "error", err)
		return
	}

	if sub.Customer == nil {
		slog.Warn("subscription update missing customer")
		return
	}

	customerID := sub.Customer.ID
	periodEnd := SubscriptionPeriodEnd(&sub)

	t, err := tenant.GetTenantByStripeCustomer(ctx, db, customerID)
	if err != nil {
		slog.Error("looking up tenant for subscription update",
			"customer_id", customerID, "error", err)
		return
	}

	if sub.CancelAtPeriodEnd {
		if err := tenant.SetDowngradePending(ctx, db, t.ID, periodEnd); err != nil {
			slog.Error("setting downgrade pending",
				"tenant_id", t.ID, "error", err)
			return
		}
		slog.Info("subscription cancellation scheduled",
			"tenant_id", t.ID, "period_end", periodEnd)
		return
	}

	if err := tenant.UpdateSubscription(ctx, db, t.ID, sub.ID, "pro",
		proLimits[0], proLimits[1], periodEnd); err != nil {
		slog.Error("updating subscription",
			"tenant_id", t.ID, "error", err)
		return
	}

	slog.Info("subscription updated",
		"tenant_id", t.ID, "subscription_id", sub.ID,
		"period_end", periodEnd)
}

func handleSubscriptionDeleted(ctx context.Context, db *sql.DB, event stripe.Event) {
	var sub stripe.Subscription
	if err := json.Unmarshal(event.Data.Raw, &sub); err != nil {
		slog.Error("unmarshaling subscription deleted", "error", err)
		return
	}

	if sub.Customer == nil {
		slog.Warn("subscription deleted missing customer")
		return
	}

	customerID := sub.Customer.ID

	t, err := tenant.GetTenantByStripeCustomer(ctx, db, customerID)
	if err != nil {
		slog.Error("looking up tenant for subscription deletion",
			"customer_id", customerID, "error", err)
		return
	}

	repoCount, err := tenant.GetActiveRepoCount(ctx, db, t.ID)
	if err != nil {
		slog.Error("getting active repo count",
			"tenant_id", t.ID, "error", err)
		return
	}

	if repoCount <= 5 {
		if err := tenant.DowngradeToFree(ctx, db, t.ID); err != nil {
			slog.Error("downgrading to free",
				"tenant_id", t.ID, "error", err)
			return
		}
		slog.Info("tenant downgraded to free",
			"tenant_id", t.ID, "repo_count", repoCount)
		return
	}

	periodEnd := SubscriptionPeriodEnd(&sub)
	if err := tenant.SetDowngradePending(ctx, db, t.ID, periodEnd); err != nil {
		slog.Error("setting downgrade pending after deletion",
			"tenant_id", t.ID, "error", err)
		return
	}

	slog.Info("downgrade pending, repos exceed free limit",
		"tenant_id", t.ID, "repo_count", repoCount,
		"period_end", periodEnd)
}

func handlePaymentFailed(event stripe.Event) {
	var invoice stripe.Invoice
	if err := json.Unmarshal(event.Data.Raw, &invoice); err != nil {
		slog.Error("unmarshaling invoice", "error", err)
		return
	}

	customerID := ""
	if invoice.Customer != nil {
		customerID = invoice.Customer.ID
	}

	slog.Warn("invoice payment failed",
		"invoice_id", invoice.ID,
		"customer_id", customerID,
		"amount_due", fmt.Sprintf("%d", invoice.AmountDue))
}
