package identity

var Permissions = []string{"users", "content", "publish", "taxonomy", "files", "campaigns", "tickets", "market", "settings", "bot", "queue", "logs", "health", "backups", "reports", "admins"}

type Actor struct {
	ID          int64    `json:"id,string"`
	Username    string   `json:"username"`
	Name        string   `json:"name"`
	Role        string   `json:"role"`
	Level       int      `json:"level"`
	Super       bool     `json:"super"`
	Permissions []string `json:"permissions"`
	Version     int      `json:"-"`
	CSRF        string   `json:"csrf"`
}

func (a Actor) Can(permission string) bool {
	for _, p := range Permissions {
		if p == permission {
			if a.Super {
				return true
			}
			for _, held := range a.Permissions {
				if held == p {
					return true
				}
			}
		}
	}
	return false
}
func (a Actor) CanDelegate(level int, perms []string) bool {
	if !a.Can("admins") || level < 1 || level > 99 || (!a.Super && level <= a.Level) {
		return false
	}
	for _, p := range perms {
		if !a.Can(p) {
			return false
		}
	}
	return true
}
