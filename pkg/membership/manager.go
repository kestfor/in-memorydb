package membership

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/hashicorp/memberlist"
)

// NodeStatus представляет статус ноды в кластере
type NodeStatus int

const (
	NodeAlive NodeStatus = iota
	NodeSuspected
	NodeLeft
	NodeDead
)

func (ns NodeStatus) String() string {
	switch ns {
	case NodeAlive:
		return "alive"
	case NodeSuspected:
		return "suspected"
	case NodeLeft:
		return "left"
	case NodeDead:
		return "dead"
	default:
		return "unknown"
	}
}

// Node представляет информацию о ноде в кластере
type Node struct {
	ID       string     // уникальный ID ноды
	Address  string     // адрес для gRPC (host:port)
	Status   NodeStatus // текущий статус
	Metadata []byte     // дополнительные метаданные
}

// EventType тип события членства
type EventType int

const (
	EventJoin EventType = iota
	EventLeave
	EventUpdate
	EventFailed
)

// Event событие изменения членства
type Event struct {
	Type EventType
	Node *Node
}

// EventHandler обработчик событий членства
type EventHandler interface {
	HandleEvent(event Event)
}

// MembershipConfig конфигурация membership manager
type MembershipConfig struct {
	NodeName       string        // имя текущей ноды
	BindAddr       string        // адрес для SWIM протокола (host:port)
	AdvertiseAddr  string        // адрес для анонсирования
	GRPCAddr       string        // адрес для gRPC в метаданных
	SeedNodes      []string      // начальные ноды для присоединения
	SecretKey      []byte        // ключ для шифрования (опционально)
	RetransmitMult int           // множитель ретрансмиссии
	ProbeInterval  time.Duration // интервал проверки нод
	ProbeTimeout   time.Duration // таймаут проверки
}

// DefaultMembershipConfig возвращает конфигурацию по умолчанию
func DefaultMembershipConfig(nodeName, bindAddr, grpcAddr string) *MembershipConfig {
	return &MembershipConfig{
		NodeName:       nodeName,
		BindAddr:       bindAddr,
		AdvertiseAddr:  bindAddr,
		GRPCAddr:       grpcAddr,
		SeedNodes:      []string{},
		RetransmitMult: 4,
		ProbeInterval:  1 * time.Second,
		ProbeTimeout:   500 * time.Millisecond,
	}
}

// Manager управляет членством в кластере через SWIM протокол
type Manager struct {
	config     *MembershipConfig
	memberlist *memberlist.Memberlist
	delegate   *delegate
	eventCh    chan Event
	handlers   []EventHandler
	handlersMu sync.RWMutex

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Кеш активных нод
	nodes   map[string]*Node
	nodesMu sync.RWMutex
}

// NewManager создает новый membership manager
func NewManager(config *MembershipConfig) (*Manager, error) {
	if config == nil {
		return nil, fmt.Errorf("config is required")
	}

	m := &Manager{
		config:   config,
		eventCh:  make(chan Event, 100),
		handlers: make([]EventHandler, 0),
		nodes:    make(map[string]*Node),
	}

	// Создаем delegate для обработки событий memberlist
	m.delegate = &delegate{
		manager: m,
		meta:    []byte(config.GRPCAddr), // сохраняем gRPC адрес в метаданных
	}

	// Настраиваем memberlist
	mlConfig := memberlist.DefaultLANConfig()
	mlConfig.Name = config.NodeName
	mlConfig.BindAddr = config.BindAddr
	if config.AdvertiseAddr != "" {
		mlConfig.AdvertiseAddr = config.AdvertiseAddr
	}
	mlConfig.ProbeInterval = config.ProbeInterval
	mlConfig.ProbeTimeout = config.ProbeTimeout
	mlConfig.RetransmitMult = config.RetransmitMult
	mlConfig.Delegate = m.delegate
	mlConfig.Events = m.delegate

	// Отключаем встроенное логирование memberlist
	mlConfig.LogOutput = nil

	// Если есть секретный ключ, включаем шифрование
	if len(config.SecretKey) > 0 {
		mlConfig.SecretKey = config.SecretKey
	}

	// Создаем memberlist
	ml, err := memberlist.Create(mlConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create memberlist: %w", err)
	}

	m.memberlist = ml

	// Добавляем себя в nodes
	m.nodes[config.NodeName] = &Node{
		ID:       config.NodeName,
		Address:  config.GRPCAddr,
		Status:   NodeAlive,
		Metadata: m.delegate.meta,
	}

	return m, nil
}

