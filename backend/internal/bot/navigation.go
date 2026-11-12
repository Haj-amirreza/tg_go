package bot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Telegram accepts one markup per message. Change the persistent keyboard only when
// its visible labels/styles change; keep actions in this user's conversation state.
func (s *session) board(rows [][]Button) error {
	keyboard := [][]Button{}
	actions := map[string]any{}
	for _, row := range rows {
		out := []Button{}
		for _, button := range row {
			if button.Data != "" {
				actions[button.Text] = button.Data
			}
			out = append(out, Button{Text: button.Text, Style: button.Style})
		}
		keyboard = append(keyboard, out)
	}
	s.state["go_reply_routes"] = actions
	raw, _ := json.Marshal(keyboard)
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	if text(s.state["go_keyboard_hash"]) == digest {
		return nil
	}
	s.state["go_keyboard_hash"] = digest
	return s.menuReply("ابزارهای این بخش در دکمه‌های پایین گفتگو آماده است.", keyboard)
}
