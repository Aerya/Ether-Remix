package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// pushoverEndpoint is the Pushover Messages API URL; overridable in tests.
var pushoverEndpoint = "https://api.pushover.net/1/messages.json"

// post sends one notification over HTTP. Errors never echo the endpoint,
// token or response body: any of them may hold a credential.
func post(ctx context.Context, s *Service, endpoint string, body []byte, contentType string, headers map[string]string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("invalid notification request")
	}
	req.Header.Set("Content-Type", contentType)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := s.opts.Client.Do(req)
	if err != nil {
		return errors.New("notification transport failed")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 65536))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("notification endpoint returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func postJSON(ctx context.Context, s *Service, endpoint string, payload any, headers map[string]string) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return errors.New("encode notification failed")
	}
	return post(ctx, s, endpoint, body, "application/json", headers)
}

func bearerHeaders(c Channel) map[string]string {
	if tok := c.Secret("token"); tok != "" {
		return map[string]string{"Authorization": "Bearer " + tok}
	}
	return nil
}

func deliverWebhook(ctx context.Context, s *Service, c Channel, e Event) error {
	return postJSON(ctx, s, c.Secret("endpoint"), e, bearerHeaders(c))
}

func hasRichEmbed(e Event) bool {
	return strings.TrimSpace(e.PosterURL) != "" ||
		strings.TrimSpace(e.Color) != "" ||
		len(e.Fields) > 0 ||
		strings.TrimSpace(e.LinkURL) != "" ||
		strings.TrimSpace(e.Footer) != ""
}

func safeHTTPURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\r\n\x00") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return raw
}

func parseEmbedColor(raw string) (int, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, false
	}
	if strings.HasPrefix(s, "#") || strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		s = strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(s, "#"), "0x"), "0X")
		if len(s) != 6 {
			return 0, false
		}
		v, err := strconv.ParseInt(s, 16, 32)
		if err != nil || v < 0 || v > 0xFFFFFF {
			return 0, false
		}
		return int(v), true
	}
	if v, err := strconv.Atoi(s); err == nil && v >= 0 && v <= 0xFFFFFF {
		return v, true
	}
	// Try 6-digit bare hex as fallback.
	if len(s) == 6 {
		if v, err := strconv.ParseInt(s, 16, 32); err == nil && v >= 0 && v <= 0xFFFFFF {
			return int(v), true
		}
	}
	return 0, false
}

func cleanHeaderValue(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == 0 {
			return -1
		}
		return r
	}, s)
}

func deliverDiscord(ctx context.Context, s *Service, c Channel, e Event) error {
	if !hasRichEmbed(e) {
		return postJSON(ctx, s, c.Secret("endpoint"),
			map[string]any{"content": clip(e.Title+"\n"+e.Message, 1900), "allowed_mentions": map[string]any{"parse": []string{}}},
			bearerHeaders(c))
	}
	embed := map[string]any{
		"title":       clip(e.Title, 256),
		"description": clip(e.Message, 4096),
	}
	if col, ok := parseEmbedColor(e.Color); ok {
		embed["color"] = col
	}
	if u := safeHTTPURL(e.LinkURL); u != "" {
		embed["url"] = u
	}
	if thumb := safeHTTPURL(e.PosterURL); thumb != "" {
		embed["thumbnail"] = map[string]string{"url": thumb}
	}
	if len(e.Fields) > 0 {
		fields := make([]map[string]any, 0, min(len(e.Fields), 25))
		for _, f := range e.Fields {
			if len(fields) >= 25 {
				break
			}
			name := strings.TrimSpace(f.Name)
			val := strings.TrimSpace(f.Value)
			if name == "" || val == "" {
				continue
			}
			fields = append(fields, map[string]any{
				"name":   clip(name, 256),
				"value":  clip(val, 1024),
				"inline": f.Inline,
			})
		}
		if len(fields) > 0 {
			embed["fields"] = fields
		}
	}
	if footer := strings.TrimSpace(e.Footer); footer != "" {
		embed["footer"] = map[string]string{"text": clip(footer, 2048)}
	}
	if !e.At.IsZero() {
		embed["timestamp"] = e.At.UTC().Format(time.RFC3339)
	}
	return postJSON(ctx, s, c.Secret("endpoint"),
		map[string]any{
			"embeds":           []map[string]any{embed},
			"allowed_mentions": map[string]any{"parse": []string{}},
		},
		bearerHeaders(c))
}