// Start запускает membership manager
func (m *Manager) Start(ctx context.Context) error {
	m.ctx, m.cancel = context.WithCancel(ctx)

	// Присоединяемся к существующим нодам
	if len(m.config.SeedNodes) > 0 {
		n, err := m.memberlist.Join(m.config.SeedNodes)
		if err != nil {
			slog.Warn("Failed to join some seed nodes",
				"joined", n,
				"error", err,
			)
		} else {
			slog.Info("Joined cluster",
				"node_name", m.config.NodeName,
				"seed_nodes", m.config.SeedNodes,
				"joined_count", n,
			)
		}
	}

	// Запускаем обработчик событий
	m.wg.Add(1)
	go m.eventLoop()

	slog.Info("Membership manager started",
		"node_name", m.config.NodeName,
		"bind_addr", m.config.BindAddr,
		"grpc_addr", m.config.GRPCAddr,
	)

	return nil
}

// Stop останавливает membership manager
func (m *Manager) Stop() error {
	if m.cancel != nil {
		m.cancel()
	}

	// Покидаем кластер
	if err := m.memberlist.Leave(5 * time.Second); err != nil {
		slog.Error("Failed to leave cluster gracefully", "error", err)
	}

	// Останавливаем memberlist
	if err := m.memberlist.Shutdown(); err != nil {
		slog.Error("Failed to shutdown memberlist", "error", err)
	}

	m.wg.Wait()
	close(m.eventCh)

	slog.Info("Membership manager stopped")
	return nil
}

// eventLoop обрабатывает события членства
func (m *Manager) eventLoop() {
	defer m.wg.Done()

	for {
		select {
		case <-m.ctx.Done():
			return
		case event := <-m.eventCh:
			// Обновляем локальный кеш нод
			m.updateNodeCache(event)

			// Уведомляем всех зарегистрированных обработчиков
			m.handlersMu.RLock()
			for _, handler := range m.handlers {
				handler.HandleEvent(event)
			}
			m.handlersMu.RUnlock()
		}
	}
}

// updateNodeCache обновляет кеш нод на основе события
func (m *Manager) updateNodeCache(event Event) {
	m.nodesMu.Lock()
	defer m.nodesMu.Unlock()

	switch event.Type {
	case EventJoin:
		m.nodes[event.Node.ID] = event.Node
		slog.Info("Node joined",
			"node_id", event.Node.ID,
			"address", event.Node.Address,
		)

	case EventLeave:
		if node, exists := m.nodes[event.Node.ID]; exists {
			node.Status = NodeLeft
		}
		slog.Info("Node left",
			"node_id", event.Node.ID,
		)

	case EventUpdate:
		if node, exists := m.nodes[event.Node.ID]; exists {
			node.Metadata = event.Node.Metadata
		}
		slog.Debug("Node updated",
			"node_id", event.Node.ID,
		)

	case EventFailed:
		if node, exists := m.nodes[event.Node.ID]; exists {
			node.Status = NodeDead
		}
		slog.Warn("Node failed",
			"node_id", event.Node.ID,
		)
	}
}

// RegisterEventHandler регистрирует обработчик событий членства
func (m *Manager) RegisterEventHandler(handler EventHandler) {
	m.handlersMu.Lock()
	defer m.handlersMu.Unlock()
	m.handlers = append(m.handlers, handler)
}

// GetMembers возвращает список всех живых членов кластера
func (m *Manager) GetMembers() []*Node {
	m.nodesMu.RLock()
	defer m.nodesMu.RUnlock()

	members := make([]*Node, 0, len(m.nodes))
	for _, node := range m.nodes {
		if node.Status == NodeAlive {
			members = append(members, &Node{
				ID:       node.ID,
				Address:  node.Address,
				Status:   node.Status,
				Metadata: node.Metadata,
			})
		}
	}

	return members
}

// GetMember возвращает информацию о конкретной ноде
func (m *Manager) GetMember(nodeID string) (*Node, bool) {
	m.nodesMu.RLock()
	defer m.nodesMu.RUnlock()

	node, exists := m.nodes[nodeID]
	if !exists {
		return nil, false
	}

	return &Node{
		ID:       node.ID,
		Address:  node.Address,
		Status:   node.Status,
		Metadata: node.Metadata,
	}, true
}

// GetAliveMembers возвращает только активные ноды
func (m *Manager) GetAliveMembers() []*Node {
	return m.GetMembers() // уже фильтрует только alive
}

// GetMemberCount возвращает количество активных членов
func (m *Manager) GetMemberCount() int {
	return len(m.GetMembers())
}

// LocalNode возвращает информацию о локальной ноде
func (m *Manager) LocalNode() *Node {
	m.nodesMu.RLock()
	defer m.nodesMu.RUnlock()

	node, _ := m.nodes[m.config.NodeName]
	return node
}

// IsHealthy проверяет здоровье memberlist
func (m *Manager) IsHealthy() bool {
	return m.memberlist.NumMembers() > 0
}

// GetStats возвращает статистику memberlist
func (m *Manager) GetStats() map[string]interface{} {
	return map[string]interface{}{
		"num_members": m.memberlist.NumMembers(),
		"alive_count": len(m.GetMembers()),
		"local_node":  m.config.NodeName,
	}
}
