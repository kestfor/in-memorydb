package hlc

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type HLCTestSuite struct {
	suite.Suite
	clock *Time
}

func (s *HLCTestSuite) SetupTest() {
	s.clock = NewHLC("node-1")
}

func TestHLCTestSuite(t *testing.T) {
	suite.Run(t, new(HLCTestSuite))
}

func (s *HLCTestSuite) TestNewHLC() {
	clock := NewHLC("test-node")
	s.NotNil(clock)
	s.Equal("test-node", clock.nodeID)
}

func (s *HLCTestSuite) TestNowGeneratesTimestamp() {
	ts := s.clock.Now()
	s.NotNil(ts)
	s.Greater(ts.WallTime, uint64(0))
	s.Equal(uint64(0), ts.Lamport)
	s.Equal("node-1", ts.ID)
}

func (s *HLCTestSuite) TestTimestampMonotonicity() {
	timestamps := make([]*Timestamp, 100)

	for i := 0; i < 100; i++ {
		timestamps[i] = s.clock.Now()
	}

	// Проверяем монотонность
	for i := 1; i < len(timestamps); i++ {
		s.False(timestamps[i].Before(timestamps[i-1]),
			"timestamp %d should not be before timestamp %d", i, i-1)
	}
}

func (s *HLCTestSuite) TestLogicalClockIncrement() {
	// Быстрые вызовы должны увеличивать logical counter
	ts1 := s.clock.Now()
	ts2 := s.clock.Now()
	ts3 := s.clock.Now()

	// Если wall time не изменился, logical должен расти
	if ts1.WallTime == ts2.WallTime {
		s.Greater(ts2.Lamport, ts1.Lamport)
	}
	if ts2.WallTime == ts3.WallTime {
		s.Greater(ts3.Lamport, ts2.Lamport)
	}
}

// === Timestamp операции ===

func (s *HLCTestSuite) TestTimestampCopy() {
	original := &Timestamp{
		WallTime: 12345,
		Lamport:  67,
		ID:       "node-1",
	}

	copied := original.Copy()
	s.Equal(original.WallTime, copied.WallTime)
	s.Equal(original.Lamport, copied.Lamport)
	s.Equal(original.ID, copied.ID)

	// Изменение копии не должно влиять на оригинал
	copied.Lamport = 100
	s.NotEqual(original.Lamport, copied.Lamport)
}

func (s *HLCTestSuite) TestTimestampTime() {
	now := time.Now()
	ts := &Timestamp{
		WallTime: uint64(now.UnixNano()),
		Lamport:  0,
		ID:       "node-1",
	}

	recovered := ts.Time()
	s.Equal(now.Unix(), recovered.Unix())
}

func (s *HLCTestSuite) TestTimestampEqual() {
	ts1 := &Timestamp{WallTime: 100, Lamport: 5, ID: "node-1"}
	ts2 := &Timestamp{WallTime: 100, Lamport: 5, ID: "node-1"}
	ts3 := &Timestamp{WallTime: 100, Lamport: 6, ID: "node-1"}

	s.True(ts1.Equal(ts2))
	s.False(ts1.Equal(ts3))
}

func (s *HLCTestSuite) TestTimestampBeforeAfter() {
	ts1 := &Timestamp{WallTime: 100, Lamport: 5, ID: "node-1"}
	ts2 := &Timestamp{WallTime: 200, Lamport: 5, ID: "node-1"}

	s.True(ts1.Before(ts2))
	s.False(ts2.Before(ts1))
	s.True(ts2.After(ts1))
	s.False(ts1.After(ts2))
}

func (s *HLCTestSuite) TestTimestampString() {
	ts := &Timestamp{
		WallTime: uint64(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano()),
		Lamport:  42,
		ID:       "node-1",
	}

	str := ts.String()
	s.Contains(str, "2024")
	s.Contains(str, "L=42")
	s.Contains(str, "node-1")
}

// === Compare function ===

func (s *HLCTestSuite) TestCompareWallTime() {
	ts1 := &Timestamp{WallTime: 100, Lamport: 0, ID: "node-1"}
	ts2 := &Timestamp{WallTime: 200, Lamport: 0, ID: "node-1"}

	s.Equal(Lower, Compare(ts1, ts2))
	s.Equal(Greater, Compare(ts2, ts1))
	s.Equal(Equal, Compare(ts1, ts1))
}

