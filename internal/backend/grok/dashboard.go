package grok

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	accountPath = "/user?include=subscription"
	billingPath = "/billing?format=credits"
)

// DashboardClient accesses the xAI account and billing services used by Grok
// Build. It is separate from the inference endpoint because billing requires
// the signed-in user ID.
type DashboardClient struct {
	BaseURL string
	HTTP    *http.Client
}

func NewDashboardClient(baseURL string, client *http.Client) *DashboardClient {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &DashboardClient{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: client}
}

type Account struct {
	UserID           string
	Email            string
	FirstName        string
	LastName         string
	SubscriptionTier string
}

func (a Account) Name() string {
	return strings.TrimSpace(a.FirstName + " " + a.LastName)
}

type Number struct {
	Value float64
	Valid bool
}

type UsagePeriod struct {
	Type  string
	Start string
	End   string
}

type Billing struct {
	Available          bool
	CreditUsagePercent Number
	CurrentPeriod      UsagePeriod
	MonthlyLimit       Number
	Used               Number
	OnDemandCap        Number
	OnDemandUsed       Number
	PrepaidBalance     Number
	SubscriptionTier   string
}

func (c *DashboardClient) Account(ctx context.Context, accessToken string) (Account, error) {
	body, err := c.get(ctx, c.BaseURL+accountPath, accessToken, "")
	if err != nil {
		return Account{}, err
	}
	return decodeAccount(body)
}

func (c *DashboardClient) Billing(ctx context.Context, accessToken, userID string) (Billing, error) {
	if userID == "" {
		return Billing{}, errors.New("billing request requires a user ID")
	}
	body, err := c.get(ctx, c.BaseURL+billingPath, accessToken, userID)
	if err != nil {
		return Billing{}, err
	}
	return decodeBilling(body)
}

func (c *DashboardClient) get(ctx context.Context, endpoint, accessToken, userID string) ([]byte, error) {
	if accessToken == "" {
		return nil, errors.New("missing access token")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-XAI-Token-Auth", "xai-grok-cli")
	request.Header.Set("x-grok-client-version", ClientVersion)
	request.Header.Set("x-grok-client-mode", "interactive")
	if userID != "" {
		request.Header.Set("x-userid", userID)
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request account service: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("read account service response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("account service returned HTTP %d: %s", response.StatusCode, bodyPreview(body, 200))
	}
	if bytes.HasPrefix(bytes.TrimSpace(body), []byte("<")) {
		return nil, errors.New("account service returned HTML instead of JSON")
	}
	return body, nil
}

func AccountFromToken(accessToken string) (Account, error) {
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 || len(parts[1]) > 1<<20 {
		return Account{}, errors.New("access token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Account{}, fmt.Errorf("decode access token claims: %w", err)
	}
	claims, err := decodeJSONObject(payload)
	if err != nil {
		return Account{}, fmt.Errorf("decode access token claims: %w", err)
	}
	account := accountFromObject(claims)
	if account.UserID == "" && account.Email == "" && account.Name() == "" {
		return Account{}, errors.New("access token contains no account identity claims")
	}
	return account, nil
}

func decodeAccount(body []byte) (Account, error) {
	root, err := decodeJSONObject(body)
	if err != nil {
		return Account{}, fmt.Errorf("decode account data: %w", err)
	}
	return accountFromObject(unwrapJSON(root, "data", "user")), nil
}

func accountFromObject(data map[string]any) Account {
	subscription, _ := objectValue(data, "subscription")
	return Account{
		UserID:           stringValue(data, "userId", "user_id", "id"),
		Email:            stringValue(data, "email", "emailAddress", "email_address"),
		FirstName:        stringValue(data, "firstName", "first_name", "givenName", "given_name"),
		LastName:         stringValue(data, "lastName", "last_name", "familyName", "family_name"),
		SubscriptionTier: firstNonEmpty(stringValue(data, "subscriptionTier", "subscription_tier"), stringValue(subscription, "tier", "name", "displayName", "subscriptionTier", "subscription_tier")),
	}
}

func decodeBilling(body []byte) (Billing, error) {
	if bytes.HasPrefix(bytes.TrimSpace(body), []byte("<")) {
		return Billing{}, errors.New("billing service returned HTML instead of JSON")
	}
	root, err := decodeJSONObject(body)
	if err != nil {
		return Billing{}, fmt.Errorf("decode billing data: %w", err)
	}
	response := unwrapJSON(root, "data", "billing", "credits")
	configValue, hasConfig := lookup(response, "config")
	data, ok := configValue.(map[string]any)
	if hasConfig && !ok {
		return Billing{SubscriptionTier: stringValue(response, "subscriptionTier", "subscription_tier")}, nil
	}
	if !hasConfig {
		data = response
	}
	period, _ := objectValue(data, "currentPeriod", "current_period")
	return Billing{
		Available:          true,
		CreditUsagePercent: numberValue(data, "creditUsagePercent", "credit_usage_percent"),
		CurrentPeriod:      UsagePeriod{Type: stringValue(period, "type"), Start: stringValue(period, "start"), End: stringValue(period, "end")},
		MonthlyLimit:       numberValue(data, "monthlyLimit", "monthly_limit"),
		Used:               numberValue(data, "used"),
		OnDemandCap:        numberValue(data, "onDemandCap", "on_demand_cap"),
		OnDemandUsed:       numberValue(data, "onDemandUsed", "on_demand_used"),
		PrepaidBalance:     numberValue(data, "prepaidBalance", "prepaid_balance"),
		SubscriptionTier:   firstNonEmpty(stringValue(response, "subscriptionTier", "subscription_tier"), stringValue(data, "subscriptionTier", "subscription_tier")),
	}, nil
}

func decodeJSONObject(body []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("expected a JSON object")
	}
	return object, nil
}

func unwrapJSON(value map[string]any, keys ...string) map[string]any {
	for {
		advanced := false
		for _, key := range keys {
			if child, ok := objectValue(value, key); ok {
				value = child
				advanced = true
				break
			}
		}
		if !advanced {
			return value
		}
	}
}

func lookup(object map[string]any, names ...string) (any, bool) {
	for _, name := range names {
		if value, ok := object[name]; ok {
			return value, true
		}
	}
	return nil, false
}

func objectValue(object map[string]any, names ...string) (map[string]any, bool) {
	value, ok := lookup(object, names...)
	if !ok {
		return nil, false
	}
	result, ok := value.(map[string]any)
	return result, ok
}

func stringValue(object map[string]any, names ...string) string {
	value, ok := lookup(object, names...)
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

func numberValue(object map[string]any, names ...string) Number {
	value, ok := lookup(object, names...)
	if !ok || value == nil {
		return Number{}
	}
	var number float64
	var err error
	switch typed := value.(type) {
	case json.Number:
		number, err = typed.Float64()
	case float64:
		number = typed
	case string:
		number, err = strconv.ParseFloat(typed, 64)
	case map[string]any:
		return numberValue(typed, "val")
	}
	return Number{Value: number, Valid: err == nil}
}

func bodyPreview(body []byte, limit int) string {
	text := strings.Join(strings.Fields(string(body)), " ")
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return text[:limit] + "…"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
