package bot

import (
	"encoding/json"
	"strings"
)

func (s *session) module() string {
	if s.activeModule != "" {
		return s.activeModule
	}
	r := s.route
	if strings.HasPrefix(r, "verify") || r == "/verify" {
		return "feedback"
	}
	if strings.HasPrefix(r, "ac:") {
		return "administration"
	}
	if len(object(s.state["go_form"])) > 0 && (strings.HasPrefix(r, "form:") || s.typed && s.update.Message != nil && r == s.update.Message.Text) {
		switch text(object(s.state["go_form"])["kind"]) {
		case "channels":
			return "directory"
		case "market", "lost", "found":
			return "market"
		case "personal":
			return "personal"
		case "admin_reply":
			return "support"
		default:
			return "content"
		}
	}
	if len(object(s.state["support_chat"])) > 0 && s.typed && s.update.Message != nil && r == s.update.Message.Text {
		return "support"
	}
	if r == "u:personal" {
		return "navigation"
	}
	if strings.HasPrefix(r, "u:personal:") {
		return "personal"
	}
	if r == "fun:channels" || strings.HasPrefix(r, "sch:") {
		return "directory"
	}
	if r == "lostfound" || strings.HasPrefix(r, "lost:") {
		return "market"
	}
	if r == "menu" || strings.HasPrefix(r, "submenu:") {
		return "navigation"
	}
	if strings.HasPrefix(r, "u:professor:") || strings.HasPrefix(r, "u:people:") || strings.HasPrefix(r, "u:links") || len(object(s.state["professor_form"])) > 0 {
		return "directory"
	}
	if strings.HasPrefix(r, "ubooks:") || r == "u:library" || s.typed && s.state["book_search"] == true {
		return "library"
	}
	if strings.HasPrefix(r, "support") {
		return "support"
	}
	if r == "market" || strings.HasPrefix(r, "market:") {
		return "market"
	}
	if strings.HasPrefix(r, "fun:") {
		return "content"
	}
	return "content"
}
func (s *session) moduleBlocked() bool {
	if root := s.rootForRoute(); root != "" && !s.nodeEnabled(root) {
		return true
	}
	var flags map[string]bool
	json.Unmarshal(s.settings["modules"], &flags)
	if enabled, ok := flags[s.module()]; ok && !enabled {
		return true
	}
	var state string
	json.Unmarshal(s.settings["bot_state"], &state)
	if state == "maintenance" {
		var modules []string
		json.Unmarshal(s.settings["maintenance_modules"], &modules)
		for _, module := range modules {
			if module == s.module() {
				return true
			}
		}
	}
	return false
}
func (s *session) nodeEnabled(id string) bool {
	seen := map[string]bool{}
	for id != "" {
		if seen[id] {
			return false
		}
		seen[id] = true
		found := false
		next := ""
		for _, n := range s.menu.Buttons {
			if n.ID == id {
				found = true
				if !n.Enabled {
					return false
				}
				if n.Parent != nil {
					next = *n.Parent
				}
				break
			}
		}
		if !found {
			return true
		}
		id = next
	}
	return true
}
func (s *session) rootForRoute() string {
	r := s.route
	switch {
	case strings.HasPrefix(r, "u:professor") || strings.Contains(r, ":professors:"):
		return "professors"
	case strings.HasPrefix(r, "u:people") || strings.HasPrefix(r, "u:roles") || strings.HasPrefix(r, "u:persons"):
		return "contacts"
	case strings.HasPrefix(r, "ubooks:") || r == "u:library" || strings.HasPrefix(r, "u:items:"):
		return "resources"
	case strings.HasPrefix(r, "u:links") || strings.HasPrefix(r, "u:legacylinks"):
		return "links"
	case strings.HasPrefix(r, "notice:") || r == "u:news":
		return "announcements"
	case strings.HasPrefix(r, "support"):
		return "support"
	case r == "fun:channels" || strings.HasPrefix(r, "sch:"):
		return "studentchannels"
	case strings.HasPrefix(r, "fun:"):
		return "leisure"
	case strings.HasPrefix(r, "market"):
		return "market"
	case strings.HasPrefix(r, "lost"):
		return "lostfound"
	case strings.HasPrefix(r, "u:personal"):
		return "personal"
	case strings.HasPrefix(r, "ac:"):
		return "adminconsole"
	}
	return ""
}
