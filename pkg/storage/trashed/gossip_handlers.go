package trashed

//
//import (
//	"context"
//	"fmt"
//	"log/slog"
//)
//
//// HandleMessage реализует MessageHandler интерфейс
//// Маршрутизирует входящие сообщения к соответствующим обработчикам
//func (gm *GossipManager) HandleMessage(ctx context.Context, envelope *MessageEnvelope) (*MessageEnvelope, error) {
//	// Синхронизируем HLC clock с входящим timestamp
//	gm.versionManager.engine.clock.SyncWithRemote(envelope.Timestamp)
//
//	switch envelope.Type {
//	case MessageTypeGossip:
//		var msg GossipMessage
//		if err := envelope.Unmarshal(&msg); err != nil {
//			return nil, fmt.Errorf("failed to unmarshal gossip message: %w", err)
//		}
//		if err := gm.HandleGossip(ctx, &msg); err != nil {
//			return nil, err
//		}
//		return nil, nil // gossip не требует ответа
//
//	case MessageTypeAntiEntropyRequest:
//		var req AntiEntropyRequest
//		if err := envelope.Unmarshal(&req); err != nil {
//			return nil, fmt.Errorf("failed to unmarshal anti-entropy request: %w", err)
//		}
//		resp, err := gm.HandleAntiEntropy(ctx, &req)
//		if err != nil {
//			return nil, err
//		}
//		return NewMessageEnvelope(MessageTypeAntiEntropyResponse, resp, gm.versionManager.engine.clock.Now())
//
//	case MessageTypeMissedRequest:
//		var req MissedUpdateRequest
//		if err := envelope.Unmarshal(&req); err != nil {
//			return nil, fmt.Errorf("failed to unmarshal missed request: %w", err)
//		}
//		resp, err := gm.HandleMissedRequest(ctx, &req)
//		if err != nil {
//			return nil, err
//		}
//		return NewMessageEnvelope(MessageTypeMissedResponse, resp, gm.versionManager.engine.clock.Now())
//
//	default:
//		return nil, fmt.Errorf("unknown message type: %s", envelope.Type)
//	}
//}
//
//// HandleGossip обрабатывает входящее gossip сообщение
//func (gm *GossipManager) HandleGossip(ctx context.Context, msg *GossipMessage) error {
//	slog.Debug("Received gossip message",
//		"sender", msg.SenderID,
//		"deltas_count", len(msg.Deltas),
//	)
//
//	// Обновляем время последнего контакта с отправителем
//	gm.updatePeerLastSeen(msg.SenderID)
//
//	// Применяем полученные deltas
//	if len(msg.Deltas) > 0 {
//		gm.versionManager.Update(msg.Deltas...)
//		gm.stats.updatesReceived += int64(len(msg.Deltas))
//
//		slog.Debug("Applied gossip deltas",
//			"sender", msg.SenderID,
//			"count", len(msg.Deltas),
//		)
//	}
//
//	// Сравниваем version vectors для обнаружения пропущенных updates
//	localVec := gm.versionManager.GetVersionVector()
//
//	for nodeID, remoteSeq := range msg.VersionVec {
//		localSeq, exists := localVec[nodeID]
//
//		// Если у отправителя есть более свежие updates
//		if !exists || remoteSeq > localSeq {
//			// Проверяем есть ли пропущенные updates
//			if gm.versionManager.HasMissedUpdates(nodeID) {
//				// Асинхронно запрашиваем пропущенные updates
//				go func(nid string) {
//					if err := gm.requestMissedUpdates(nid); err != nil {
//						slog.Warn("Failed to request missed updates",
//							"node_id", nid,
//							"error", err,
//						)
//					}
//				}(nodeID)
//			}
//		}
//	}
//
//	return nil
//}
//
//// HandleAntiEntropy обрабатывает запрос anti-entropy синхронизации
//// Сравнивает version vectors и возвращает пропущенные updates
//func (gm *GossipManager) HandleAntiEntropy(ctx context.Context, req *AntiEntropyRequest) (*AntiEntropyResponse, error) {
//	slog.Info("Received anti-entropy request",
//		"sender", req.SenderID,
//	)
//
//	gm.updatePeerLastSeen(req.SenderID)
//
//	localVec := gm.versionManager.GetVersionVector()
//	missingUpdates := make([]Update, 0)
//
//	// Для каждой ноды в локальном version vector
//	for nodeID, localSeq := range localVec {
//		remoteSeq, exists := req.VersionVec[nodeID]
//
//		// Если у нас есть updates, которых нет у запрашивающей ноды
//		if !exists || localSeq > remoteSeq {
//			startSeq := remoteSeq + 1
//			if !exists {
//				startSeq = 0
//			}
//
//			// Пытаемся найти updates в буфере
//			ranges := []Range{{Start: startSeq, End: localSeq}}
//			updates := gm.deltaBuffer.GetByRanges(nodeID, ranges)
//
//			if len(updates) > 0 {
//				missingUpdates = append(missingUpdates, updates...)
//			} else {
//				// Updates не найдены в буфере
//				// В реальной системе здесь может быть запрос к persistence layer
//				slog.Warn("Updates not found in buffer",
//					"node_id", nodeID,
//					"range", ranges,
//				)
//			}
//		}
//	}
//
//	resp := &AntiEntropyResponse{
//		SenderID:       gm.nodeID,
//		MissingUpdates: missingUpdates,
//		VersionVec:     localVec,
//		HasMore:        false, // TODO: реализовать пагинацию для больших объемов
//	}
//
//	slog.Info("Sending anti-entropy response",
//		"sender", req.SenderID,
//		"missing_count", len(missingUpdates),
//	)
//
//	return resp, nil
//}
//
//// HandleMissedRequest обрабатывает запрос конкретных пропущенных updates
//func (gm *GossipManager) HandleMissedRequest(ctx context.Context, req *MissedUpdateRequest) (*MissedUpdateResponse, error) {
//	slog.Info("Received missed updates request",
//		"sender", req.SenderID,
//		"node_id", req.NodeID,
//		"ranges", req.Ranges,
//	)
//
//	gm.updatePeerLastSeen(req.SenderID)
//
//	// Ищем запрошенные updates в буфере
//	updates := gm.deltaBuffer.GetByRanges(req.NodeID, req.Ranges)
//
//	// Определяем какие ID не найдены
//	foundSet := make(map[int64]bool)
//	for _, update := range updates {
//		if len(update.Version.Sequence) > 0 {
//			for _, seq := range update.Version.Sequence {
//				foundSet[seq] = true
//			}
//		}
//	}
//
//	missing := make([]int64, 0)
//	for _, r := range req.Ranges {
//		for seq := r.Start; seq <= r.End; seq++ {
//			if !foundSet[seq] {
//				missing = append(missing, seq)
//			}
//		}
//	}
//
//	resp := &MissedUpdateResponse{
//		SenderID: gm.nodeID,
//		NodeID:   req.NodeID,
//		Updates:  updates,
//		Found:    len(updates),
//		Missing:  missing,
//	}
//
//	if len(missing) > 0 {
//		slog.Warn("Some requested updates not found",
//			"sender", req.SenderID,
//			"node_id", req.NodeID,
//			"missing_count", len(missing),
//		)
//	}
//
//	slog.Info("Sending missed updates response",
//		"sender", req.SenderID,
//		"found", len(updates),
//		"missing", len(missing),
//	)
//
//	return resp, nil
//}
//
//// HandlePing обрабатывает ping запрос
//func (gm *GossipManager) HandlePing(ctx context.Context, req *PingRequest) (*PingResponse, error) {
//	gm.updatePeerLastSeen(req.SenderID)
//
//	resp := &PingResponse{
//		SenderID:   gm.nodeID,
//		Timestamp:  gm.versionManager.engine.clock.Now(),
//		VersionVec: gm.versionManager.GetVersionVector(),
//	}
//
//	return resp, nil
//}
