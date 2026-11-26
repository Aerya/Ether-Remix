package core

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// UserTargets is either every user ("all" in JSON) or a list of local user
// ids.
type UserTargets struct {
	All bool
	IDs []int64
}

// AllUsers targets everyone.
func AllUsers() UserTargets { return UserTargets{All: true} }

// Includes reports whether a user (nil = anonymous) is targeted.
func (u UserTargets) Includes(user *User) bool {
	if u.All {
		return true
	}
	if user == nil {
		return false
	}
	for _, id := range u.IDs {
		if id == user.ID {
			return true
		}
	}
	return false
}

func (u UserTargets) MarshalJSON() ([]byte, error) {
	if u.All {
		return []byte(`"all"`), nil
	}
	ids := u.IDs
	if ids == nil {
		ids = []int64{}
	}
	return json.Marshal(ids)
}

func (u *UserTargets) UnmarshalJSON(b []byte) error {
	*u = UserTargets{}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		if s == "all" || s == "" {
			u.All = true
			return nil
		}
		return fmt.Errorf(`users: want "all" or a list of user ids, got %q`, s)
	}
	if string(b) == "null" {
		u.All = true
		return nil
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return fmt.Errorf(`users: want "all" or a list of user ids`)
	}
	for _, r := range raw {
		var n int64
		if err := json.Unmarshal(r, &n); err == nil {
			u.IDs = append(u.IDs, n)
			continue
		}
		var str string
		if err := json.Unmarshal(r, &str); err == nil {
			if n, err := strconv.ParseInt(str, 10, 64); err == nil {
				u.IDs = append(u.IDs, n)
				continue
			}
		}
		return fmt.Errorf("users: %s is not a user id", string(r))
	}
	if u.IDs == nil {
		u.IDs = []int64{}
	}
	return nil
}
