package collections

import (
	"strconv"
	"strings"
	"time"
)

// Cadence libre de PSE : « 6h » reconstruit la collection toutes les six
// heures (de 1 à 168 h), en plus de live, hourly, daily et weekly. Voir
// docs/pse/FORK.fr.md.

// PSERefreshMaxHours borne la cadence libre (une semaine).
const PSERefreshMaxHours = 168

// pseRefreshEvery lit une cadence « Nh ».
func pseRefreshEvery(r string) (time.Duration, bool) {
	n, ok := strings.CutSuffix(strings.TrimSpace(r), "h")
	if !ok {
		return 0, false
	}
	h, err := strconv.Atoi(n)
	if err != nil || h < 1 || h > PSERefreshMaxHours || strconv.Itoa(h) != n {
		return 0, false
	}
	return time.Duration(h) * time.Hour, true
}
