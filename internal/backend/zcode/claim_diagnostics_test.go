package zcode

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
)

func TestPreviewDiagnosticsAreSanitized(t *testing.T) {
	logger := logrus.StandardLogger()
	oldOutput, oldFormatter, oldLevel := logger.Out, logger.Formatter, logger.GetLevel()
	defer func() {
		logger.SetOutput(oldOutput)
		logger.SetFormatter(oldFormatter)
		logger.SetLevel(oldLevel)
	}()
	logger.SetFormatter(&logrus.JSONFormatter{})
	logger.SetLevel(logrus.InfoLevel)

	for _, tc := range []struct {
		name, body, outcome, data, plans       string
		status, code, count, accepted, invalid int
		wantErr                                bool
	}{
		{"empty", `{"code":0,"data":{"plans":[]}}`, "ok", "present", "present", 200, 0, 0, 0, 0, false},
		{"missing data", `{"code":0}`, "missing_data", "missing", "unknown", 200, 0, -1, 0, -1, true},
		{"null data", `{"code":0,"data":null}`, "missing_data", "null", "missing", 200, 0, -1, 0, -1, true},
		{"missing plans", `{"code":0,"data":{}}`, "ok", "present", "missing", 200, 0, 0, 0, 0, false},
		{"null plans", `{"code":0,"data":{"plans":null}}`, "ok", "present", "null", 200, 0, 0, 0, 0, false},
		{"malformed JSON", `sensitive-payload`, "decode_error", "unknown", "unknown", 200, -1, -1, 0, -1, true},
		{"malformed data", `{"code":0,"data":"sensitive-data"}`, "decode_error", "present", "unknown", 200, 0, -1, 0, -1, true},
		{"malformed plans", `{"code":0,"data":{"plans":"sensitive-plans"}}`, "decode_error", "present", "present", 200, 0, -1, 0, -1, true},
		{"rejected", `{"code":3001,"msg":"sensitive-message","data":null}`, "rejected", "null", "missing", 403, 3001, -1, 0, -1, true},
		{"invalid code", `{"code":"sensitive-code","data":{}}`, "rejected", "present", "missing", 200, -1, -1, 0, -1, true},
		{"dropped plans", `{"code":0,"data":{"plans":[{"plan_id":"sensitive-id","name":"sensitive-name","entitlements":[{"entitlement_id":"sensitive-entitlement","show_name":"sensitive-grant"}]},{"plan_id":" "},{"name":"sensitive-invalid"}]}}`, "ok", "present", "present", 200, 0, 3, 1, 2, false},
		{"all dropped", `{"code":0,"data":{"plans":[{"plan_id":""}]}}`, "ok", "present", "present", 200, 0, 1, 0, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			logger.SetOutput(&logs)
			m := autoClaimTestManager(t, func(w http.ResponseWriter, r *http.Request) {
				if got := r.URL.Query().Get("app_version"); got != "3.16.2" {
					t.Errorf("app_version = %q, want official client 3.16.2", got)
				}
				if r.Header.Get("X-ZCode-App-Version") != "3.16.2" || r.Header.Get("User-Agent") != "ZCode/3.16.2" {
					t.Error("request headers do not match current official version")
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			_, err := m.PreviewPlans(context.Background())
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tc.wantErr)
			}
			if strings.Count(logs.String(), "\n") != 1 {
				t.Fatalf("want one diagnostic, got %q", logs.String())
			}
			if strings.Contains(logs.String(), "sensitive") || strings.Contains(logs.String(), "account-a") || strings.Contains(logs.String(), "test-proof") {
				t.Fatalf("diagnostic leaked upstream/account data: %s", logs.String())
			}
			var fields map[string]any
			if err := json.Unmarshal(logs.Bytes(), &fields); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{
				"msg": "ZCode plan preview completed", "app_version": "3.16.2",
				"outcome": tc.outcome, "data_state": tc.data, "plans_state": tc.plans,
				"http_status": float64(tc.status), "envelope_code": float64(tc.code),
				"plan_count": float64(tc.count), "accepted_count": float64(tc.accepted), "invalid_id_count": float64(tc.invalid),
			}
			for key, value := range want {
				if fields[key] != value {
					t.Errorf("%s = %v, want %v", key, fields[key], value)
				}
			}
			if len(fields) != len(want)+2 { // logrus adds only level and time.
				t.Errorf("unexpected diagnostic fields: %v", fields)
			}
		})
	}
}
