package storage

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"in-memorydb/pkg/config"
	"in-memorydb/pkg/crdt"
	"in-memorydb/pkg/gossip"
	gossipimpl "in-memorydb/pkg/gossip/gossip"
	"in-memorydb/pkg/membership"
	membershipv1 "in-memorydb/pkg/membership/v1"
	"in-memorydb/pkg/observability/spans"
	"in-memorydb/pkg/observability/tracing"
	engine "in-memorydb/pkg/storage/engine"
	"in-memorydb/pkg/storage/updates_buffer"
	bufferimpl "in-memorydb/pkg/storage/updates_buffer/new"
	"in-memorydb/pkg/storage/version_manager"
	"in-memorydb/pkg/storage/wal"
	walimpl "in-memorydb/pkg/storage/wal/poc"
	"in-memorydb/pkg/structs"
	grpc2 "in-memorydb/pkg/transport/grpc"
	types2 "in-memorydb/pkg/types"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	codes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var ErrInternal = errors.New("internal error")

var fabric = crdt.NewFabric()

type listEl struct {
	key string
	*engine.CRDTEntry
}

type Storage struct {
	config *config.Config // config

	engine     *engine.Engine                  // controls key-value mapping, sharding
	vm         *version_manager.VersionManager // controls current version of data
	gossip     gossip.Gossip                   // controls data transfer between nodes
	buffer     updates_buffer.UpdatesBuffer    // for efficient data transfer
	memberlist membership.Membership           // controls membership
	wal        wal.WAL                         // write-ahead-log

	markedForDelete *list.List
	markChan        chan listEl

	updatesChan chan<- []*types2.Update
	shutdown    context.CancelFunc
}

func NewStorage(config *config.Config) (*Storage, error) {

	eng := engine.NewEngine(256, config.Node.ID) // TODO initial shards value from config
	vm := version_manager.NewVersionManager(config.Node.ID, eng)
	transport := grpc2.NewGRPCTransport(&config.Transport)

	members, err := membershipv1.New(membershipv1.ConfigFromGlobal(config))

	if err != nil {
		return nil, err
	}

	writeLog, err := walimpl.New(config.Persistence.WalConfig)
	if err != nil {
		return nil, err
	}

	buffer := bufferimpl.NewBuffer(1000)                                                          // TODO прокидывание из конфига
	goss := gossipimpl.NewDefaultGossip(&config.Gossip, transport, members, vm, writeLog, buffer) // TODO choose transport

	marked := list.New()

	return &Storage{
		config:          config,
		engine:          eng,
		gossip:          goss,
		vm:              vm,
		memberlist:      members,
		wal:             writeLog,
		buffer:          buffer,
		markedForDelete: marked,
		markChan:        make(chan listEl, 100), // TODO
	}, nil
}

func (s *Storage) StartUp(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	s.shutdown = cancel

	if err := s.restoreFromWAL(ctx); err != nil {
		return err
	}

	if len(s.config.Seeds) > 0 {
		err := s.memberlist.Join(s.config.Seeds)
		if err != nil || len(s.memberlist.Members()) == 1 {
			n := s.memberlist.LocalNode()
			slog.Debug("storage.NewStorage: cannot join cluster, choosing standalone mode", "known seeds", s.config.Seeds, "node", n)
		}
	}

	var err error
	s.updatesChan, err = s.gossip.Start(ctx)
	if err != nil {
		return err
	}

	s.startBufferRead(ctx)
	s.markedGC(ctx)

	return nil
}

// TODO нужно поправить VM
func (s *Storage) restoreFromWAL(ctx context.Context) error {
	slog.Info("storage.restoreFromWAL: restoring data from WAL")
	start := time.Now()
	err := s.vm.RestoreFromWal(ctx, s.wal)
	if err != nil {
		return err
	}
	slog.Info("storage.restoreFromWAL: data restored successfully", "vectorClock", s.vm.VectorClockContiguous(), "elapsed (sec)", time.Since(start).Seconds())
	return nil
}

func (s *Storage) GracefulStop() error {
	s.shutdown()
	_ = s.gossip.Shutdown()
	_ = s.wal.Close()
	time.Sleep(time.Second)
	return nil
}

func (s *Storage) startBufferRead(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Second * 1) // TODO
		for {
			select {
			case <-ticker.C:
				err := s.bufferReadRound()
				if err != nil {
					slog.Error("storage.startBufferRead: error while reading buffered updates", "error", err)
				}
			case <-ctx.Done():
				slog.Info("storage.startBufferRead: shutting down buffer read")
				close(s.updatesChan)
				return
			}
		}
	}()
}

