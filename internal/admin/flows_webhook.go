package admin

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/flows"
	"github.com/braedonsaunders/ether/internal/identity"
)

const (
	// MaxWebhookBodyBytes caps incoming flow webhook request bodies (256 KiB).
	MaxWebhookBodyBytes int64 = 256 << 10

	defaultWebhookRatePerIP    = 120
	defaultWebhookRatePerToken = 60
	webhookRateWindow          = time.Minute
	maxRateBuckets             = 4096

	webhookTokenPrefix = "flwh_"
	maxWebhookTokenLen = 256
)

// HashWebhookToken returns the SHA-256 hex digest of a plaintext incoming
// webhook token. Plaintext tokens are never persisted or echoed after creation.
func HashWebhookToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// ComputeWebhookHMAC returns the canonical "sha256=<hex>" HMAC-SHA256 signature
// of body keyed by secret.
func ComputeWebhookHMAC(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func generateWebhookToken() (plain, hash string, err error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", "", fmt.Errorf("flows: generate webhook token: %w", err)
	}
	plain = webhookTokenPrefix + hex.EncodeToString(buf[:])
	return plain, HashWebhookToken(plain), nil
}

// verifyWebhookHMAC checks headerVal against HMAC-SHA256(secret, body) in
// constant time. It accepts "sha256=<hex>", bare "<hex>", or "sha256=<base64>".
func verifyWebhookHMAC(secret string, body []byte, headerVal string) bool {
	if secret == "" {
		return false
	}
	raw := strings.TrimSpace(headerVal)
	if raw == "" {
		return false
	}
	hasPrefix := false
	if len(raw) >= 7 && strings.EqualFold(raw[:7], "sha256=") {
		raw = strings.TrimSpace(raw[7:])
		hasPrefix = true
	}
	if raw == "" {
		return false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	expected := mac.Sum(nil)

	if decoded, err := hex.DecodeString(raw); err == nil && len(decoded) == sha256.Size {
		return hmac.Equal(decoded, expected)
	}
	if hasPrefix {
		if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil && len(decoded) == sha256.Size {
			return hmac.Equal(decoded, expected)
		}
		if decoded, err := base64.RawStdEncoding.DecodeString(raw); err == nil && len(decoded) == sha256.Size {
			return hmac.Equal(decoded, expected)
		}
	}
	return false
}

// flowRateLimiter is a bounded fixed-window counter per key, modeled after
// authLimiter so hostile traffic cannot grow memory without bound.
type flowRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]flowRateBucket
	window  time.Duration
}

type flowRateBucket struct {
	count int
	until time.Time
}

func newFlowRateLimiter(window time.Duration) *flowRateLimiter {
	if window <= 0 {
		window = webhookRateWindow
	}
	return &flowRateLimiter{
		buckets: make(map[string]flowRateBucket),
		window:  window,
	}
}

