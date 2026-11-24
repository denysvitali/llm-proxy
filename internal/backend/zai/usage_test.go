package zai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Captured Coding Plan responses: the original TOKENS_LIMIT shape and the
// 2026 CREDIT_LIMIT rename (counts, no reset on an idle 5-hour window).
const (
	tokensQuota = `{"code":200,"msg":"Operation successful","data":{"limits":[
	  {"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":17,"nextResetTime":1782724971179},
	  {"type":"TOKENS_LIMIT","unit":6,"number":1,"percentage":3,"nextResetTime":1783305486997},
	  {"type":"TIME_LIMIT","unit":5,"number":1,"usage":1000,"currentValue":40,"remaining":960,"percentage":4,"nextResetTime":1785292686976,
	   "usageDetails":[{"modelCode":"search-prime","usage":30},{"modelCode":"web-reader","usage":10}]},
	  {"type":"SOMETHING_NEW","unit":3,"number":1,"percentage":50}
	],"level":"pro"},"success":true}`
	creditQuota = `{"code":200,"msg":"Operation successful","data":{"limits":[
	  {"type":"CREDIT_LIMIT","unit":3,"number":5,"usage":2000,"currentValue":0,"remaining":2000,"percentage":0},
	  {"type":"CREDIT_LIMIT","unit":6,"number":1,"usage":10000,"currentValue":9855,"remaining":145,"percentage":98,"nextResetTime":1786685679998}
	],"level":"lite"},"success":true}`
	subscriptions = `{"code":200,"msg":"Operation successful","data":[
	  {"productName":"GLM Coding Lite","status":"EXPIRED"},{"productName":"GLM Coding Pro","status":"VALID"}],"success":true}`
)

func usageServer(t *testing.T, quota string, auth func(string) bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !auth(r.Header.Get("Authorization")) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api" + quotaPath:
			_, _ = w.Write([]byte(quota))
		case "/api" + subscriptionPath:
			_, _ = w.Write([]byte(subscriptions))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestUsageTokensLimitShape(t *testing.T) {
	srv := usageServer(t, tokensQuota, func(h string) bool { return h == "k" })
	usage, err := New(srv.URL+"/api", "k").Usage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if usage.Plan != "GLM Coding Pro" || usage.Level != "pro" || len(usage.Windows) != 3 {
		t.Fatalf("usage = %+v", usage)
	}
	five, week, tools := usage.Windows[0], usage.Windows[1], usage.Windows[2]
	if five.Kind != "prompts" || five.DurationSeconds != 5*3600 || five.UsedPercent != 17 || five.Used != nil ||
		five.ResetsAt == nil || !five.ResetsAt.Equal(time.UnixMilli(1782724971179)) {
		t.Errorf("5-hour = %+v", five)
	}
	if week.DurationSeconds != 7*86400 || week.UsedPercent != 3 {
		t.Errorf("weekly = %+v", week)
	}
	if tools.Kind != "tools" || tools.DurationSeconds != 30*86400 || *tools.Limit != 1000 || *tools.Used != 40 ||
		*tools.Remaining != 960 || len(tools.Details) != 2 || tools.Details[0] != (ToolUsage{Name: "search-prime", Usage: 30}) {
		t.Errorf("tools = %+v", tools)
	}
}

func TestUsageCreditLimitShapeAndBearerFallback(t *testing.T) {
	srv := usageServer(t, creditQuota, func(h string) bool { return h == "Bearer k" })
	usage, err := New(srv.URL+"/api", "k").Usage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if usage.Level != "lite" || len(usage.Windows) != 2 {
		t.Fatalf("usage = %+v", usage)
	}
	five, week := usage.Windows[0], usage.Windows[1]
	if five.Kind != "prompts" || five.ResetsAt != nil || five.UsedPercent != 0 || *five.Limit != 2000 {
		t.Errorf("5-hour = %+v", five)
	}
	if week.UsedPercent != 98.55 || *week.Remaining != 145 {
		t.Errorf("weekly = %+v (counts must refine the integer percentage)", week)
	}
}

func TestUsageErrors(t *testing.T) {
	srv := usageServer(t, tokensQuota, func(string) bool { return false })
	if _, err := New(srv.URL+"/api", "k").Usage(context.Background()); !errors.Is(err, ErrUsageUnauthorized) {
		t.Errorf("rejected key err = %v", err)
	}
	noPlan := usageServer(t, `{"code":1113,"msg":"No active coding plan","success":false}`, func(string) bool { return true })
	if _, err := New(noPlan.URL+"/api", "k").Usage(context.Background()); err == nil || err.Error() != "z.ai usage: No active coding plan" {
		t.Errorf("no-plan err = %v", err)
	}
	envelope401 := usageServer(t, `{"code":401,"msg":"token expired","success":false}`, func(string) bool { return true })
	if _, err := New(envelope401.URL+"/api", "k").Usage(context.Background()); !errors.Is(err, ErrUsageUnauthorized) {
		t.Errorf("envelope 401 err = %v", err)
	}
}