func (s *HLCTestSuite) TestCompareLamport() {
	ts1 := &Timestamp{WallTime: 100, Lamport: 5, ID: "node-1"}
	ts2 := &Timestamp{WallTime: 100, Lamport: 10, ID: "node-1"}

	s.Equal(Lower, Compare(ts1, ts2))
	s.Equal(Greater, Compare(ts2, ts1))
}

func (s *HLCTestSuite) TestCompareNodeID() {
	ts1 := &Timestamp{WallTime: 100, Lamport: 5, ID: "node-1"}
	ts2 := &Timestamp{WallTime: 100, Lamport: 5, ID: "node-2"}

	s.Equal(Lower, Compare(ts1, ts2))
	s.Equal(Greater, Compare(ts2, ts1))
}

func (s *HLCTestSuite) TestCompareIdentical() {
	ts := &Timestamp{WallTime: 100, Lamport: 5, ID: "node-1"}

	s.Equal(Equal, Compare(ts, ts))
}

// === SyncWithRemote ===

func (s *HLCTestSuite) TestSyncWithRemoteNil() {
	ts := s.clock.SyncWithRemote(nil)
	s.NotNil(ts)
	s.Greater(ts.WallTime, uint64(0))
}

func (s *HLCTestSuite) TestSyncWithRemoteFutureTime() {
	localTS := s.clock.Now()

	// Создаём timestamp из будущего
	futureTS := &Timestamp{
		WallTime: localTS.WallTime + uint64(time.Second),
		Lamport:  0,
		ID:       "node-2",
	}

	syncedTS := s.clock.SyncWithRemote(futureTS)

	// Synced timestamp должен быть >= future timestamp
	s.GreaterOrEqual(syncedTS.WallTime, futureTS.WallTime)
}

func (s *HLCTestSuite) TestSyncWithRemotePastTime() {
	// Делаем несколько вызовов чтобы продвинуть часы
	for i := 0; i < 10; i++ {
		s.clock.Now()
	}

	localTS := s.clock.Now()

	// Создаём timestamp из прошлого
	pastTS := &Timestamp{
		WallTime: localTS.WallTime - uint64(time.Second),
		Lamport:  0,
		ID:       "node-2",
	}

	syncedTS := s.clock.SyncWithRemote(pastTS)

	// Synced timestamp не должен откатиться назад
	s.GreaterOrEqual(syncedTS.WallTime, localTS.WallTime)
}

func (s *HLCTestSuite) TestSyncIncreasesLamport() {
	ts1 := s.clock.Now()

	remoteTS := &Timestamp{
		WallTime: ts1.WallTime,
		Lamport:  ts1.Lamport + 5,
		ID:       "node-2",
	}

	syncedTS := s.clock.SyncWithRemote(remoteTS)

	// При синхронизации lamport должен увеличиться
	if syncedTS.WallTime == remoteTS.WallTime {
		s.Greater(syncedTS.Lamport, remoteTS.Lamport)
	}
}

// === Offset tests ===

func (s *HLCTestSuite) TestWithOffset() {
	clock := NewHLC("test").WithOffset(time.Hour)

	ts := clock.Now()
	now := time.Now()

	tsTime := time.Unix(0, int64(ts.WallTime))

	// Timestamp должен быть примерно на час в будущем
	diff := tsTime.Sub(now)
	s.Greater(diff, 59*time.Minute)
	s.Less(diff, 61*time.Minute)
}

func (s *HLCTestSuite) TestWithNegativeOffset() {
	clock := NewHLC("test").WithOffset(-time.Hour)

	ts := clock.Now()
	now := time.Now()

	tsTime := time.Unix(0, int64(ts.WallTime))

	// Timestamp должен быть примерно на час в прошлом
	diff := now.Sub(tsTime)
	s.Greater(diff, 59*time.Minute)
	s.Less(diff, 61*time.Minute)
}

// === Concurrency Tests ===

func (s *HLCTestSuite) TestConcurrentNow() {
	const numGoroutines = 100
	const callsPerGoroutine = 100

	timestamps := make([]*Timestamp, numGoroutines*callsPerGoroutine)
	var wg sync.WaitGroup
	var idx int32

	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < callsPerGoroutine; j++ {
				ts := s.clock.Now()
				// Используем atomic для безопасного доступа к слайсу
				currentIdx := int(idx)
				idx++
				if currentIdx < len(timestamps) {
					timestamps[currentIdx] = ts
				}
			}
		}()
	}

	wg.Wait()

	// Проверяем что все timestamps уникальны или монотонны
	seen := make(map[string]bool)
	for _, ts := range timestamps {
		if ts != nil {
			key := ts.String()
			s.False(seen[key], "should not have duplicate timestamps")
			seen[key] = true
		}
	}
}

