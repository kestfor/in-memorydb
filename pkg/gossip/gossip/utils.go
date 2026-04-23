package gossip

import "math"

//const targetReliability = 0.9

// Safety margin зависит от желаемой надёжности
// 99% reliability -> k=2, 99.9% -> k=3
//var safetyMargin = math.Ceil(-math.Log(1.0 - targetReliability))

//func getTTLNum(fanout int, clusterSize int) uint8 {
//	n := float64(clusterSize)
//	f := float64(fanout)
//
//	// Базовая формула: log_f(n) + ln(n)/ln(f)
//	exponentialPhase := math.Ceil(math.Log2(n) / math.Log2(f))
//	linearPhase := math.Ceil(math.Log(n) / math.Log(f))
//
//	ttl := uint8(exponentialPhase + linearPhase + safetyMargin)
//	return ttl
//}

func getTTLNumForAsync(clusterSize int, fanout int, antiEntropyIntervalSec int) uint8 {
	if clusterSize <= 1 {
		return 1
	}
	n := float64(clusterSize)
	f := float64(fanout)

	// Минимальное число хопов для покрытия кластера
	baseHops := math.Ceil(math.Log(n) / math.Log(f))

	ttl := uint8(baseHops * 1.5)

	return ttl
}
