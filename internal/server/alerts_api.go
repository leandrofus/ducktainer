package server

import (
	"context"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"

	"ducktainer/internal/config"
)

const maskedSecret = "••••"

// alertsStatus: what is firing now and what happened lately.
func (s *Server) alertsStatus(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"active": []Alert{}, "history": []alertEntry{}}
	if s.alerts != nil {
		out["active"] = s.alerts.activeList()
		out["history"] = s.alerts.history(100)
	}
	writeJSON(w, out)
}

// alertsConfig returns rules and channels; secrets are not sent to the browser.
func (s *Server) alertsConfig(w http.ResponseWriter, r *http.Request) {
	out := struct {
		Rules    []config.AlertRule    `json:"rules"`
		Channels []config.AlertChannel `json:"channels"`
	}{[]config.AlertRule{}, []config.AlertChannel{}}
	if al := s.Cfg.Snapshot().Alerts; al != nil {
		out.Rules = append(out.Rules, al.Rules...)
		for _, ch := range al.Channels {
			if ch.URL != "" {
				ch.URL = maskedSecret
			}
			if ch.Password != "" {
				ch.Password = maskedSecret
			}
			out.Channels = append(out.Channels, ch)
		}
	}
	writeJSON(w, out)
}

func (s *Server) saveAlertRule(w http.ResponseWriter, r *http.Request) {
	var rule config.AlertRule
	if err := readJSON(r, &rule); err != nil {
		writeErr(w, err)
		return
	}
	rule.Name = strings.TrimSpace(rule.Name)
	if rule.Name == "" {
		writeErr(w, badRequest("enter a name for the rule"))
		return
	}
	switch rule.Kind {
	case "host_down", "agent_offline":
		if rule.ForSeconds < 0 {
			rule.ForSeconds = 0
		}
		rule.Pattern, rule.Containers, rule.CooldownMinutes = "", nil, 0
	case "log_match":
		if strings.TrimSpace(rule.Pattern) == "" {
			writeErr(w, badRequest("enter the regular expression"))
			return
		}
		if _, err := regexp.Compile(rule.Pattern); err != nil {
			writeErr(w, badRequest("invalid regular expression: %v", err))
			return
		}
		if rule.CooldownMinutes <= 0 {
			rule.CooldownMinutes = 5
		}
		rule.ForSeconds = 0
	default:
		writeErr(w, badRequest("unknown rule type"))
		return
	}
	if rule.ID == "" {
		rule.ID = config.NewToken()[:8]
	}
	err := s.Cfg.Update(func(c *config.Config) error {
		if c.Alerts == nil {
			c.Alerts = &config.Alerts{}
		}
		for i, x := range c.Alerts.Rules {
			if x.ID == rule.ID {
				c.Alerts.Rules[i] = rule
				return nil
			}
		}
		c.Alerts.Rules = append(c.Alerts.Rules, rule)
		return nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, rule)
}

func (s *Server) deleteAlertRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.Cfg.Update(func(c *config.Config) error {
		if c.Alerts == nil {
			return nil
		}
		keep := c.Alerts.Rules[:0]
		for _, x := range c.Alerts.Rules {
			if x.ID != id {
				keep = append(keep, x)
			}
		}
		c.Alerts.Rules = keep
		return nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// resolveChannel validates a channel from the browser and puts back the
// secrets it left masked.
func (s *Server) resolveChannel(ch *config.AlertChannel) error {
	ch.Name = strings.TrimSpace(ch.Name)
	if ch.Name == "" {
		return badRequest("enter a name for the channel")
	}
	if ch.ID != "" {
		if al := s.Cfg.Snapshot().Alerts; al != nil {
			for _, prev := range al.Channels {
				if prev.ID == ch.ID {
					if ch.URL == maskedSecret {
						ch.URL = prev.URL
					}
					if ch.Password == maskedSecret {
						ch.Password = prev.Password
					}
				}
			}
		}
	}
	switch ch.Kind {
	case "webhook":
		u, err := url.Parse(strings.TrimSpace(ch.URL))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return badRequest("enter a valid http(s) URL")
		}
		ch.URL = u.String()
		ch.Host, ch.Username, ch.Password, ch.From, ch.To = "", "", "", "", nil
	case "email":
		ch.Host = strings.TrimSpace(ch.Host)
		if ch.Host == "" || len(ch.To) == 0 {
			return badRequest("enter the SMTP server and at least one recipient")
		}
		if _, err := mail.ParseAddress(ch.From); err != nil {
			return badRequest("enter a valid sender address")
		}
		switch ch.Security {
		case "starttls", "tls", "none":
		default:
			ch.Security = "starttls"
		}
		ch.URL = ""
	default:
		return badRequest("unknown channel type")
	}
	return nil
}

func (s *Server) saveAlertChannel(w http.ResponseWriter, r *http.Request) {
	var ch config.AlertChannel
	if err := readJSON(r, &ch); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.resolveChannel(&ch); err != nil {
		writeErr(w, err)
		return
	}
	if ch.ID == "" {
		ch.ID = config.NewToken()[:8]
	}
	err := s.Cfg.Update(func(c *config.Config) error {
		if c.Alerts == nil {
			c.Alerts = &config.Alerts{}
		}
		for i, x := range c.Alerts.Channels {
			if x.ID == ch.ID {
				c.Alerts.Channels[i] = ch
				return nil
			}
		}
		c.Alerts.Channels = append(c.Alerts.Channels, ch)
		return nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]string{"id": ch.ID})
}

func (s *Server) deleteAlertChannel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.Cfg.Update(func(c *config.Config) error {
		if c.Alerts == nil {
			return nil
		}
		keep := c.Alerts.Channels[:0]
		for _, x := range c.Alerts.Channels {
			if x.ID != id {
				keep = append(keep, x)
			}
		}
		c.Alerts.Channels = keep
		for i := range c.Alerts.Rules {
			rule := &c.Alerts.Rules[i]
			var ids []string
			for _, cid := range rule.Channels {
				if cid != id {
					ids = append(ids, cid)
				}
			}
			// a rule that only used this channel must not start going to all of them
			if len(rule.Channels) > 0 && len(ids) == 0 {
				rule.Enabled = false
			}
			rule.Channels = ids
		}
		return nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// testAlertChannel sends a test message through a channel as it is in the form.
func (s *Server) testAlertChannel(w http.ResponseWriter, r *http.Request) {
	var ch config.AlertChannel
	if err := readJSON(r, &ch); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.resolveChannel(&ch); err != nil {
		writeErr(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	e := alertEntry{Time: time.Now(), State: "test", Rule: "test", Kind: "test", Message: "This is a test alert from ducktainer"}
	if err := sendAlert(ctx, ch, e); err != nil {
		writeErr(w, &httpError{http.StatusBadRequest, err})
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}