func deliverSlack(ctx context.Context, s *Service, c Channel, e Event) error {
	if !hasRichEmbed(e) {
		return postJSON(ctx, s, c.Secret("endpoint"),
			map[string]string{"text": clip(e.Title+"\n"+e.Message, 3900)}, bearerHeaders(c))
	}
	att := map[string]any{
		"title": clip(e.Title, 256),
		"text":  clip(e.Message, 3500),
	}
	if col, ok := parseEmbedColor(e.Color); ok {
		att["color"] = fmt.Sprintf("#%06x", col)
	}
	if u := safeHTTPURL(e.LinkURL); u != "" {
		att["title_link"] = u
	}
	if thumb := safeHTTPURL(e.PosterURL); thumb != "" {
		att["thumb_url"] = thumb
	}
	if len(e.Fields) > 0 {
		fields := make([]map[string]any, 0, min(len(e.Fields), 25))
		for _, f := range e.Fields {
			if len(fields) >= 25 {
				break
			}
			name := strings.TrimSpace(f.Name)
			val := strings.TrimSpace(f.Value)
			if name == "" || val == "" {
				continue
			}
			fields = append(fields, map[string]any{
				"title": clip(name, 256),
				"value": clip(val, 1024),
				"short": f.Inline,
			})
		}
		if len(fields) > 0 {
			att["fields"] = fields
		}
	}
	if footer := strings.TrimSpace(e.Footer); footer != "" {
		att["footer"] = clip(footer, 512)
	}
	if !e.At.IsZero() {
		att["ts"] = e.At.Unix()
	}
	return postJSON(ctx, s, c.Secret("endpoint"),
		map[string]any{
			"text":        clip(e.Title+"\n"+e.Message, 3900),
			"attachments": []map[string]any{att},
		}, bearerHeaders(c))
}

func deliverTelegram(ctx context.Context, s *Service, c Channel, e Event) error {
	endpoint := "https://api.telegram.org/bot" + c.Secret("token") + "/sendMessage"
	text := e.Title + "\n" + e.Message
	if u := safeHTTPURL(e.LinkURL); u != "" {
		text += "\n" + u
	}
	return postJSON(ctx, s, endpoint,
		map[string]string{"chat_id": c.Setting("target"), "text": clip(text, 4000)}, nil)
}

func deliverGotify(ctx context.Context, s *Service, c Channel, e Event) error {
	var headers map[string]string
	if tok := c.Secret("token"); tok != "" {
		headers = map[string]string{"X-Gotify-Key": tok}
	}
	prio := 5
	if e.Priority != 0 {
		prio = e.Priority
		if prio < 0 {
			prio = 0
		}
		if prio > 10 {
			prio = 10
		}
	}
	payload := map[string]any{"title": e.Title, "message": e.Message, "priority": prio}
	if u := safeHTTPURL(e.LinkURL); u != "" {
		payload["extras"] = map[string]any{
			"client::notification": map[string]any{
				"click": map[string]string{"url": u},
			},
		}
	}
	return postJSON(ctx, s, c.Secret("endpoint"), payload, headers)
}

func deliverNtfy(ctx context.Context, s *Service, c Channel, e Event) error {
	headers := bearerHeaders(c)
	if headers == nil {
		headers = map[string]string{}
	}
	headers["Title"] = cleanHeaderValue(e.Title)
	if e.Priority != 0 {
		prio := e.Priority
		if prio < 1 {
			prio = 1
		}
		if prio > 5 {
			prio = 5
		}
		headers["Priority"] = strconv.Itoa(prio)
	}
	if u := safeHTTPURL(e.LinkURL); u != "" {
		headers["Click"] = u
	}
	if thumb := safeHTTPURL(e.PosterURL); thumb != "" {
		headers["Attach"] = thumb
	}
	return post(ctx, s, c.Secret("endpoint"), []byte(e.Message), "text/plain; charset=utf-8", headers)
}

