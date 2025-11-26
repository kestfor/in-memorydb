package trashed

//
//import (
//	"context"
//	"errors"
//)
//
//var (
//	// ErrTransportClosed возникает при попытке использовать закрытый транспорт
//	ErrTransportClosed = errors.New("transport is closed")
//
//	// ErrPeerNotFound возникает когда указанный peer не найден
//	ErrPeerNotFound = errors.New("peer not found")
//
//	// ErrTimeout возникает при превышении таймаута операции
//	ErrTimeout = errors.New("operation timeout")
//
//	// ErrSendFailed возникает при ошибке отправки сообщения
//	ErrSendFailed = errors.New("failed to send message")
//)
//
//// Transport определяет интерфейс для сетевого взаимодействия между нодами
//// Абстрагирует детали сетевого протокола (TCP, UDP, gRPC, etc)
//type Transport interface {
//	// Send отправляет сообщение указанной ноде асинхронно (fire and forget)
//	// Возвращает ошибку только если не удалось инициировать отправку
//	Send(ctx context.Context, nodeID string, msg *MessageEnvelope) error
//
//	// Request отправляет запрос и ожидает ответ
//	// Блокирующая операция с таймаутом из context
//	Request(ctx context.Context, nodeID string, req *MessageEnvelope) (*MessageEnvelope, error)
//
//	// Broadcast отправляет сообщение всем указанным нодам параллельно
//	// Возвращает map с результатами (успех/ошибка) для каждой ноды
//	Broadcast(ctx context.Context, nodeIDs []string, msg *MessageEnvelope) map[string]error
//
//	// RegisterHandler регистрирует обработчик входящих сообщений
//	// Может быть зарегистрирован только один обработчик
//	RegisterHandler(handler MessageHandler)
//
//	// GetPeers возвращает список всех известных peers
//	GetPeers() []PeerInfo
//
//	// AddPeer добавляет новый peer в список известных нод
//	AddPeer(info PeerInfo) error
//
//	// RemovePeer удаляет peer из списка
//	RemovePeer(nodeID string) error
//
//	// UpdatePeerStatus обновляет статус peer-ноды
//	UpdatePeerStatus(nodeID string, status PeerStatus) error
//
//	// Start запускает транспортный слой
//	Start(ctx context.Context) error
//
//	// Stop останавливает транспортный слой
//	Stop() error
//
//	// LocalAddr возвращает локальный адрес транспорта
//	LocalAddr() string
//
//	// IsHealthy проверяет работоспособность транспорта
//	IsHealthy() bool
//}
//
//// MessageHandler определяет интерфейс для обработки входящих сообщений
//// Реализуется компонентом GossipManager
//type MessageHandler interface {
//	// HandleMessage обрабатывает входящее сообщение любого типа
//	// Возвращает ответное сообщение (если требуется) и ошибку
//	HandleMessage(ctx context.Context, envelope *MessageEnvelope) (*MessageEnvelope, error)
//
//	// HandleGossip обрабатывает gossip сообщение с updates
//	HandleGossip(ctx context.Context, msg *GossipMessage) error
//
//	// HandleAntiEntropy обрабатывает запрос anti-entropy синхронизации
//	HandleAntiEntropy(ctx context.Context, req *AntiEntropyRequest) (*AntiEntropyResponse, error)
//
//	// HandleMissedRequest обрабатывает запрос пропущенных updates
//	HandleMissedRequest(ctx context.Context, req *MissedUpdateRequest) (*MissedUpdateResponse, error)
//
//	// HandlePing обрабатывает ping запрос
//	HandlePing(ctx context.Context, req *PingRequest) (*PingResponse, error)
//}
//
//// TransportStats содержит статистику работы транспорта
//type TransportStats struct {
//	MessagesSent     int64 // всего отправлено сообщений
//	MessagesReceived int64 // всего получено сообщений
//	BytesSent        int64 // всего отправлено байт
//	BytesReceived    int64 // всего получено байт
//	Errors           int64 // количество ошибок
//	ActivePeers      int   // количество активных peers
//	AvgLatencyMs     int64 // средняя задержка в миллисекундах
//}
//
//// TransportConfig содержит конфигурацию транспортного слоя
//type TransportConfig struct {
//	// ListenAddr - адрес для прослушивания входящих соединений
//	ListenAddr string
//
//	// MaxMessageSize - максимальный размер сообщения в байтах
//	MaxMessageSize int
//
//	// SendTimeout - таймаут отправки сообщения
//	SendTimeoutMs int
//
//	// RequestTimeout - таймаут ожидания ответа на запрос
//	RequestTimeoutMs int
//
//	// MaxConcurrentRequests - максимальное количество одновременных запросов
//	MaxConcurrentRequests int
//
//	// KeepAlive - интервал keep-alive для соединений
//	KeepAliveMs int
//
//	// EnableCompression - включить сжатие сообщений
//	EnableCompression bool
//
//	// EnableEncryption - включить шифрование (TLS)
//	EnableEncryption bool
//}
//
//// DefaultTransportConfig возвращает конфигурацию по умолчанию
//func DefaultTransportConfig() *TransportConfig {
//	return &TransportConfig{
//		ListenAddr:            ":8080",
//		MaxMessageSize:        10 * 1024 * 1024, // 10MB
//		SendTimeoutMs:         5000,              // 5 seconds
//		RequestTimeoutMs:      10000,             // 10 seconds
//		MaxConcurrentRequests: 1000,
//		KeepAliveMs:           30000,             // 30 seconds
//		EnableCompression:     true,
//		EnableEncryption:      false,
//	}
//}
