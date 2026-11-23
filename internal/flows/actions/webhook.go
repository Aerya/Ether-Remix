package actions

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/egress"
	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/flows"
	"github.com/braedonsaunders/ether/internal/safefetch"
)

const (
	webhookMaxReqBytes  = 256 * 1024 // 256 KiB request body ceiling
	webhookMaxRespBytes = 4096       // 4 KiB captured response body ceiling
	webhookDefaultWait  = 15 * time.Second
)

// BuiltinHTTPClient constructs the default SSRF-safe outbound HTTP client used
// by http.webhook, layered over Ether's egress transport and safefetch guard.
func BuiltinHTTPClient() *http.Client {
	base := egress.Client(egress.CapDirect, webhookDefaultWait)
	client := (&safefetch.Guard{}).Client(base)
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return client
}

// NewHTTPWebhook constructs the built-in http.webhook action handler.
func NewHTTPWebhook(deps Deps) flows.ActionHandler {
	spec := flows.ActionSpec{
		Type:        "http.webhook",
		Owner:       events.OwnerBuiltin,
		Group:       "http",
		Label:       "HTTP webhook",
		Description: "Send a signed or authenticated outbound HTTP request with templated JSON body and headers through Ether's SSRF-safe egress guard.",
		Icon:        "webhook",
		Idempotent:  false,
		SideEffect:  true,
		Params: []flows.ParamSchema{
			{Key: "url", Type: events.FieldString, Label: "URL", Description: "Destination HTTP(S) URL.", Required: true, Template: true},
			{
				Key:     "method",
				Type:    events.FieldEnum,
				Label:   "HTTP method",
				Default: "POST",
				Enum: []events.EnumOption{
					{Value: "POST", Label: "POST"},
					{Value: "PUT", Label: "PUT"},
					{Value: "PATCH", Label: "PATCH"},
					{Value: "DELETE", Label: "DELETE"},
					{Value: "GET", Label: "GET"},
				},
			},
			{Key: "headers", Type: events.FieldObject, Label: "Headers", Description: "Optional request headers (key/value pairs with template support).", Template: true},
			{Key: "body", Type: events.FieldString, Label: "Body", Description: "Raw or templated request body (for example JSON).", Template: true},
			{Key: "bodyJson", Type: events.FieldObject, Label: "JSON body object", Description: "Structured object serialized as JSON when Body is empty.", Template: true},
			{Key: "contentType", Type: events.FieldString, Label: "Content-Type", Description: "Content-Type header sent with the body.", Default: "application/json", Template: true},
			{Key: "secret", Type: events.FieldString, Label: "Signing secret", Description: "Optional shared secret used to attach X-Ether-Signature (HMAC-SHA256).", Secret: true},
			{Key: "bearerToken", Type: events.FieldString, Label: "Bearer token", Description: "Optional token sent in Authorization: Bearer <token>.", Secret: true},
			{Key: "failOnNon2xx", Type: events.FieldBool, Label: "Fail on non-2xx", Description: "Treat HTTP responses outside 200–299 as step errors.", Default: true},
		},
		Outputs: []events.FieldSchema{
			{Name: "statusCode", Type: events.FieldInt, Label: "HTTP status code"},
			{Name: "ok", Type: events.FieldBool, Label: "2xx status"},
			{Name: "body", Type: events.FieldString, Label: "Response body (truncated to 4 KiB)"},
			{Name: "durationMs", Type: events.FieldDurationMS, Label: "Request duration", Unit: "ms"},
			{Name: "dryRun", Type: events.FieldBool, Label: "Dry run"},
		},
	}

	return flows.ActionFunc{ActionSpec: spec, Run: func(ctx context.Context, actx flows.ActionContext, params map[string]any) (flows.ActionResult, error) {
		rawURL := strings.TrimSpace(paramString(params, "url", ""))
		if err := validateWebhookURL(rawURL); err != nil {
			return flows.ActionResult{}, fmt.Errorf("http.webhook: %w", err)
		}
		method := strings.ToUpper(strings.TrimSpace(paramString(params, "method", http.MethodPost)))
		switch method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodGet:
		default:
			return flows.ActionResult{}, fmt.Errorf("http.webhook: unsupported method %q", method)
		}

		var bodyBytes []byte
		if rawBody, present := params["body"]; present && rawBody != nil {
			if s, ok := rawBody.(string); ok {
				bodyBytes = []byte(s)
			} else {
				b, err := json.Marshal(rawBody)
				if err != nil {
					return flows.ActionResult{}, fmt.Errorf("http.webhook: encode body: %w", err)
				}
				bodyBytes = b
			}
		} else if obj := paramMap(params, "bodyJson"); obj != nil {
			b, err := json.Marshal(obj)
			if err != nil {
				return flows.ActionResult{}, fmt.Errorf("http.webhook: encode bodyJson: %w", err)
			}
			bodyBytes = b
		}
		if len(bodyBytes) > webhookMaxReqBytes {
			return flows.ActionResult{}, fmt.Errorf("http.webhook: request body exceeds %d byte limit", webhookMaxReqBytes)
		}

		contentType := strings.TrimSpace(paramString(params, "contentType", "application/json"))
		if strings.ContainsAny(contentType, "\r\n\x00") {
			return flows.ActionResult{}, errors.New("http.webhook: invalid contentType header")
		}
		failOnNon2xx, _, err := paramBool(params, "failOnNon2xx", true)
		if err != nil {
			return flows.ActionResult{}, fmt.Errorf("http.webhook: %w", err)
		}

		hdrs, err := sanitizeWebhookHeaders(paramMap(params, "headers"))
		if err != nil {
			return flows.ActionResult{}, fmt.Errorf("http.webhook: %w", err)
		}

		if actx.DryRun {
			return flows.ActionResult{
				Output: map[string]any{
					"dryRun":     true,
					"statusCode": 0,
					"ok":         true,
					"body":       "",
					"durationMs": int64(0),
					"method":     method,
					"url":        rawURL,
					"bodyBytes":  len(bodyBytes),
				},
			}, nil
		}

		reqCtx := ctx
		if len(deps.TrustedHosts) > 0 {
			reqCtx = safefetch.WithTrustedHosts(reqCtx, deps.TrustedHosts)
		}
		var bodyReader io.Reader
		if len(bodyBytes) > 0 && method != http.MethodGet {
			bodyReader = bytes.NewReader(bodyBytes)
		}
		req, err := http.NewRequestWithContext(reqCtx, method, rawURL, bodyReader)
		if err != nil {
			return flows.ActionResult{}, errors.New("http.webhook: could not construct request")
		}
		if len(bodyBytes) > 0 && method != http.MethodGet && contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		if actx.FlowID != "" {
			req.Header.Set("X-Ether-Flow-Id", actx.FlowID)
		}
		if actx.RunID != "" {
			req.Header.Set("X-Ether-Run-Id", actx.RunID)
		}
		if actx.IdempotencyKey != "" {
			req.Header.Set("X-Ether-Idempotency-Key", actx.IdempotencyKey)
		}
		for k, v := range hdrs {
			req.Header.Set(k, v)
		}
		if tok := strings.TrimSpace(paramString(params, "bearerToken", "")); tok != "" {
			if strings.ContainsAny(tok, "\r\n\x00") {
				return flows.ActionResult{}, errors.New("http.webhook: invalid bearerToken")
			}
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		if secret := paramString(params, "secret", ""); secret != "" {
			mac := hmac.New(sha256.New, []byte(secret))
			mac.Write(bodyBytes)
			req.Header.Set("X-Ether-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		}

		client := deps.HTTPClient
		if client == nil {
			client = BuiltinHTTPClient()
		}

		start := deps.now()
		resp, err := client.Do(req)
		elapsed := deps.now().Sub(start)
		if elapsed < 0 {
			elapsed = 0
		}
		if err != nil {
			// Avoid leaking destination credentials/query tokens while still
			// preserving SSRF blocks clearly.
			if errors.Is(err, safefetch.ErrBlocked) {
				return flows.ActionResult{}, fmt.Errorf("http.webhook: destination blocked by SSRF guard: %w", safefetch.ErrBlocked)
			}
			return flows.ActionResult{}, errors.New("http.webhook: request failed")
		}
		defer resp.Body.Close()

		captured, _ := io.ReadAll(io.LimitReader(resp.Body, webhookMaxRespBytes))
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))

		ok := resp.StatusCode >= 200 && resp.StatusCode < 300
		res := flows.ActionResult{
			Output: map[string]any{
				"dryRun":     false,
				"statusCode": resp.StatusCode,
				"ok":         ok,
				"body":       string(captured),
				"durationMs": elapsed.Milliseconds(),
			},
		}
		if !ok && failOnNon2xx {
			return res, fmt.Errorf("http.webhook: endpoint returned HTTP %d", resp.StatusCode)
		}
		return res, nil
	}}
}

func validateWebhookURL(raw string) error {
	if raw == "" || strings.ContainsAny(raw, "\r\n\x00") {
		return errors.New("HTTP(S) url is required")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("url must be a valid http:// or https:// URL")
	}
	if u.User != nil {
		return errors.New("url cannot contain embedded username or password")
	}
	return nil
}

func sanitizeWebhookHeaders(raw map[string]any) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if len(raw) > 32 {
		return nil, errors.New("at most 32 custom headers are allowed")
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		key := strings.TrimSpace(k)
		if key == "" || strings.ContainsAny(key, ":\r\n\x00 ") {
			return nil, fmt.Errorf("invalid header name %q", k)
		}
		if strings.EqualFold(key, "host") || strings.EqualFold(key, "content-length") || strings.EqualFold(key, "transfer-encoding") {
			continue
		}
		val := fmt.Sprint(v)
		if strings.ContainsAny(val, "\r\n\x00") {
			return nil, fmt.Errorf("header %q contains forbidden control characters", key)
		}
		out[key] = val
	}
	return out, nil
}
