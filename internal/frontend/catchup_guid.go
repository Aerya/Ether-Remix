package frontend

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strings"
	"time"
)

const (
	catchUpShowPrefix    = "e7ca"
	catchUpSeasonPrefix  = "e7cb"
	catchUpEpisodePrefix = "e7cc"
)

func CatchUpGroupGUID(id string) string {
	if value, ok := strings.CutPrefix(id, "catchup-show-"); ok && len(value) == 32 {
		return catchUpShowPrefix + value[:28]
	}
	if value, ok := strings.CutPrefix(id, "catchup-season-"); ok && len(value) == 32 {
		return catchUpSeasonPrefix + value[:28]
	}
	return ""
}

func CatchUpGUID(item CatchUpItem) string {
	if item.Type != TypeEpisode {
		return CatchUpGroupGUID(item.ID)
	}
	var b [14]byte
	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], uint64(item.Programme.Start.UnixMilli()))
	copy(b[8:], stamp[2:])
	binary.BigEndian.PutUint64(b[:8], CatchUpChannelKey(item.Programme.ChannelID))
	return catchUpEpisodePrefix + hex.EncodeToString(b[:])
}

func CatchUpChannelKey(channel string) uint64 {
	h := sha256.Sum256([]byte(channel))
	return binary.BigEndian.Uint64(h[:8])
}

func parseCatchUpGUID(id string) (string, MediaType, []byte) {
	if len(id) == 36 && id[8] == '-' && id[13] == '-' && id[18] == '-' && id[23] == '-' {
		id = strings.ReplaceAll(id, "-", "")
	}
	if len(id) != 32 {
		return "", "", nil
	}
	id = strings.ToLower(id)
	kind := map[string]MediaType{catchUpShowPrefix: TypeShow, catchUpSeasonPrefix: TypeSeason, catchUpEpisodePrefix: TypeEpisode}[id[:4]]
	if kind == "" {
		return "", "", nil
	}
	b, err := hex.DecodeString(id[4:])
	if err != nil {
		return "", "", nil
	}
	return id, kind, b
}

func IsCatchUpGUID(id string) bool {
	_, kind, _ := parseCatchUpGUID(id)
	return kind != ""
}

func CatchUpGUIDGroup(id string) (string, MediaType) {
	canonical, kind, _ := parseCatchUpGUID(id)
	if kind == TypeShow || kind == TypeSeason {
		return canonical, kind
	}
	return "", ""
}

func CatchUpGUIDAiring(id string) (uint64, time.Time, bool) {
	_, kind, raw := parseCatchUpGUID(id)
	if kind != TypeEpisode {
		return 0, time.Time{}, false
	}
	var stamp [8]byte
	copy(stamp[2:], raw[8:])
	return binary.BigEndian.Uint64(raw[:8]), time.UnixMilli(int64(binary.BigEndian.Uint64(stamp[:]))), true
}
