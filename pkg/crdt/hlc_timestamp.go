package crdt

type HLCTimestamp struct {
	WallTime int64  `json:"wall_time"` // unix nano
	Lamport  int64  `json:"lamport"`   // локальный счетчик
	ID       string `json:"id"`        // replica ID для tie-break
}

// CompareHLC Сравнивает два HLC: возвращает -1,0,1
func CompareHLC(a, b HLCTimestamp) int {
	if a.WallTime < b.WallTime {
		return -1
	}
	if a.WallTime > b.WallTime {
		return 1
	}
	if a.Lamport < b.Lamport {
		return -1
	}
	if a.Lamport > b.Lamport {
		return 1
	}
	if a.ID < b.ID {
		return -1
	}
	if a.ID > b.ID {
		return 1
	}
	return 0
}
