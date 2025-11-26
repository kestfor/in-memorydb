package trashed

//
//import (
//	"container/list"
//	"sync"
//	"time"
//)
//
//// BufferedUpdate - обертка над Update с дополнительной метаинформацией
//type BufferedUpdate struct {
//	Update    Update    // сам update
//	AddedAt   time.Time // когда добавлен в буфер
//	NodeID    string    // ID ноды-источника
//	Sequence  int64     // sequence number
//}
//
//// DeltaBuffer - потокобезопасный кольцевой буфер для хранения updates
//// Используется для:
//// - Gossip: отправка последних updates peers
//// - Anti-entropy: replay пропущенных updates
//// - Recovery: восстановление после временной недоступности
//type DeltaBuffer struct {
//	mu          sync.RWMutex
//	buffer      *list.List                    // FIFO очередь updates
//	maxSize     int                           // максимальный размер буфера
//	byNode      map[string]*list.List         // индекс по nodeID для быстрого поиска
//	bySequence  map[string]map[int64]*list.Element // nodeID -> sequence -> element
//	totalSize   int                           // текущий размер
//}
//
//// NewDeltaBuffer создает новый буфер с указанным максимальным размером
//func NewDeltaBuffer(maxSize int) *DeltaBuffer {
//	if maxSize <= 0 {
//		maxSize = 1000 // default
//	}
//
//	return &DeltaBuffer{
//		buffer:     list.New(),
//		maxSize:    maxSize,
//		byNode:     make(map[string]*list.List),
//		bySequence: make(map[string]map[int64]*list.Element),
//		totalSize:  0,
//	}
//}
//
//// Add добавляет update в буфер
//// Если буфер заполнен, удаляет самый старый элемент
//func (db *DeltaBuffer) Add(update Update) {
//	db.mu.Lock()
//	defer db.mu.Unlock()
//
//	// Извлекаем sequence number из Version
//	if len(update.Version.Sequence) == 0 {
//		return // invalid update
//	}
//
//	nodeID := update.Version.ReplicaID
//	// Берем максимальный sequence (последний после сортировки)
//	maxSeq := update.Version.Sequence[len(update.Version.Sequence)-1]
//
//	buffered := &BufferedUpdate{
//		Update:   update,
//		AddedAt:  time.Now(),
//		NodeID:   nodeID,
//		Sequence: maxSeq,
//	}
//
//	// Добавляем в основной буфер
//	elem := db.buffer.PushBack(buffered)
//	db.totalSize++
//
//	// Инициализируем индексы для ноды если нужно
//	if _, exists := db.byNode[nodeID]; !exists {
//		db.byNode[nodeID] = list.New()
//		db.bySequence[nodeID] = make(map[int64]*list.Element)
//	}
//
//	// Добавляем в индексы
//	db.byNode[nodeID].PushBack(buffered)
//	db.bySequence[nodeID][maxSeq] = elem
//
//	// Удаляем старые если превышен размер
//	db.evictOldest()
//}
//
//// evictOldest удаляет самые старые updates если буфер переполнен
//// Должен вызываться под locked mutex
//func (db *DeltaBuffer) evictOldest() {
//	for db.totalSize > db.maxSize {
//		elem := db.buffer.Front()
//		if elem == nil {
//			break
//		}
//
//		buffered := elem.Value.(*BufferedUpdate)
//
//		// Удаляем из основного буфера
//		db.buffer.Remove(elem)
//		db.totalSize--
//
//		// Удаляем из индексов
//		nodeID := buffered.NodeID
//		if nodeList, ok := db.byNode[nodeID]; ok {
//			// Находим и удаляем из индекса по ноде
//			for e := nodeList.Front(); e != nil; e = e.Next() {
//				if b, ok := e.Value.(*BufferedUpdate); ok && b.Sequence == buffered.Sequence {
//					nodeList.Remove(e)
//					break
//				}
//			}
//		}
//
//		if seqMap, ok := db.bySequence[nodeID]; ok {
//			delete(seqMap, buffered.Sequence)
//		}
//	}
//}
//
//// GetRecent возвращает N последних updates из буфера
//func (db *DeltaBuffer) GetRecent(n int) []Update {
//	db.mu.RLock()
//	defer db.mu.RUnlock()
//
//	if n <= 0 {
//		return nil
//	}
//
//	result := make([]Update, 0, n)
//	count := 0
//
//	// Идем с конца (самые свежие)
//	for elem := db.buffer.Back(); elem != nil && count < n; elem = elem.Prev() {
//		buffered := elem.Value.(*BufferedUpdate)
//		result = append(result, buffered.Update)
//		count++
//	}
//
//	return result
//}
//
//// GetByNodeRecent возвращает N последних updates от указанной ноды
//func (db *DeltaBuffer) GetByNodeRecent(nodeID string, n int) []Update {
//	db.mu.RLock()
//	defer db.mu.RUnlock()
//
//	nodeList, ok := db.byNode[nodeID]
//	if !ok || nodeList.Len() == 0 {
//		return nil
//	}
//
//	if n <= 0 {
//		return nil
//	}
//
//	result := make([]Update, 0, n)
//	count := 0
//
//	// Идем с конца (самые свежие)
//	for elem := nodeList.Back(); elem != nil && count < n; elem = elem.Prev() {
//		buffered := elem.Value.(*BufferedUpdate)
//		result = append(result, buffered.Update)
//		count++
//	}
//
//	return result
//}
//
//// GetByRanges возвращает updates в указанных диапазонах sequence numbers
//// Используется для anti-entropy и восстановления пропущенных updates
//func (db *DeltaBuffer) GetByRanges(nodeID string, ranges []Range) []Update {
//	db.mu.RLock()
//	defer db.mu.RUnlock()
//
//	seqMap, ok := db.bySequence[nodeID]
//	if !ok {
//		return nil
//	}
//
//	result := make([]Update, 0)
//
//	for _, r := range ranges {
//		for seq := r.Start; seq <= r.End; seq++ {
//			if elem, found := seqMap[seq]; found {
//				buffered := elem.Value.(*BufferedUpdate)
//				result = append(result, buffered.Update)
//			}
//		}
//	}
//
//	return result
//}
//
//// GetBySequence возвращает update с указанным sequence number от ноды
//func (db *DeltaBuffer) GetBySequence(nodeID string, seq int64) (Update, bool) {
//	db.mu.RLock()
//	defer db.mu.RUnlock()
//
//	seqMap, ok := db.bySequence[nodeID]
//	if !ok {
//		return Update{}, false
//	}
//
//	elem, found := seqMap[seq]
//	if !found {
//		return Update{}, false
//	}
//
//	buffered := elem.Value.(*BufferedUpdate)
//	return buffered.Update, true
//}
//
//// Cleanup удаляет updates старше указанного времени
//func (db *DeltaBuffer) Cleanup(threshold time.Duration) int {
//	db.mu.Lock()
//	defer db.mu.Unlock()
//
//	cutoff := time.Now().Add(-threshold)
//	removed := 0
//
//	for elem := db.buffer.Front(); elem != nil; {
//		buffered := elem.Value.(*BufferedUpdate)
//
//		if buffered.AddedAt.Before(cutoff) {
//			next := elem.Next()
//
//			// Удаляем из основного буфера
//			db.buffer.Remove(elem)
//			db.totalSize--
//			removed++
//
//			// Удаляем из индексов
//			nodeID := buffered.NodeID
//			if nodeList, ok := db.byNode[nodeID]; ok {
//				for e := nodeList.Front(); e != nil; e = e.Next() {
//					if b, ok := e.Value.(*BufferedUpdate); ok && b.Sequence == buffered.Sequence {
//						nodeList.Remove(e)
//						break
//					}
//				}
//			}
//
//			if seqMap, ok := db.bySequence[nodeID]; ok {
//				delete(seqMap, buffered.Sequence)
//			}
//
//			elem = next
//		} else {
//			// Буфер отсортирован по времени добавления, можем остановиться
//			break
//		}
//	}
//
//	return removed
//}
//
//// Size возвращает текущий размер буфера
//func (db *DeltaBuffer) Size() int {
//	db.mu.RLock()
//	defer db.mu.RUnlock()
//	return db.totalSize
//}
//
//// NodeCount возвращает количество нод в буфере
//func (db *DeltaBuffer) NodeCount() int {
//	db.mu.RLock()
//	defer db.mu.RUnlock()
//	return len(db.byNode)
//}
//
//// GetNodeSequenceRange возвращает минимальный и максимальный sequence для ноды
//func (db *DeltaBuffer) GetNodeSequenceRange(nodeID string) (min, max int64, ok bool) {
//	db.mu.RLock()
//	defer db.mu.RUnlock()
//
//	seqMap, exists := db.bySequence[nodeID]
//	if !exists || len(seqMap) == 0 {
//		return 0, 0, false
//	}
//
//	min = int64(^uint64(0) >> 1) // max int64
//	max = int64(^uint64(0)>>1) * -1 // min int64
//
//	for seq := range seqMap {
//		if seq < min {
//			min = seq
//		}
//		if seq > max {
//			max = seq
//		}
//	}
//
//	return min, max, true
//}
//
//// GetAll возвращает все updates из буфера (для тестов и отладки)
//func (db *DeltaBuffer) GetAll() []Update {
//	db.mu.RLock()
//	defer db.mu.RUnlock()
//
//	result := make([]Update, 0, db.totalSize)
//	for elem := db.buffer.Front(); elem != nil; elem = elem.Next() {
//		buffered := elem.Value.(*BufferedUpdate)
//		result = append(result, buffered.Update)
//	}
//
//	return result
//}
//
//// Clear очищает весь буфер
//func (db *DeltaBuffer) Clear() {
//	db.mu.Lock()
//	defer db.mu.Unlock()
//
//	db.buffer.Init()
//	db.byNode = make(map[string]*list.List)
//	db.bySequence = make(map[string]map[int64]*list.Element)
//	db.totalSize = 0
//}
