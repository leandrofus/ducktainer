package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"ducktainer/internal/config"
)

// alertText is the one-line summary used by chat webhooks and email subjects.
func alertText(e alertEntry) string {
	mark := map[string]string{"firing": "🔴", "resolved": "🟢", "test": "🔔"}[e.State]
	if mark == "" {
		mark = "🟠"
	}
	where := e.Endpoint
	if e.Container != "" {
		where += " / " + e.Container
	}
	text := fmt.Sprintf("%s [ducktainer] %s", mark, e.Rule)
	if where != "" && where != " / " {
		text += " — " + where
	}
	return text + ": " + e.Message
}

func sendAlert(ctx context.Context, ch config.AlertChannel, e alertEntry) error {
	switch ch.Kind {
	case "webhook":
		return sendWebhook(ctx, ch, e)
	case "email":
		return sendEmail(ctx, ch, e)
	}
	return fmt.Errorf("unknown channel type %q", ch.Kind)
}

func sendWebhook(ctx context.Context, ch config.AlertChannel, e alertEntry) error {
	text := alertText(e)
	body, _ := json.Marshal(map[string]any{
		"event":     e.State,
		"rule":      e.Rule,
		"kind":      e.Kind,
		"host":      e.Endpoint,
		"container": e.Container,
		"message":   e.Message,
		"time":      e.Time.Format(time.RFC3339),
		// the field chat services read: Slack/Mattermost "text", Discord "content"
		"text":    text,
		"content": text,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ch.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ducktainer")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("webhook answered %s %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	return nil
}

func sendEmail(ctx context.Context, ch config.AlertChannel, e alertEntry) error {
	port := ch.Port
	if port == 0 {
		port = map[string]int{"tls": 465, "none": 25}[ch.Security]
		if port == 0 {
			port = 587
		}
	}
	addr := net.JoinHostPort(ch.Host, strconv.Itoa(port))
	d := &net.Dialer{Timeout: 10 * time.Second}
	tlsConf := &tls.Config{ServerName: ch.Host, MinVersion: tls.VersionTLS12}

	var conn net.Conn
	var err error
	if ch.Security == "tls" {
		conn, err = (&tls.Dialer{NetDialer: d, Config: tlsConf}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return err
	}
	conn.SetDeadline(time.Now().Add(20 * time.Second))
	c, err := smtp.NewClient(conn, ch.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if ch.Security == "" || ch.Security == "starttls" {
		if err := c.StartTLS(tlsConf); err != nil {
			return fmt.Errorf("STARTTLS: %w", err)
		}
	}
	if ch.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", ch.Username, ch.Password, ch.Host)); err != nil {
			return err
		}
	}
	from, err := mail.ParseAddress(ch.From)
	if err != nil {
		return fmt.Errorf("sender: %w", err)
	}
	if err := c.Mail(from.Address); err != nil {
		return err
	}
	var to []string
	for _, r := range ch.To {
		a, err := mail.ParseAddress(strings.TrimSpace(r))
		if err != nil {
			return fmt.Errorf("recipient %q: %w", r, err)
		}
		if err := c.Rcpt(a.Address); err != nil {
			return err
		}
		to = append(to, a.String())
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	subject := strings.NewReplacer("\r", " ", "\n", " ").Replace(alertText(e))
	if len(subject) > 200 {
		subject = subject[:200]
	}
	body := fmt.Sprintf("%s\r\n\r\nrule: %s\r\ntype: %s\r\nhost: %s\r\n", e.Message, e.Rule, e.Kind, e.Endpoint)
	if e.Container != "" {
		body += "container: " + e.Container + "\r\n"
	}
	body += "time: " + e.Time.Format(time.RFC3339) + "\r\n"
	msg := "From: " + from.String() + "\r\n" +
		"To: " + strings.Join(to, ", ") + "\r\n" +
		"Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n" +
		"Date: " + e.Time.Format(time.RFC1123Z) + "\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n" + body
	if _, err := io.WriteString(w, msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}