func deliverPushover(ctx context.Context, s *Service, c Channel, e Event) error {
	payload := map[string]any{
		"token":   c.Secret("token"),
		"user":    c.Secret("user"),
		"title":   clip(e.Title, 250),
		"message": clip(e.Message, 1024),
	}
	if dev := strings.TrimSpace(c.Setting("device")); dev != "" {
		payload["device"] = dev
	}
	if snd := strings.TrimSpace(c.Setting("sound")); snd != "" {
		payload["sound"] = snd
	}
	if e.Priority != 0 {
		prio := e.Priority
		if prio < -2 {
			prio = -2
		}
		if prio > 2 {
			prio = 2
		}
		payload["priority"] = prio
		if prio == 2 {
			payload["retry"] = 60
			payload["expire"] = 3600
		}
	}
	if u := safeHTTPURL(e.LinkURL); u != "" {
		payload["url"] = u
	}
	if !e.At.IsZero() {
		payload["timestamp"] = e.At.Unix()
	}
	return postJSON(ctx, s, pushoverEndpoint, payload, nil)
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

func sendSMTP(ctx context.Context, c Channel, e Event) error {
	return sendSMTPWithTLS(ctx, c, e, nil)
}

func sendSMTPWithTLS(ctx context.Context, c Channel, e Event, trusted *tls.Config) error {
	endpoint := c.Secret("endpoint")
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return errors.New("invalid SMTP address")
	}
	tlsConfig := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	if trusted != nil {
		tlsConfig = trusted.Clone()
		tlsConfig.ServerName = host
	}
	dialer := net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	mode := c.Setting("smtpMode")
	implicitTLS := mode == "tls" || (mode == "" && port == "465")
	if implicitTLS {
		conn, err = (&tls.Dialer{NetDialer: &dialer, Config: tlsConfig}).DialContext(ctx, "tcp", endpoint)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", endpoint)
	}
	if err != nil {
		return errors.New("SMTP connection failed")
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline := time.Now().Add(20 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	cl, err := smtp.NewClient(conn, host)
	if err != nil {
		return errors.New("SMTP greeting failed")
	}
	defer cl.Close()
	if !implicitTLS {
		if err := cl.StartTLS(tlsConfig); err != nil {
			return errors.New("SMTP requires TLS")
		}
	}
	if username := c.Secret("username"); username != "" {
		if err := cl.Auth(smtp.PlainAuth("", username, c.Secret("password"), host)); err != nil {
			return errors.New("SMTP authentication failed")
		}
	}
	from, err := mail.ParseAddress(c.Setting("from"))
	if err != nil {
		return errors.New("invalid sender email address")
	}
	recipients, err := mail.ParseAddressList(c.Setting("to"))
	if err != nil || len(recipients) == 0 {
		return errors.New("invalid recipient email addresses")
	}
	if cl.Mail(from.Address) != nil {
		return errors.New("SMTP sender rejected")
	}
	for _, to := range recipients {
		if cl.Rcpt(to.Address) != nil {
			return errors.New("SMTP recipient rejected")
		}
	}

	w, err := cl.Data()
	if err != nil {
		return errors.New("SMTP DATA failed")
	}
	subject := mime.QEncoding.Encode("utf-8", strings.NewReplacer("\r", " ", "\n", " ").Replace(e.Title))
	contentType, body := "text/plain", e.Message
	if e.HTML != "" {
		contentType = "text/html"
		body = e.HTML
	}
	var encoded bytes.Buffer
	qp := quotedprintable.NewWriter(&encoded)
	_, _ = qp.Write([]byte(body))
	_ = qp.Close()
	_, err = fmt.Fprintf(w, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: %s; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n%s\r\n", from.String(), c.Setting("to"), subject, time.Now().Format(time.RFC1123Z), contentType, encoded.String())
	if err != nil {
		return errors.New("SMTP write failed")
	}

	if w.Close() != nil {
		return errors.New("SMTP message rejected")
	}
	_ = cl.Quit()
	return nil
}