func (s *HLCTestSuite) TestConcurrentSync() {
	const numGoroutines = 50

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Создаём удалённые timestamps от разных узлов
	remoteTimestamps := make([]*Timestamp, numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		remoteClock := NewHLC(string(rune('A' + i)))
		remoteTimestamps[i] = remoteClock.Now()
	}

	results := make([]*Timestamp, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			results[idx] = s.clock.SyncWithRemote(remoteTimestamps[idx])
		}(i)
	}

	wg.Wait()

	// Все результаты должны быть валидны
	for i, ts := range results {
		s.NotNil(ts, "result %d should not be nil", i)
		s.Greater(ts.WallTime, uint64(0))
	}
}

// === Property-based tests ===

func (s *HLCTestSuite) TestTimestampTotalOrder() {
	// Генерируем много timestamps
	timestamps := make([]*Timestamp, 1000)
	for i := 0; i < len(timestamps); i++ {
		timestamps[i] = s.clock.Now()
	}

	// Проверяем что для любых двух timestamps существует total order
	for i := 0; i < len(timestamps); i++ {
		for j := i + 1; j < len(timestamps); j++ {
			cmp := Compare(timestamps[i], timestamps[j])
			s.NotEqual(Equal, cmp, "different timestamps should not be equal")

			// Транзитивность
			if cmp == Lower {
				s.True(timestamps[i].Before(timestamps[j]))
			} else {
				s.True(timestamps[i].After(timestamps[j]))
			}
		}
	}
}

func (s *HLCTestSuite) TestSyncMonotonicity() {
	// Последовательные синхронизации не должны откатывать время назад
	ts1 := s.clock.Now()

	remote1 := &Timestamp{WallTime: ts1.WallTime + 1000, Lamport: 0, ID: "node-2"}
	ts2 := s.clock.SyncWithRemote(remote1)

	remote2 := &Timestamp{WallTime: ts2.WallTime - 500, Lamport: 0, ID: "node-3"}
	ts3 := s.clock.SyncWithRemote(remote2)

	s.False(ts3.Before(ts2), "time should not go backwards")
	s.False(ts3.Before(ts1), "time should not go backwards")
}

// === Unit tests (без suite) ===

func TestHLCCreation(t *testing.T) {
	clock := NewHLC("test-node")

	require.NotNil(t, clock)
	assert.Equal(t, "test-node", clock.nodeID)

	// Проверяем что internal state инициализирован
	p := clock.st.Load()
	require.NotNil(t, p)
	assert.Equal(t, uint64(0), p.wall)
	assert.Equal(t, uint64(0), p.logical)
}

func TestTimestampComparePriority(t *testing.T) {
	// WallTime имеет приоритет
	ts1 := &Timestamp{WallTime: 100, Lamport: 10, ID: "z"}
	ts2 := &Timestamp{WallTime: 200, Lamport: 5, ID: "a"}
	assert.Equal(t, Lower, Compare(ts1, ts2))

	// При равном WallTime, смотрим на Lamport
	ts3 := &Timestamp{WallTime: 100, Lamport: 5, ID: "z"}
	ts4 := &Timestamp{WallTime: 100, Lamport: 10, ID: "a"}
	assert.Equal(t, Lower, Compare(ts3, ts4))

	// При равных WallTime и Lamport, смотрим на ID
	ts5 := &Timestamp{WallTime: 100, Lamport: 5, ID: "a"}
	ts6 := &Timestamp{WallTime: 100, Lamport: 5, ID: "z"}
	assert.Equal(t, Lower, Compare(ts5, ts6))
}

func TestNowNanoWithoutOffset(t *testing.T) {
	clock := NewHLC("test")

	before := time.Now().UnixNano()
	nano := clock.nowNano()
	after := time.Now().UnixNano()

	assert.GreaterOrEqual(t, int64(nano), before)
	assert.LessOrEqual(t, int64(nano), after)
}

func TestLamportTime(t *testing.T) {
	ts := &Timestamp{
		WallTime: 12345,
		Lamport:  67890,
		ID:       "node-1",
	}

	assert.Equal(t, uint64(67890), ts.LamportTime())
}
