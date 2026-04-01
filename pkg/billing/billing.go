package billing

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/billingportal/session"
	checkoutsession "github.com/stripe/stripe-go/v82/checkout/session"
	"github.com/stripe/stripe-go/v82/customer"
)

// Config holds Stripe configuration from environment variables.
type Config struct {
	SecretKey      string
	WebhookSecret  string
	MonthlyPriceID string
	AnnualPriceID  string
	BaseURL        string
}

// LoadConfig reads Stripe config from environment variables.
// Returns nil if STRIPE_SECRET_KEY is not set (Stripe disabled).
func LoadConfig() *Config {
	key := os.Getenv("STRIPE_SECRET_KEY")
	if key == "" {
		return nil
	}
	stripe.Key = key
	return &Config{
		SecretKey:      key,
		WebhookSecret:  os.Getenv("STRIPE_WEBHOOK_SECRET"),
		MonthlyPriceID: os.Getenv("STRIPE_PRO_MONTHLY_PRICE_ID"),
		AnnualPriceID:  os.Getenv("STRIPE_PRO_ANNUAL_PRICE_ID"),
		BaseURL:        os.Getenv("BASE_URL"),
	}
}

// PriceIDForPeriod returns the Stripe Price ID for the given period.
func (c *Config) PriceIDForPeriod(period string) (string, error) {
	switch period {
	case "monthly":
		return c.MonthlyPriceID, nil
	case "annual":
		return c.AnnualPriceID, nil
	default:
		return "", fmt.Errorf("invalid period: %s", period)
	}
}

// CreateCustomer creates a Stripe customer for a tenant.
func CreateCustomer(ctx context.Context, email, tenantID string) (string, error) {
	params := &stripe.CustomerParams{
		Email: stripe.String(email),
	}
	params.Context = ctx
	params.AddMetadata("tenant_id", tenantID)

	c, err := customer.New(params)
	if err != nil {
		return "", fmt.Errorf("creating stripe customer: %w", err)
	}

	slog.Info("stripe customer created", "customer_id", c.ID, "tenant_id", tenantID)
	return c.ID, nil
}

// CreateCheckoutSession creates a Stripe Checkout Session for subscription.
func CreateCheckoutSession(ctx context.Context, cfg *Config, customerID, priceID, tenantID string) (string, error) {
	params := &stripe.CheckoutSessionParams{
		Customer: stripe.String(customerID),
		Mode:     stripe.String(string(stripe.CheckoutSessionModeSubscription)),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{
				Price:    stripe.String(priceID),
				Quantity: stripe.Int64(1),
			},
		},
		SuccessURL: stripe.String(cfg.BaseURL + "/pricing?status=success"),
		CancelURL:  stripe.String(cfg.BaseURL + "/pricing?status=cancel"),
	}
	params.Context = ctx
	params.AddMetadata("tenant_id", tenantID)

	s, err := checkoutsession.New(params)
	if err != nil {
		return "", fmt.Errorf("creating checkout session: %w", err)
	}

	slog.Info("checkout session created", "session_id", s.ID, "tenant_id", tenantID)
	return s.URL, nil
}

// CreatePortalSession creates a Stripe Customer Portal session.
func CreatePortalSession(ctx context.Context, cfg *Config, customerID string) (string, error) {
	params := &stripe.BillingPortalSessionParams{
		Customer:  stripe.String(customerID),
		ReturnURL: stripe.String(cfg.BaseURL + "/dashboard"),
	}
	params.Context = ctx

	s, err := session.New(params)
	if err != nil {
		return "", fmt.Errorf("creating portal session: %w", err)
	}
	return s.URL, nil
}

// SubscriptionPeriodEnd extracts the period end from a Stripe subscription's first item.
// In stripe-go v82, CurrentPeriodEnd moved from Subscription to SubscriptionItem.
func SubscriptionPeriodEnd(sub *stripe.Subscription) time.Time {
	if sub.Items != nil {
		for _, item := range sub.Items.Data {
			if item.CurrentPeriodEnd > 0 {
				return time.Unix(item.CurrentPeriodEnd, 0).UTC()
			}
		}
	}
	return time.Time{}
}
