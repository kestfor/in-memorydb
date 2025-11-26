package membership

import (
	"github.com/hashicorp/memberlist"
)

// delegate реализует интерфейсы memberlist.Delegate и memberlist.EventDelegate
// Обрабатывает события и метаданные от memberlist
type delegate struct {
	manager *Manager
	meta    []byte // метаданные локальной ноды (gRPC адрес)
}

// NodeMeta возвращает метаданные для локальной ноды
// Вызывается memberlist для получения метаданных, которые будут распространяться
func (d *delegate) NodeMeta(limit int) []byte {
	if len(d.meta) > limit {
		return d.meta[:limit]
	}
	return d.meta
}

// NotifyMsg вызывается когда получено пользовательское сообщение
// В нашем случае не используется, так как данные передаются через gRPC
func (d *delegate) NotifyMsg(msg []byte) {
	// Не используется - данные передаются через gRPC gossip
}

// GetBroadcasts возвращает сообщения для широковещательной передачи
// В нашем случае не используется
func (d *delegate) GetBroadcasts(overhead, limit int) [][]byte {
	// Не используется - данные передаются через gRPC gossip
	return nil
}

// LocalState возвращает локальное состояние для передачи другим нодам
// Вызывается при anti-entropy синхронизации memberlist
func (d *delegate) LocalState(join bool) []byte {
	// Возвращаем только метаданные (gRPC адрес)
	return d.meta
}

// MergeRemoteState объединяет удаленное состояние с локальным
// Вызывается при anti-entropy синхронизации memberlist
func (d *delegate) MergeRemoteState(buf []byte, join bool) {
	// В нашем случае состояние - это просто метаданные, их не нужно мержить
}

// NotifyJoin вызывается когда нода присоединяется к кластеру
func (d *delegate) NotifyJoin(node *memberlist.Node) {
	// Извлекаем gRPC адрес из метаданных
	grpcAddr := string(node.Meta)

	event := Event{
		Type: EventJoin,
		Node: &Node{
			ID:       node.Name,
			Address:  grpcAddr,
			Status:   NodeAlive,
			Metadata: node.Meta,
		},
	}

	// Отправляем событие в канал
	select {
	case d.manager.eventCh <- event:
	default:
		// Канал переполнен, пропускаем событие
	}
}

// NotifyLeave вызывается когда нода покидает кластер
func (d *delegate) NotifyLeave(node *memberlist.Node) {
	grpcAddr := string(node.Meta)

	event := Event{
		Type: EventLeave,
		Node: &Node{
			ID:       node.Name,
			Address:  grpcAddr,
			Status:   NodeLeft,
			Metadata: node.Meta,
		},
	}

	select {
	case d.manager.eventCh <- event:
	default:
	}
}

// NotifyUpdate вызывается когда метаданные ноды обновляются
func (d *delegate) NotifyUpdate(node *memberlist.Node) {
	grpcAddr := string(node.Meta)

	event := Event{
		Type: EventUpdate,
		Node: &Node{
			ID:       node.Name,
			Address:  grpcAddr,
			Status:   NodeAlive,
			Metadata: node.Meta,
		},
	}

	select {
	case d.manager.eventCh <- event:
	default:
	}
}

// Проверяем что delegate реализует нужные интерфейсы
var _ memberlist.Delegate = (*delegate)(nil)
var _ memberlist.EventDelegate = (*delegate)(nil)
