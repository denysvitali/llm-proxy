package zcode

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

var errAutoClaimAttempted = errors.New("ZCode automatic claim already attempted today")
var errAutoClaimUnavailable = errors.New("ZCode daily free offer unavailable")

const autoClaimInterval = time.Hour

// RunAutoClaim checks at startup and hourly. Only the advertised daily free
// GLM-5.3-Flash offer is eligible; manual claims remain available for other plans.
func (m *Manager) RunAutoClaim(ctx context.Context, log *logrus.Logger) {
	m.runAutoClaim(ctx, log, autoClaimInterval, time.Now)
}

func (m *Manager) runAutoClaim(ctx context.Context, log *logrus.Logger, interval time.Duration, now func() time.Time) {
	if log == nil {
		log = logrus.StandardLogger()
	}
	if interval <= 0 {
		interval = autoClaimInterval
	}
	check := func() {
		if ctx.Err() != nil {
			return
		}
		attemptCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		err := m.autoClaim(attemptCtx, now().UTC())
		outcome := "failed"
		switch {
		case err == nil:
			outcome = "success"
		case errors.Is(err, errAutoClaimAttempted):
			outcome = "already_attempted"
		case errors.Is(err, errAutoClaimUnavailable):
			outcome = "unavailable"
		case ctx.Err() != nil:
			return
		}
		// Never log upstream errors, plan text, JWTs, CAPTCHA proofs or account IDs.
		if outcome == "failed" {
			log.WithField("outcome", outcome).Warn("ZCode automatic claim failed")
		} else {
			log.WithField("outcome", outcome).Info("ZCode automatic claim completed")
		}
	}
	check()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
}

func (m *Manager) autoClaim(ctx context.Context, now time.Time) error {
	now = now.UTC()
	if err := ctx.Err(); err != nil {
		return err
	}
	if now.Hour() < 11 || !m.HasSession() {
		return errAutoClaimUnavailable
	}
	token, err := m.AccessToken(ctx)
	if err != nil {
		return err
	}
	subject, err := autoClaimSubject(token)
	if err != nil {
		return err
	}
	path := autoClaimMarkerPath(m.Store.Path, subject, now)
	if _, err := os.Stat(path); err == nil {
		return errAutoClaimAttempted
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	plans, err := m.PreviewPlans(ctx)
	if err != nil {
		return err
	}
	var selected *PreviewPlan
	for i := range plans {
		if dailyFreeOffer(plans[i], now) {
			// An ambiguous preview is not permission to make multiple mutations.
			if selected != nil {
				return errAutoClaimUnavailable
			}
			selected = &plans[i]
		}
	}
	if selected == nil {
		return errAutoClaimUnavailable
	}
	outcome, err := m.claimPlan(ctx, selected.PlanID, token, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return createAutoClaimMarker(path)
	})
	if err != nil {
		return err
	}
	if !outcome.OK {
		return errors.New("ZCode automatic claim rejected")
	}
	return nil
}

// The announcement opens claims at 11:00 UTC independently of the later usage
// window. The official client's normalized preview has no usage timestamps.
// Require an explicit free offer and the exact daily model grant instead.
func dailyFreeOffer(plan PreviewPlan, now time.Time) bool {
	nameWords := strings.FieldsFunc(strings.ToLower(plan.Name), func(r rune) bool { return r < 'a' || r > 'z' })
	free := false
	for _, word := range nameWords {
		if word == "free" {
			free = true
		}
	}
	text := strings.ToLower(plan.Name + " " + plan.Description)
	if now.UTC().Hour() < 11 || strings.TrimSpace(plan.PlanID) == "" || !free ||
		strings.Contains(text, "paid") || strings.Contains(text, "not free") {
		return false
	}
	// Never combine the model from one entitlement with another's grant.
	// Flattened manual-preview fields are deliberately not used here.
	for _, grant := range plan.EntitlementDetails {
		model := dailyFreeModel(grant.EntitlementID) || dailyFreeModel(grant.ShowName)
		if model && grant.GrantUnits == 300_000_000 &&
			(strings.EqualFold(grant.UnitType, "tokens") || strings.EqualFold(grant.UnitType, "token") || strings.EqualFold(grant.UnitType, "units")) &&
			(strings.EqualFold(grant.Period, "daily") || strings.EqualFold(grant.Period, "day")) {
			return true
		}
	}
	return false
}

// Normalize only model-name separators, not arbitrary surrounding display text.
// In particular, retain the version dot and reject suffixes for other variants.
func dailyFreeModel(name string) bool {
	name = strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(name)))
	return name == "glm5.3flash"
}

// Only a nonempty string JWT subject is currently supported as account identity.
// Never substitute the rotating raw token or a device-only identifier.
func autoClaimSubject(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errors.New("missing ZCode account identity")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", errors.New("missing ZCode account identity")
	}
	var claims struct {
		Subject string `json:"sub"`
	}
	if json.Unmarshal(payload, &claims) != nil || strings.TrimSpace(claims.Subject) == "" {
		return "", errors.New("missing ZCode account identity")
	}
	return claims.Subject, nil
}

func autoClaimMarkerPath(storePath, subject string, now time.Time) string {
	hash := sha256.Sum256([]byte(subject))
	return filepath.Join(storePath+".autoclaim", now.UTC().Format("2006-01-02")+"-"+hex.EncodeToString(hash[:]))
}

func createAutoClaimMarker(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return err
	}
	if err := syncAutoClaimDir(filepath.Dir(dir)); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return errAutoClaimAttempted
	}
	if err != nil {
		return err
	}
	// Retain even on write, sync, POST or response errors: outcome may be unknown.
	_, writeErr := file.WriteString("attempted\n")
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	return syncAutoClaimDir(dir)
}

func syncAutoClaimDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}