func (l *flowRateLimiter) allow(key string, limit int, now time.Time) (ok bool, retryAfter int) {
	if limit <= 0 {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	for k, b := range l.buckets {
		if !now.Before(b.until) {
			delete(l.buckets, k)
		}
	}
	b, exists := l.buckets[key]
	if !exists {
		if len(l.buckets) >= maxRateBuckets {
			return false, int(l.window.Seconds())
		}
		b.until = now.Add(l.window)
	}
	b.count++
	l.buckets[key] = b
	if b.count > limit {
		secs := int(b.until.Sub(now).Seconds())
		if secs < 1 {
			secs = 1
		}
		return false, secs
	}
	return true, 0
}

// WebhookHandler serves incoming webhook triggers at /{token} (mounted at
// /api/flows/hooks/ outside console auth).
func (a *FlowsAPI) WebhookHandler() http.Handler {
	return http.HandlerFunc(a.handleIncomingWebhook)
}

func (a *FlowsAPI) handleIncomingWebhook(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.URL.Path, "/")
	if token == "" || strings.Contains(token, "/") || len(token) > maxWebhookTokenLen {
		writeError(w, http.StatusUnauthorized, "invalid_webhook", "invalid webhook token")
		return
	}

	now := a.now()
	clientIP := a.webhookClientIP(r)
	if ok, retry := a.ipLimiter.allow(clientIP, a.webhookRatePerIP, now); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many webhook requests from this client")
		return
	}

	// Enforce body cap before touching the store or parsing payload.
	var rawBody []byte
	if r.Body != nil {
		var err error
		rawBody, err = io.ReadAll(io.LimitReader(r.Body, a.webhookMaxBody+1))
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_body", "could not read webhook body")
			return
		}
		if int64(len(rawBody)) > a.webhookMaxBody {
			writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", fmt.Sprintf("webhook body exceeds %d byte limit", a.webhookMaxBody))
			return
		}
	}

	tokenHash := HashWebhookToken(token)
	tokenLimit := a.webhookRatePerToken
	secretHeaders := []string{}

	var matchedFlow *flows.Flow
	if a.deps.Store != nil {
		f, err := a.deps.Store.FindFlowByWebhookHash(r.Context(), tokenHash)
		if err != nil {
			if errors.Is(err, flows.ErrNotFound) {
				writeError(w, http.StatusUnauthorized, "invalid_webhook", "invalid webhook token")
				return
			}
			a.log.Error("flows: webhook token lookup failed", "err", err)
			writeError(w, http.StatusInternalServerError, "store_error", "could not verify webhook token")
			return
		}
		if f == nil {
			writeError(w, http.StatusUnauthorized, "invalid_webhook", "invalid webhook token")
			return
		}
		matchedFlow = f

		// An explicitly configured per-flow limit wins; the zero value
		// keeps the API-wide per-token default (Effective() would always
		// substitute the package default and mask it).
		if lim := f.Limits.RateLimitPerMin; lim > 0 {
			tokenLimit = lim
		}
		if ok, retry := a.tokenLimiter.allow(tokenHash, tokenLimit, now); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			writeError(w, http.StatusTooManyRequests, "rate_limited", "webhook rate limit exceeded for this flow")
			return
		}

		if !f.Enabled {
			writeError(w, http.StatusConflict, "flow_disabled", "flow is disabled")
			return
		}

		cfgs := activeWebhookTriggers(f.Graph)
		if len(cfgs) == 0 {
			writeError(w, http.StatusNotFound, "no_webhook_trigger", "flow has no active webhook trigger")
			return
		}

		allowed := unionAllowedWebhookMethods(cfgs)
		if !methodInList(r.Method, allowed) {
			w.Header().Set("Allow", strings.Join(allowed, ", "))
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "HTTP method is not permitted for this webhook trigger")
			return
		}

		hmacSecret := token
		if a.deps.WebhookHMACSecret != nil {
			if s, ok := a.deps.WebhookHMACSecret(r.Context(), f.ID); ok && s != "" {
				hmacSecret = s
			}
		}

		requireSig := false
		for _, cfg := range cfgs {
			if h := strings.TrimSpace(cfg.SecretHeader); h != "" {
				secretHeaders = append(secretHeaders, h)
				requireSig = true
			}
			if cfg.RequireAuth {
				requireSig = true
			}
		}

		if requireSig {
			sigVal := extractWebhookSignature(r.Header, secretHeaders)
			if sigVal == "" {
				writeError(w, http.StatusUnauthorized, "missing_signature", "webhook signature header is required")
				return
			}
			if !verifyWebhookHMAC(hmacSecret, rawBody, sigVal) {
				writeError(w, http.StatusUnauthorized, "invalid_signature", "webhook signature verification failed")
				return
			}
		} else if sigVal := extractOptionalWebhookSignature(r.Header); sigVal != "" {
			// Defense in depth: if caller supplies a signature header anyway,
			// verify it rather than silently ignoring a forged signature.
			if !verifyWebhookHMAC(hmacSecret, rawBody, sigVal) {
				writeError(w, http.StatusUnauthorized, "invalid_signature", "webhook signature verification failed")
				return
			}
		}
	} else {
		if ok, retry := a.tokenLimiter.allow(tokenHash, tokenLimit, now); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			writeError(w, http.StatusTooManyRequests, "rate_limited", "webhook rate limit exceeded")
			return
		}
		if !methodInList(r.Method, []string{http.MethodPost, http.MethodPut, http.MethodGet}) {
			w.Header().Set("Allow", "POST, PUT, GET")
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "HTTP method is not permitted for webhooks")
			return
		}
		if sigVal := extractOptionalWebhookSignature(r.Header); sigVal != "" {
			if !verifyWebhookHMAC(token, rawBody, sigVal) {
				writeError(w, http.StatusUnauthorized, "invalid_signature", "webhook signature verification failed")
				return
			}
		}
	}

	if a.deps.Runtime == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "flow runtime is not configured")
		return
	}

	payload, err := parseWebhookPayload(r, rawBody)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_payload", err.Error())
		return
	}
	headers := sanitizeWebhookHeaders(r, secretHeaders)

	run, err := a.deps.Runtime.TriggerWebhook(r.Context(), token, payload, headers)
	if err != nil {
		switch {
		case errors.Is(err, flows.ErrInvalidWebhook):
			writeError(w, http.StatusUnauthorized, "invalid_webhook", "invalid webhook token")
		case errors.Is(err, flows.ErrNotFound):
			writeError(w, http.StatusNotFound, "not_found", "flow not found")
		case errors.Is(err, flows.ErrFlowDisabled):
			writeError(w, http.StatusConflict, "flow_disabled", "flow is disabled")
		case errors.Is(err, flows.ErrRateLimited), errors.Is(err, flows.ErrLoopDepthLimit):
			w.Header().Set("Retry-After", "60")
			writeError(w, http.StatusTooManyRequests, "rate_limited", err.Error())
		default:
			a.log.Error("flows: webhook trigger failed", "err", err)
			writeError(w, http.StatusInternalServerError, "trigger_failed", "could not start flow run")
		}
		return
	}

	resp := map[string]any{"accepted": true}
	if run != nil {
		resp["runId"] = run.ID
		resp["flowId"] = run.FlowID
		resp["status"] = run.Status
		flowName := run.FlowName
		if flowName == "" && matchedFlow != nil {
			flowName = matchedFlow.Name
		}
		a.publishStream(r.Context(), StreamFlowRun, FlowRunEvent{
			RunID:      run.ID,
			FlowID:     run.FlowID,
			FlowName:   flowName,
			EventType:  run.EventType,
			Status:     run.Status,
			StartedAt:  run.StartedAt,
			UpdatedAt:  now,
			FinishedAt: run.FinishedAt,
		})
	}
	writeJSON(w, http.StatusAccepted, resp)
}