func (s *Storage) bufferReadRound() error {
	upds := s.buffer.PeekN(100)
	if len(upds) > 0 {
		s.updatesChan <- upds
		s.buffer.RemoveN(len(upds)) // TODO сделать лучше, сейчас так, чтобы не гонять одинаковые апдейты
	}
	return nil
}

func (s *Storage) markedGC(ctx context.Context) {
	go func() {
		lastCheckedTime := time.Now()
		for {
			select {
			case key, ok := <-s.markChan:
				if !ok {
					return
				}
				s.markedForDelete.PushBack(key)

			case <-ctx.Done():
				slog.Info("storage.markedGC: shutting down garbage collection")
				return
			default:

				if time.Now().Sub(lastCheckedTime) < time.Second {
					continue
				}
				lastCheckedTime = time.Now()

				// проверяем первый (самый старый элемент если его время еще не пришло для остальных не пришло точно)
				for ent := s.markedForDelete.Front(); ent != nil; ent = ent.Next() {

					// TODO определить что ключ достаточно старый для удаления
					v := ent.Value.(listEl)
					v.Mu.RLock()
					if v.Tombstone && (time.Duration(s.engine.Clock().Now().WallTime-v.LastUpdated.WallTime) >= 3600*time.Second) {
						v.Mu.RUnlock()
						s.engine.Delete(ctx, v.key)
					} else {
						v.Mu.RUnlock()
					}

				}
			}
		}
	}()
}

// нужно извлекать значение из crdt типа под мьютексом, отдавать crdt дальше не стоит, хоть они и потоко-безопасны, но от этого наверное нужно избавиться
func (s *Storage) Get(ctx context.Context, key string) (val any, t crdt.CRDTType, ok bool) {
	ctx, span := tracing.StartSpan(ctx, spans.SpanGetKey, trace.WithAttributes(attribute.String("key", key)), trace.WithSpanKind(trace.SpanKindClient))
	defer span.End()

	entry, ok := s.engine.Get(ctx, key)
	if !ok {
		return nil, "", false
	}

	entry.Mu.Lock()
	defer entry.Mu.Unlock()

	// mark as deleted
	if entry.Tombstone {
		return nil, "", false
	}

	val = entry.Object.Value()
	span.SetAttributes(attribute.String("type", entry.Object.Type().String()))
	span.SetStatus(codes.Ok, "")
	return val, entry.Object.Type(), true
}

// TODO выбрать в зависимости от политики когда возвращать результат, и что делать асинхронно
// сейчас для тестов ответ приходит после всех операций
func (s *Storage) Put(ctx context.Context, key string, t crdt.CRDTType) error {
	ctx, span := tracing.StartSpan(ctx, spans.SpanSetKey, trace.WithAttributes(attribute.String("key", key), attribute.String("type", t.String())), trace.WithSpanKind(trace.SpanKindClient))
	defer span.End()

	nodeID := s.config.Node.ID
	val, err := fabric.New(t, nodeID)
	if err != nil {
		return tracing.RecordError(ctx, err)
	}

	nilDelta, err := fabric.NilDelta(t)
	if err != nil {
		return tracing.RecordError(ctx, err)
	}

	// на этом этапе уже можно возвращать результат пользователю, остальное делать в другой горутине (worker pool чтобы не грузить)

	// put value
	ts := s.engine.Put(ctx, key, val)

	// increase sequence num
	seqNum := s.vm.Advance()

	update := &types2.Update{
		NodeID:    nodeID,
		Type:      types2.UpdateTypeSet,
		TimeStamp: ts,
		Range:     structs.Range{Start: seqNum, End: seqNum},
		Key:       key,
		Payload:   nilDelta, // since there is no data
	}

	s.buffer.Put(update)

	if err = s.wal.Append(ctx, update); err != nil {
		slog.Error("storage.Put: cannot append update to wal", "err", err)
		return tracing.RecordError(ctx, ErrInternal)
	}

	span.SetStatus(codes.Ok, "")

	return nil
}

// TODO можно ускорить не дожидаясь ответа
func (s *Storage) Delete(ctx context.Context, key string) (bool, error) {
	ctx, span := tracing.StartSpan(ctx, spans.SpanDeleteKey, trace.WithAttributes(attribute.String("key", key)), trace.WithSpanKind(trace.SpanKindClient))
	defer span.End()
	entry, ok := s.engine.MarkDeleted(ctx, key)

	if ok {

		seqNum := s.vm.Advance()

		update := &types2.Update{
			NodeID:    s.config.Node.ID,
			Type:      types2.UpdateTypeDelete,
			TimeStamp: entry.LastUpdated,
			Range:     structs.Range{Start: seqNum, End: seqNum},
			Key:       key,
			Payload:   &crdt.PNCounterDelta{}, // since its delete update, no data specified
		}

		s.buffer.Put(update)

		if err := s.wal.Append(ctx, update); err != nil {
			slog.Error("storage.Delete: cannot append update to wal", "err", err)
			return false, tracing.RecordError(ctx, ErrInternal)
		}

		s.markChan <- listEl{key, entry}

	}

	return ok, nil
}

