package riststats

import (
	"sort"
	"sync"
	"time"
)

const StaleAfter = 5 * time.Second

type Statistics struct {
	SessionName      string
	CameraName       string
	GatewayInstance  string
	FlowID           uint32
	IntervalStart    time.Time
	IntervalEnd      time.Time
	OutputPackets    uint64
	LostPackets      uint64
	RecoveredPackets uint64
	Discontinuities  uint64
	ReceivedAt       time.Time
}

type Snapshot struct {
	Statistics
	Stale bool
}

type statisticsKey struct {
	sessionName string
	cameraName  string
	flowID      uint32
}

type Store struct {
	mutex      sync.RWMutex
	statistics map[statisticsKey]Statistics
	now        func() time.Time
}

func NewStore() *Store {
	return newStore(time.Now)
}

func newStore(now func() time.Time) *Store {
	return &Store{statistics: make(map[statisticsKey]Statistics), now: now}
}

func (s *Store) Record(statistics []Statistics) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for _, candidate := range statistics {
		key := statisticsKey{candidate.SessionName, candidate.CameraName, candidate.FlowID}
		current, exists := s.statistics[key]
		if exists && candidate.IntervalEnd.Before(current.IntervalEnd) {
			continue
		}
		candidate.ReceivedAt = s.now()
		s.statistics[key] = candidate
	}
}

func (s *Store) List(sessionName string) []Snapshot {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	now := s.now()
	result := make([]Snapshot, 0)
	for _, statistics := range s.statistics {
		if statistics.SessionName != sessionName {
			continue
		}
		result = append(result, Snapshot{
			Statistics: statistics,
			Stale:      now.Sub(statistics.ReceivedAt) > StaleAfter,
		})
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].CameraName == result[right].CameraName {
			return result[left].FlowID < result[right].FlowID
		}
		return result[left].CameraName < result[right].CameraName
	})
	return result
}
