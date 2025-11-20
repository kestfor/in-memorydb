package trashed

//
//import (
//	"context"
//	"fmt"
//	"log/slog"
//	"math/rand"
//	"sync"
//	"time"
//)
//
//// GossipConfig содержит конфигурацию gossip протокола
//type GossipConfig struct {
//	GossipInterval      time.Duration // интервал отправки gossip (default: 2s)
//	Fanout              int           // количество нод для отправки в каждом раунде (default: 3)
//	AntiEntropyInterval time.Duration // интервал anti-entropy синхронизации (default: 60s)
//	BufferSize          int           // размер delta buffer (default: 1000)
//	PingInterval        time.Duration // интервал проверки доступности peers (default: 10s)
//	FailureThreshold    int           // количество неудачных ping для перевода в suspected (default: 3)
//	SuspectedTimeout    time.Duration // таймаут перед переводом из suspected в dead (default: 30s)
//}
//
//// DefaultGossipConfig возвращает конфигурацию по умолчанию
//func DefaultGossipConfig() *GossipConfig {
//	return &GossipConfig{
//		GossipInterval:      2 * time.Second,
//		Fanout:              3,
//		AntiEntropyInterval: 60 * time.Second,
//		BufferSize:          1000,
//		PingInterval:        10 * time.Second,
//		FailureThreshold:    3,
//		SuspectedTimeout:    30 * time.Second,
//	}
//}
//
//// GossipManager управляет gossip протоколом для распространения updates
//type GossipManager struct {
//	nodeID         string
//	versionManager *VersionManager
//	transport      Transport
//	config         *GossipConfig
//
//	// Peer management
//	peers      sync.Map // nodeID -> *PeerInfo
//	peersMu    sync.RWMutex
//	peersList  []*PeerInfo // кешированный список для быстрого выбора
//
//	// Delta buffering
//	deltaBuffer *DeltaBuffer
//
//	// Lifecycle
//	ctx    context.Context
//	cancel context.CancelFunc
//	wg     sync.WaitGroup
//
//	// Stats
//	stats struct {
//		gossipRounds     int64
//		antiEntropyRuns  int64
//		missedRequests   int64
//		updatesReceived  int64
//		updatesSent      int64
//	}
//}
//
//// NewGossipManager создает новый GossipManager
//func NewGossipManager(nodeID string, vm *VersionManager, transport Transport, config *GossipConfig) *GossipManager {
//	if config == nil {
//		config = DefaultGossipConfig()
//	}
//
//	return &GossipManager{
//		nodeID:         nodeID,
//		versionManager: vm,
//		transport:      transport,
//		config:         config,
//		deltaBuffer:    NewDeltaBuffer(config.BufferSize),
//		peersList:      make([]*PeerInfo, 0),
//	}
//}
//
//// Start запускает gossip loops
//func (gm *GossipManager) Start(ctx context.Context) error {
//	gm.ctx, gm.cancel = context.WithCancel(ctx)
//
//	// Регистрируем себя как обработчик сообщений
//	gm.transport.RegisterHandler(gm)
//
//	// Запускаем транспорт
//	if err := gm.transport.Start(gm.ctx); err != nil {
//		return fmt.Errorf("failed to start transport: %w", err)
//	}
//
//	// Запускаем фоновые процессы
//	gm.wg.Add(4)
//	go gm.gossipLoop()
//	go gm.antiEntropyLoop()
//	go gm.pingLoop()
//	go gm.cleanupLoop()
//
//	slog.Info("GossipManager started",
//		"node_id", gm.nodeID,
//		"gossip_interval", gm.config.GossipInterval,
//		"fanout", gm.config.Fanout,
//	)
//
//	return nil
//}
//
//// Stop останавливает gossip
//func (gm *GossipManager) Stop() error {
//	if gm.cancel != nil {
//		gm.cancel()
//	}
//
//	// Ждем завершения всех goroutines
//	gm.wg.Wait()
//
//	// Останавливаем транспорт
//	return gm.transport.Stop()
//}
//
//// Publish добавляет локальный update для распространения
//func (gm *GossipManager) Publish(update Update) {
//	gm.deltaBuffer.Add(update)
//	slog.Debug("Update published to gossip",
//		"key", update.Key,
//		"node_id", update.Version.ReplicaID,
//		"sequence", update.Version.Sequence,
//	)
//}
//
//// gossipLoop основной цикл gossip
//func (gm *GossipManager) gossipLoop() {
//	defer gm.wg.Done()
//
//	ticker := time.NewTicker(gm.config.GossipInterval)
//	defer ticker.Stop()
//
//	for {
//		select {
//		case <-gm.ctx.Done():
//			return
//		case <-ticker.C:
//			gm.doGossipRound()
//		}
//	}
//}
//
//// doGossipRound выполняет один раунд gossip
//func (gm *GossipManager) doGossipRound() {
//	// Выбираем случайные peers
//	peers := gm.selectRandomPeers(gm.config.Fanout)
//	if len(peers) == 0 {
//		return
//	}
//
//	// Собираем последние updates
//	recentUpdates := gm.deltaBuffer.GetRecent(100)
//	if len(recentUpdates) == 0 {
//		// Нет новых updates, но отправляем version vector
//		recentUpdates = []Update{}
//	}
//
//	// Получаем текущий version vector
//	versionVec := gm.versionManager.GetVersionVector()
//
//	// Создаем gossip message
//	msg := &GossipMessage{
//		SenderID:   gm.nodeID,
//		Timestamp:  gm.versionManager.engine.clock.Now(),
//		Deltas:     recentUpdates,
//		VersionVec: versionVec,
//	}
//
//	// Создаем envelope
//	envelope, err := NewMessageEnvelope(MessageTypeGossip, msg, msg.Timestamp)
//	if err != nil {
//		slog.Error("Failed to create message envelope", "error", err)
//		return
//	}
//
//	// Отправляем всем выбранным peers
//	for _, peer := range peers {
//		go func(p *PeerInfo) {
//			ctx, cancel := context.WithTimeout(gm.ctx, 5*time.Second)
//			defer cancel()
//
//			err := gm.transport.Send(ctx, p.NodeID, envelope)
//			if err != nil {
//				slog.Warn("Failed to send gossip",
//					"target", p.NodeID,
//					"error", err,
//				)
//				gm.handlePeerFailure(p.NodeID)
//			} else {
//				gm.updatePeerLastSeen(p.NodeID)
//			}
//		}(peer)
//	}
//
//	gm.stats.gossipRounds++
//}
//
//// antiEntropyLoop цикл anti-entropy синхронизации
//func (gm *GossipManager) antiEntropyLoop() {
//	defer gm.wg.Done()
//
//	ticker := time.NewTicker(gm.config.AntiEntropyInterval)
//	defer ticker.Stop()
//
//	for {
//		select {
//		case <-gm.ctx.Done():
//			return
//		case <-ticker.C:
//			gm.doAntiEntropyRound()
//		}
//	}
//}
//
//// doAntiEntropyRound выполняет один раунд anti-entropy
//func (gm *GossipManager) doAntiEntropyRound() {
//	// Выбираем одного случайного peer
//	peers := gm.selectRandomPeers(1)
//	if len(peers) == 0 {
//		return
//	}
//
//	peer := peers[0]
//
//	slog.Info("Starting anti-entropy",
//		"target", peer.NodeID,
//	)
//
//	// Создаем запрос
//	req := &AntiEntropyRequest{
//		SenderID:   gm.nodeID,
//		VersionVec: gm.versionManager.GetVersionVector(),
//		Timestamp:  gm.versionManager.engine.clock.Now(),
//	}
//
//	envelope, err := NewMessageEnvelope(MessageTypeAntiEntropyRequest, req, req.Timestamp)
//	if err != nil {
//		slog.Error("Failed to create anti-entropy request", "error", err)
//		return
//	}
//
//	// Отправляем запрос и ждем ответ
//	ctx, cancel := context.WithTimeout(gm.ctx, 30*time.Second)
//	defer cancel()
//
//	respEnvelope, err := gm.transport.Request(ctx, peer.NodeID, envelope)
//	if err != nil {
//		slog.Warn("Anti-entropy request failed",
//			"target", peer.NodeID,
//			"error", err,
//		)
//		gm.handlePeerFailure(peer.NodeID)
//		return
//	}
//
//	// Обрабатываем ответ
//	var resp AntiEntropyResponse
//	if err := respEnvelope.Unmarshal(&resp); err != nil {
//		slog.Error("Failed to unmarshal anti-entropy response", "error", err)
//		return
//	}
//
//	// Применяем пропущенные updates
//	if len(resp.MissingUpdates) > 0 {
//		gm.versionManager.Update(resp.MissingUpdates...)
//		slog.Info("Applied missing updates",
//			"target", peer.NodeID,
//			"count", len(resp.MissingUpdates),
//		)
//	}
//
//	gm.stats.antiEntropyRuns++
//}
//
//// pingLoop периодически проверяет доступность peers
//func (gm *GossipManager) pingLoop() {
//	defer gm.wg.Done()
//
//	ticker := time.NewTicker(gm.config.PingInterval)
//	defer ticker.Stop()
//
//	for {
//		select {
//		case <-gm.ctx.Done():
//			return
//		case <-ticker.C:
//			gm.pingAllPeers()
//		}
//	}
//}
//
//// pingAllPeers пингует все активные peers
//func (gm *GossipManager) pingAllPeers() {
//	peers := gm.getAllPeers()
//
//	for _, peer := range peers {
//		if peer.Status == StatusDead {
//			continue
//		}
//
//		go func(p *PeerInfo) {
//			ctx, cancel := context.WithTimeout(gm.ctx, 5*time.Second)
//			defer cancel()
//
//			req := &PingRequest{
//				SenderID:  gm.nodeID,
//				Timestamp: gm.versionManager.engine.clock.Now(),
//			}
//
//			envelope, err := NewMessageEnvelope(MessageTypeMissedRequest, req, req.Timestamp)
//			if err != nil {
//				return
//			}
//
//			_, err = gm.transport.Request(ctx, p.NodeID, envelope)
//			if err != nil {
//				gm.handlePeerFailure(p.NodeID)
//			} else {
//				gm.updatePeerLastSeen(p.NodeID)
//				gm.resetPeerFailures(p.NodeID)
//			}
//		}(peer)
//	}
//}
//
//// cleanupLoop периодически очищает старые данные
//func (gm *GossipManager) cleanupLoop() {
//	defer gm.wg.Done()
//
//	ticker := time.NewTicker(5 * time.Minute)
//	defer ticker.Stop()
//
//	for {
//		select {
//		case <-gm.ctx.Done():
//			return
//		case <-ticker.C:
//			// Очищаем старые updates из буфера (старше 10 минут)
//			removed := gm.deltaBuffer.Cleanup(10 * time.Minute)
//			if removed > 0 {
//				slog.Debug("Cleaned up old updates", "count", removed)
//			}
//		}
//	}
//}
//
//// selectRandomPeers выбирает N случайных активных peers
//func (gm *GossipManager) selectRandomPeers(n int) []*PeerInfo {
//	gm.peersMu.RLock()
//	defer gm.peersMu.RUnlock()
//
//	// Фильтруем только alive peers
//	alivePeers := make([]*PeerInfo, 0)
//	for _, peer := range gm.peersList {
//		if peer.Status == StatusAlive {
//			alivePeers = append(alivePeers, peer)
//		}
//	}
//
//	if len(alivePeers) == 0 {
//		return nil
//	}
//
//	if n >= len(alivePeers) {
//		return alivePeers
//	}
//
//	// Случайно перемешиваем и берем первые N
//	selected := make([]*PeerInfo, n)
//	perm := rand.Perm(len(alivePeers))
//	for i := 0; i < n; i++ {
//		selected[i] = alivePeers[perm[i]]
//	}
//
//	return selected
//}
//
//// AddPeer добавляет новый peer
//func (gm *GossipManager) AddPeer(nodeID string, address string) error {
//	if nodeID == gm.nodeID {
//		return nil // не добавляем себя
//	}
//
//	peer := &PeerInfo{
//		NodeID:      nodeID,
//		Address:     address,
//		LastSeen:    time.Now(),
//		Status:      StatusAlive,
//		Version:     0,
//		FailedPings: 0,
//	}
//
//	gm.peers.Store(nodeID, peer)
//	gm.refreshPeersList()
//
//	// Регистрируем в version manager
//	gm.versionManager.RegisterNode(nodeID)
//
//	// Добавляем в транспорт
//	return gm.transport.AddPeer(*peer)
//}
//
//// RemovePeer удаляет peer
//func (gm *GossipManager) RemovePeer(nodeID string) error {
//	gm.peers.Delete(nodeID)
//	gm.refreshPeersList()
//	return gm.transport.RemovePeer(nodeID)
//}
//
//// refreshPeersList обновляет кешированный список peers
//func (gm *GossipManager) refreshPeersList() {
//	gm.peersMu.Lock()
//	defer gm.peersMu.Unlock()
//
//	peers := make([]*PeerInfo, 0)
//	gm.peers.Range(func(key, value interface{}) bool {
//		peer := value.(*PeerInfo)
//		peers = append(peers, peer)
//		return true
//	})
//
//	gm.peersList = peers
//}
//
//// getAllPeers возвращает все peers
//func (gm *GossipManager) getAllPeers() []*PeerInfo {
//	gm.peersMu.RLock()
//	defer gm.peersMu.RUnlock()
//	return append([]*PeerInfo{}, gm.peersList...)
//}
//
//// updatePeerLastSeen обновляет время последнего контакта с peer
//func (gm *GossipManager) updatePeerLastSeen(nodeID string) {
//	if value, ok := gm.peers.Load(nodeID); ok {
//		peer := value.(*PeerInfo)
//		peer.LastSeen = time.Now()
//		if peer.Status != StatusAlive {
//			peer.Status = StatusAlive
//			gm.transport.UpdatePeerStatus(nodeID, StatusAlive)
//		}
//	}
//}
//
//// handlePeerFailure обрабатывает неудачу связи с peer
//func (gm *GossipManager) handlePeerFailure(nodeID string) {
//	value, ok := gm.peers.Load(nodeID)
//	if !ok {
//		return
//	}
//
//	peer := value.(*PeerInfo)
//	peer.FailedPings++
//
//	if peer.FailedPings >= gm.config.FailureThreshold {
//		if peer.Status == StatusAlive {
//			peer.Status = StatusSuspected
//			gm.transport.UpdatePeerStatus(nodeID, StatusSuspected)
//
//			slog.Warn("Peer suspected",
//				"node_id", nodeID,
//				"failed_pings", peer.FailedPings,
//			)
//
//			// Запускаем таймер для перевода в dead
//			go gm.scheduleDeadTransition(nodeID)
//		}
//	}
//}
//
//// scheduleDeadTransition переводит peer в dead после таймаута
//func (gm *GossipManager) scheduleDeadTransition(nodeID string) {
//	select {
//	case <-time.After(gm.config.SuspectedTimeout):
//		value, ok := gm.peers.Load(nodeID)
//		if !ok {
//			return
//		}
//
//		peer := value.(*PeerInfo)
//		if peer.Status == StatusSuspected {
//			peer.Status = StatusDead
//			gm.transport.UpdatePeerStatus(nodeID, StatusDead)
//
//			slog.Warn("Peer marked as dead",
//				"node_id", nodeID,
//			)
//		}
//	case <-gm.ctx.Done():
//		return
//	}
//}
//
//// resetPeerFailures сбрасывает счетчик неудач для peer
//func (gm *GossipManager) resetPeerFailures(nodeID string) {
//	if value, ok := gm.peers.Load(nodeID); ok {
//		peer := value.(*PeerInfo)
//		peer.FailedPings = 0
//	}
//}
//
//// requestMissedUpdates запрашивает пропущенные updates от указанной ноды
//func (gm *GossipManager) requestMissedUpdates(nodeID string) error {
//	// Получаем диапазоны пропущенных updates
//	ranges := gm.versionManager.GetMissingRanges(nodeID)
//	if len(ranges) == 0 {
//		return nil
//	}
//
//	slog.Info("Requesting missed updates",
//		"source_node", nodeID,
//		"ranges", ranges,
//	)
//
//	req := &MissedUpdateRequest{
//		SenderID:  gm.nodeID,
//		NodeID:    nodeID,
//		Ranges:    ranges,
//		Timestamp: gm.versionManager.engine.clock.Now(),
//	}
//
//	envelope, err := NewMessageEnvelope(MessageTypeMissedRequest, req, req.Timestamp)
//	if err != nil {
//		return err
//	}
//
//	ctx, cancel := context.WithTimeout(gm.ctx, 10*time.Second)
//	defer cancel()
//
//	respEnvelope, err := gm.transport.Request(ctx, nodeID, envelope)
//	if err != nil {
//		return fmt.Errorf("missed update request failed: %w", err)
//	}
//
//	var resp MissedUpdateResponse
//	if err := respEnvelope.Unmarshal(&resp); err != nil {
//		return fmt.Errorf("failed to unmarshal response: %w", err)
//	}
//
//	// Применяем полученные updates
//	if len(resp.Updates) > 0 {
//		gm.versionManager.Update(resp.Updates...)
//		slog.Info("Applied missed updates",
//			"source_node", nodeID,
//			"count", len(resp.Updates),
//		)
//	}
//
//	gm.stats.missedRequests++
//	return nil
//}
//
//// GetStats возвращает статистику работы gossip
//func (gm *GossipManager) GetStats() map[string]int64 {
//	return map[string]int64{
//		"gossip_rounds":     gm.stats.gossipRounds,
//		"anti_entropy_runs": gm.stats.antiEntropyRuns,
//		"missed_requests":   gm.stats.missedRequests,
//		"updates_received":  gm.stats.updatesReceived,
//		"updates_sent":      gm.stats.updatesSent,
//	}
//}