func (s *Storage) ApplyInc(ctx context.Context, key string, val int64) (bool, error) {
	ctx, span := tracing.StartSpan(ctx, spans.SpanApplyInc, trace.WithAttributes(attribute.String("key", key), attribute.Int64("value", val)))
	defer span.End()

	entry, ok := s.engine.Get(ctx, key)
	if !ok {
		return false, fmt.Errorf("key not found")
	}

	entry.Mu.Lock()
	defer entry.Mu.Unlock()
	if entry.Tombstone {
		return false, fmt.Errorf("key not found")
	}
	switch t := entry.Object.(type) {
	case *crdt.PNCounter:

		seqNum := s.vm.Advance()

		delta := t.Increment(val)

		upd := &types2.Update{
			NodeID:    s.config.Node.ID,
			Type:      types2.UpdateTypeDelta,
			TimeStamp: s.engine.Clock().Now(),
			Payload:   delta,
			Range:     structs.Range{Start: seqNum, End: seqNum},
			Key:       key,
		}

		if err := s.wal.Append(ctx, upd); err != nil {
			slog.Error("storage.ApplyInc: cannot append update to wal", "err", err)
			_ = tracing.RecordError(ctx, err)
		}

		s.buffer.Put(upd)
	default:
		return false, tracing.RecordError(ctx, fmt.Errorf("unexpected type for increment, expected: crdt.PNCounter, got: %T", entry.Object))
	}

	return true, nil
}

func (s *Storage) ApplyDec(ctx context.Context, key string, val int64) (bool, error) {
	ctx, span := tracing.StartSpan(ctx, spans.SpanApplyDec, trace.WithAttributes(attribute.String("key", key), attribute.Int64("value", val)))
	defer span.End()

	entry, ok := s.engine.Get(ctx, key)
	if !ok {
		return false, fmt.Errorf("key not found")
	}
	entry.Mu.Lock()
	defer entry.Mu.Unlock()
	if entry.Tombstone {
		return false, fmt.Errorf("key not found")
	}
	switch t := entry.Object.(type) {
	case *crdt.PNCounter:
		seqNum := s.vm.Advance()
		delta := t.Decrement(val)

		upd := &types2.Update{
			NodeID:    s.config.Node.ID,
			Type:      types2.UpdateTypeDelta,
			TimeStamp: s.engine.Clock().Now(),
			Payload:   delta,
			Range:     structs.Range{Start: seqNum, End: seqNum},
			Key:       key,
		}

		if err := s.wal.Append(ctx, upd); err != nil {
			slog.Error("storage.ApplyDec: cannot append update to wal", "err", err)
			_ = tracing.RecordError(ctx, err)
		}

		s.buffer.Put(upd)
	default:
		return false, tracing.RecordError(ctx, fmt.Errorf("unexpected type for increment, expected: crdt.PNCounter, got: %T", entry.Object))
	}
	return true, nil
}

func (s *Storage) ApplySetRegister(ctx context.Context, key string, val []byte) (bool, error) {
	ctx, span := tracing.StartSpan(ctx, spans.SpanApplySetRegister, trace.WithAttributes(attribute.String("key", key)))
	defer span.End()

	entry, ok := s.engine.Get(ctx, key)
	if !ok {
		return false, fmt.Errorf("key not found")
	}

	entry.Mu.Lock()
	defer entry.Mu.Unlock()
	if entry.Tombstone {
		return false, fmt.Errorf("key not found")
	}
	switch t := entry.Object.(type) {
	case *crdt.LWWHLCRegister:
		seqNum := s.vm.Advance()

		delta := t.Write(val)

		upd := &types2.Update{
			NodeID:    s.config.Node.ID,
			Type:      types2.UpdateTypeDelta,
			TimeStamp: s.engine.Clock().Now(),
			Payload:   delta,
			Range:     structs.Range{Start: seqNum, End: seqNum},
			Key:       key,
		}

		if err := s.wal.Append(ctx, upd); err != nil {
			slog.Error("storage.ApplySetRegister: cannot append update to wal", "err", err) // TODO в этом случае нужно делать декремент seq_num, но такой ситуации не должно быть
			return false, tracing.RecordError(ctx, ErrInternal)
		}

		s.buffer.Put(upd)

	default:
		return false, tracing.RecordError(ctx, fmt.Errorf("unexpected type for increment, expected: crdt.PNCounter, got: %T", entry.Object))
	}
	return true, nil
}
