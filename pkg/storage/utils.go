package storage

import (
	"hash/fnv"
	"in-memorydb/pkg/structs"
	"log/slog"
)

func hashKey(key string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(key))
	return h.Sum32()
}

func VersionDifference(source Version, received Version) Version {
	res := Version{}
	for nodeID, rang := range received {

		// new node
		oldRang, ok := source[nodeID]
		if !ok {
			res[nodeID] = rang
			continue
		}

		// difference
		if oldRang.End < rang.End {
			res[nodeID] = structs.Range{Start: oldRang.End + 1, End: rang.End}

			if rang.Start < oldRang.Start {
				slog.Error("new range version start less than old", "old", oldRang, "new", rang, "node", nodeID)
			}

		}
	}
	return res
}