func (a *FlowsAPI) webhookClientIP(r *http.Request) string {
	var trusted []string
	if a.deps.TrustedProxyCIDRs != nil {
		trusted = a.deps.TrustedProxyCIDRs()
	}
	if addr, _ := identity.ClientNet(r, identity.ProxyPrefixes(trusted)...); addr.IsValid() {
		return addr.String()
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "unknown"
}

func activeWebhookTriggers(g flows.Graph) []flows.WebhookTriggerConfig {
	var out []flows.WebhookTriggerConfig
	for _, n := range g.Nodes {
		if n.Disabled || n.Kind != flows.NodeTriggerWebhook {
			continue
		}
		cfg, err := flows.DecodeConfig[flows.WebhookTriggerConfig](n)
		if err != nil {
			cfg = flows.WebhookTriggerConfig{}
		}
		out = append(out, cfg)
	}
	return out
}

func unionAllowedWebhookMethods(cfgs []flows.WebhookTriggerConfig) []string {
	seen := map[string]bool{}
	for _, cfg := range cfgs {
		methods := cfg.AllowedMethods
		if len(methods) == 0 {
			seen[http.MethodPost] = true
			continue
		}
		for _, m := range methods {
			m = strings.ToUpper(strings.TrimSpace(m))
			if m != "" {
				seen[m] = true
			}
		}
	}
	if len(seen) == 0 {
		return []string{http.MethodPost}
	}
	out := make([]string, 0, len(seen))
	for m := range seen {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

func methodInList(method string, allowed []string) bool {
	for _, m := range allowed {
		if strings.EqualFold(method, m) {
			return true
		}
	}
	return false
}

func extractWebhookSignature(h http.Header, configuredHeaders []string) string {
	for _, name := range configuredHeaders {
		if v := strings.TrimSpace(h.Get(name)); v != "" {
			return v
		}
	}
	return extractOptionalWebhookSignature(h)
}

func extractOptionalWebhookSignature(h http.Header) string {
	for _, name := range []string{"X-Ether-Signature", "X-Hub-Signature-256"} {
		if v := strings.TrimSpace(h.Get(name)); v != "" {
			return v
		}
	}
	return ""
}

func parseWebhookPayload(r *http.Request, rawBody []byte) (map[string]any, error) {
	payload := map[string]any{}
	trimmed := strings.TrimSpace(string(rawBody))
	mediaType := ""
	if ct := strings.TrimSpace(r.Header.Get("Content-Type")); ct != "" {
		if mt, _, err := mime.ParseMediaType(ct); err == nil {
			mediaType = strings.ToLower(mt)
		}
	}

	if len(trimmed) > 0 {
		switch {
		case mediaType == "application/json" || strings.HasSuffix(mediaType, "+json") ||
			(mediaType == "" && (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "["))):
			var decoded any
			if err := json.Unmarshal(rawBody, &decoded); err != nil {
				return nil, fmt.Errorf("invalid JSON webhook body")
			}
			if obj, ok := decoded.(map[string]any); ok {
				payload = obj
			} else {
				payload["body"] = decoded
			}
		case mediaType == "application/x-www-form-urlencoded":
			vals, err := url.ParseQuery(string(rawBody))
			if err != nil {
				return nil, fmt.Errorf("invalid form-encoded webhook body")
			}
			for k, vs := range vals {
				if len(vs) == 1 {
					payload[k] = vs[0]
				} else {
					payload[k] = vs
				}
			}
		default:
			payload["raw"] = string(rawBody)
			if mediaType != "" {
				payload["contentType"] = mediaType
			}
		}
	}

	if q := r.URL.Query(); len(q) > 0 {
		qm := make(map[string]any, len(q))
		for k, vs := range q {
			if len(vs) == 1 {
				qm[k] = vs[0]
			} else {
				qm[k] = vs
			}
		}
		if _, exists := payload["query"]; !exists {
			payload["query"] = qm
		}
	}
	return payload, nil
}

// sanitizeWebhookHeaders copies non-sensitive request headers into a lowercase
// map so secrets and auth credentials are never persisted into run traces.
func sanitizeWebhookHeaders(r *http.Request, secretHeaders []string) map[string]string {
	deny := map[string]bool{
		"authorization":       true,
		"proxy-authorization": true,
		"cookie":              true,
		"set-cookie":          true,
		"x-plex-token":        true,
		"x-ether-admin-token": true,
		"x-ether-signature":   true,
		"x-hub-signature-256": true,
	}
	for _, h := range secretHeaders {
		if k := strings.ToLower(strings.TrimSpace(h)); k != "" {
			deny[k] = true
		}
	}
	out := make(map[string]string, len(r.Header)+1)
	for k, vs := range r.Header {
		lk := strings.ToLower(strings.TrimSpace(k))
		if lk == "" || deny[lk] || len(vs) == 0 {
			continue
		}
		out[lk] = strings.Join(vs, ", ")
	}
	out["x-webhook-method"] = r.Method
	return out
}
