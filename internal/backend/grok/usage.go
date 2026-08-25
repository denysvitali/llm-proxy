package grok

import "time"

type Usage struct {
	Account Account
	Billing Billing
}

type UsageView struct {
	Available         bool      `json:"available"`
	Email             string    `json:"email,omitempty"`
	Name              string    `json:"name,omitempty"`
	SubscriptionTier  string    `json:"subscriptionTier,omitempty"`
	PercentUsed       float64   `json:"percentUsed"`
	HasPercent        bool      `json:"hasPercent"`
	LimitCents        *float64  `json:"limitCents,omitempty"`
	UsedCents         *float64  `json:"usedCents,omitempty"`
	RemainingCents    *float64  `json:"remainingCents,omitempty"`
	OnDemandUsedCents *float64  `json:"onDemandUsedCents,omitempty"`
	OnDemandCapCents  *float64  `json:"onDemandCapCents,omitempty"`
	PrepaidCents      *float64  `json:"prepaidCents,omitempty"`
	PeriodType        string    `json:"periodType,omitempty"`
	PeriodStart       string    `json:"periodStart,omitempty"`
	PeriodEnd         string    `json:"periodEnd,omitempty"`
	FetchedAt         time.Time `json:"fetchedAt"`
}

func NewUsageView(usage Usage, fetchedAt time.Time) UsageView {
	billing := usage.Billing
	view := UsageView{
		Available:        billing.Available,
		Email:            usage.Account.Email,
		Name:             usage.Account.Name(),
		SubscriptionTier: firstNonEmpty(billing.SubscriptionTier, usage.Account.SubscriptionTier),
		FetchedAt:        fetchedAt.UTC(),
	}
	if billing.CreditUsagePercent.Valid {
		view.PercentUsed = billing.CreditUsagePercent.Value
		view.HasPercent = true
	} else if billing.MonthlyLimit.Valid && billing.MonthlyLimit.Value > 0 && billing.Used.Valid {
		view.PercentUsed = billing.Used.Value / billing.MonthlyLimit.Value * 100
		view.HasPercent = true
	}
	if billing.MonthlyLimit.Valid {
		value := billing.MonthlyLimit.Value
		view.LimitCents = &value
	}
	if billing.Used.Valid {
		value := billing.Used.Value
		view.UsedCents = &value
	}
	if billing.MonthlyLimit.Valid && billing.Used.Valid {
		value := billing.MonthlyLimit.Value - billing.Used.Value
		view.RemainingCents = &value
	}
	if billing.OnDemandUsed.Valid {
		value := billing.OnDemandUsed.Value
		view.OnDemandUsedCents = &value
	}
	if billing.OnDemandCap.Valid {
		value := billing.OnDemandCap.Value
		view.OnDemandCapCents = &value
	}
	if billing.PrepaidBalance.Valid {
		value := billing.PrepaidBalance.Value
		view.PrepaidCents = &value
	}
	view.PeriodType = billing.CurrentPeriod.Type
	view.PeriodStart = billing.CurrentPeriod.Start
	view.PeriodEnd = billing.CurrentPeriod.End
	return view
}
