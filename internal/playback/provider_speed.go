package playback

import (
	"slices"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/speedtest"
)

type throughputSample struct {
	key      core.RatingKey
	protocol string
	speedtest.Observation
}

func (m *Manager) observeThroughput(key core.RatingKey, protocol string, at time.Time, readBps, mediaBps float64) {
	if protocol != core.ProtocolUsenet && protocol != core.ProtocolTorrent {
		return
	}
	m.throughputMu.Lock()
	defer m.throughputMu.Unlock()
	set := &m.throughputSamples
	for i, previous := range *set {
		if previous.key == key && previous.protocol == protocol {
			*set = slices.Delete(*set, i, i+1)
			break
		}
	}
	const maxSamples = 32
	if len(*set) == maxSamples {
		*set = slices.Delete(*set, 0, 1)
	}
	*set = append(*set, throughputSample{
		key:      key,
		protocol: protocol,
		Observation: speedtest.Observation{
			At:            at,
			SourceReadBps: int64(readBps),
			MediaBps:      int64(mediaBps),
		},
	})
}

// ProviderSpeedEvidence reuses the playback rate estimator's bounded window
// of recent titles. The median is a recent-playback baseline for the console.
func (m *Manager) ProviderSpeedEvidence() (typicalMbps float64, usenet []speedtest.Observation) {
	if m == nil {
		return 0, nil
	}
	m.throughputMu.Lock()
	defer m.throughputMu.Unlock()
	var rates []int64
	for _, sample := range m.throughputSamples {
		age := m.now().Sub(sample.At)
		if age <= 24*time.Hour {
			rates = append(rates, sample.MediaBps)
		}
		if sample.protocol == core.ProtocolUsenet && age <= time.Hour {
			usenet = append(usenet, sample.Observation)
		}
	}
	if len(rates) > 0 {
		slices.Sort(rates)
		n := len(rates)
		typicalMbps = (float64(rates[(n-1)/2]) + float64(rates[n/2])) * 4 / 1e6
	}
	return
}
