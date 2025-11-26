package trashed

//
//import (
//	"encoding/json"
//	"in-memorydb/pkg/crdt"
//	"time"
//)
//
//// MessageType определяет тип сообщения в gossip протоколе
//type MessageType string
//
//const (
//	MessageTypeGossip             MessageType = "gossip"
//	MessageTypeAntiEntropyRequest MessageType = "anti_entropy_req"
//	MessageTypeAntiEntropyResponse MessageType = "anti_entropy_resp"
//	MessageTypeMissedRequest      MessageType = "missed_req"
//	MessageTypeMissedResponse     MessageType = "missed_resp"
//)
//
//// GossipMessage - основное сообщение для передачи updates через gossip
//// Содержит актуальные дельты и информацию о версиях отправителя
//type GossipMessage struct {
//	SenderID   string              `json:"sender_id"`   // ID ноды-отправителя
//	Timestamp  *crdt.Timestamp     `json:"timestamp"`   // HLC timestamp сообщения
//	Deltas     []Update            `json:"deltas"`      // массив updates для передачи
//	VersionVec map[string]int64    `json:"version_vec"` // version vector отправителя
//}
//
//// MarshalJSON сериализует GossipMessage
//func (gm *GossipMessage) MarshalJSON() ([]byte, error) {
//	// Сериализуем дельты отдельно
//	serializedDeltas := make([]json.RawMessage, len(gm.Deltas))
//	for i, update := range gm.Deltas {
//		deltaJSON, err := update.Delta.MarshalJSON()
//		if err != nil {
//			return nil, err
//		}
//
//		updateData := map[string]interface{}{
//			"key":       update.Key,
//			"delta":     json.RawMessage(deltaJSON),
//			"version":   update.Version,
//			"timestamp": update.Timestamp,
//		}
//
//		data, err := json.Marshal(updateData)
//		if err != nil {
//			return nil, err
//		}
//		serializedDeltas[i] = data
//	}
//
//	tmp := struct {
//		SenderID   string              `json:"sender_id"`
//		Timestamp  *crdt.Timestamp     `json:"timestamp"`
//		Deltas     []json.RawMessage   `json:"deltas"`
//		VersionVec map[string]int64    `json:"version_vec"`
//	}{
//		SenderID:   gm.SenderID,
//		Timestamp:  gm.Timestamp,
//		Deltas:     serializedDeltas,
//		VersionVec: gm.VersionVec,
//	}
//
//	return json.Marshal(tmp)
//}
//
//// AntiEntropyRequest - запрос полной синхронизации состояния
//// Отправляется периодически для обнаружения и устранения расхождений
//type AntiEntropyRequest struct {
//	SenderID   string           `json:"sender_id"`   // ID ноды-отправителя
//	VersionVec map[string]int64 `json:"version_vec"` // version vector отправителя
//	Timestamp  *crdt.Timestamp  `json:"timestamp"`   // timestamp запроса
//}
//
//// AntiEntropyResponse - ответ с пропущенными updates
//// Содержит все updates, которых нет у запрашивающей ноды
//type AntiEntropyResponse struct {
//	SenderID       string           `json:"sender_id"`        // ID ноды-отправителя ответа
//	MissingUpdates []Update         `json:"missing_updates"`  // массив пропущенных updates
//	VersionVec     map[string]int64 `json:"version_vec"`      // актуальный version vector
//	HasMore        bool             `json:"has_more"`         // есть ли еще updates (для пагинации)
//	ContinueFrom   map[string]int64 `json:"continue_from"`    // с какой версии продолжить
//}
//
//// MissedUpdateRequest - запрос конкретных пропущенных updates
//// Используется когда обнаружены пропуски в sequence numbers
//type MissedUpdateRequest struct {
//	SenderID  string           `json:"sender_id"`  // ID запрашивающей ноды
//	NodeID    string           `json:"node_id"`    // от какой ноды запрашиваем updates
//	Ranges    []Range          `json:"ranges"`     // диапазоны пропущенных sequence numbers
//	Timestamp *crdt.Timestamp  `json:"timestamp"`  // timestamp запроса
//}
//
//// MissedUpdateResponse - ответ с запрошенными updates
//type MissedUpdateResponse struct {
//	SenderID string           `json:"sender_id"` // ID ноды-отправителя ответа
//	NodeID   string           `json:"node_id"`   // для какой ноды эти updates
//	Updates  []Update         `json:"updates"`   // запрошенные updates
//	Found    int              `json:"found"`     // сколько updates найдено
//	Missing  []int64          `json:"missing"`   // какие ID не найдены
//}
//
//// PeerInfo содержит информацию о peer-ноде
//type PeerInfo struct {
//	NodeID      string       `json:"node_id"`      // уникальный ID ноды
//	Address     string       `json:"address"`      // сетевой адрес (host:port)
//	LastSeen    time.Time    `json:"last_seen"`    // время последнего контакта
//	Status      PeerStatus   `json:"status"`       // текущий статус ноды
//	Version     int64        `json:"version"`      // последняя известная версия
//	FailedPings int          `json:"failed_pings"` // счетчик неудачных ping
//}
//
//// PeerStatus представляет статус peer-ноды
//type PeerStatus int
//
//const (
//	StatusAlive PeerStatus = iota     // нода активна и отвечает
//	StatusSuspected                    // нода подозревается в недоступности
//	StatusDead                         // нода признана недоступной
//)
//
//func (ps PeerStatus) String() string {
//	switch ps {
//	case StatusAlive:
//		return "alive"
//	case StatusSuspected:
//		return "suspected"
//	case StatusDead:
//		return "dead"
//	default:
//		return "unknown"
//	}
//}
//
//// PingRequest - простой запрос для проверки доступности
//type PingRequest struct {
//	SenderID  string          `json:"sender_id"`
//	Timestamp *crdt.Timestamp `json:"timestamp"`
//}
//
//// PingResponse - ответ на ping с текущим состоянием
//type PingResponse struct {
//	SenderID   string           `json:"sender_id"`
//	Timestamp  *crdt.Timestamp  `json:"timestamp"`
//	VersionVec map[string]int64 `json:"version_vec"` // version vector для обнаружения расхождений
//}
//
//// MessageEnvelope - обертка для любого типа сообщения
//// Используется для маршрутизации и обработки разных типов сообщений
//type MessageEnvelope struct {
//	Type      MessageType     `json:"type"`
//	Payload   json.RawMessage `json:"payload"`
//	Timestamp *crdt.Timestamp `json:"timestamp"`
//}
//
//// NewMessageEnvelope создает envelope для указанного типа сообщения
//func NewMessageEnvelope(msgType MessageType, payload interface{}, timestamp *crdt.Timestamp) (*MessageEnvelope, error) {
//	data, err := json.Marshal(payload)
//	if err != nil {
//		return nil, err
//	}
//
//	return &MessageEnvelope{
//		Type:      msgType,
//		Payload:   data,
//		Timestamp: timestamp,
//	}, nil
//}
//
//// Unmarshal десериализует payload в указанную структуру
//func (me *MessageEnvelope) Unmarshal(v interface{}) error {
//	return json.Unmarshal(me.Payload, v)
//}
