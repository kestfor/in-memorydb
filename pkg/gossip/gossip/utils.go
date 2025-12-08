package gossip

import "math"

const targetReliability = 0.9

// Safety margin зависит от желаемой надёжности
// 99% reliability -> k=2, 99.9% -> k=3
var safetyMargin = math.Ceil(-math.Log(1.0 - targetReliability))

func getTTLNum(fanout int, clusterSize int) uint8 {
	n := float64(clusterSize)
	f := float64(fanout)

	// Базовая формула: log_f(n) + ln(n)/ln(f)
	exponentialPhase := math.Ceil(math.Log2(n) / math.Log2(f))
	linearPhase := math.Ceil(math.Log(n) / math.Log(f))

	ttl := uint8(exponentialPhase + linearPhase + safetyMargin)
	return ttl
}

func getTTLNumForAsync(clusterSize int, fanout int, antiEntropyIntervalSec int) uint8 {
	n := float64(clusterSize)
	f := float64(fanout)

	// Базовое число хопов (как в синхронной модели)
	baseHops := math.Ceil(math.Log(n) / math.Log(f))

	// Коррекция на асинхронность: +50% для safety
	asyncMultiplier := 1.5

	// Коррекция на длинный интервал anti-entropy
	// Если интервал > 1 сек, увеличиваем TTL
	intervalPenalty := math.Max(1.0, float64(antiEntropyIntervalSec)/2.0)
	ttl := uint16(math.Ceil(baseHops * asyncMultiplier * intervalPenalty))
	return uint8(min(math.MaxUint8, ttl))
}
