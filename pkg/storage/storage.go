package storage

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"in-memorydb/pkg/config"
	"in-memorydb/pkg/crdt"
	engine "in-memorydb/pkg/storage/engine"
	"in-memorydb/pkg/storage/gossip"
	gossipimpl "in-memorydb/pkg/storage/gossip/gossip"
	"in-memorydb/pkg/storage/transport/grpc"
	"in-memorydb/pkg/storage/transport/grpc/transportpb"
	"in-memorydb/pkg/storage/types"
	"in-memorydb/pkg/storage/updates_buffer"
	bufferimpl "in-memorydb/pkg/storage/updates_buffer/new"
	"in-memorydb/pkg/storage/version_manager"
	"in-memorydb/pkg/storage/wal"
	walimpl "in-memorydb/pkg/storage/wal/poc"
	"in-memorydb/pkg/structs"
	"log/slog"
	"net"
	"time"

	"github.com/hashicorp/memberlist"
	grpcserver "google.golang.org/grpc"
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
	memberlist *memberlist.Memberlist          // controls membership
	wal        wal.WAL                         // write-ahead-log

	markedForDelete *list.List
	markChan        chan listEl

	updatesChan chan<- []*types.Update
	shutdown    context.CancelFunc
}

func NewStorage(config *config.Config) (*Storage, error) {

	eng := engine.NewEngine(256, config.Node.ID) // TODO initial shards value from config
	vm := version_manager.NewVersionManager(config.Node.ID, eng)
	transport := grpc.NewGRPCTransport(&config.Transport)

	memConfig := memberlist.DefaultLocalConfig()
	memConfig.Name = config.Node.ID
	memConfig.BindAddr = config.Node.BindAddress

	memList, err := memberlist.Create(memConfig)

	if err != nil {
		return nil, err
	}

	if len(config.Seeds) > 0 {
		_, err = memList.Join(config.Seeds)
		if err != nil {
			n := memList.LocalNode()
			slog.Warn("cannot join cluster, choosing standalone mode", "known seeds", config.Seeds, "node", n)
		}
	}

	goss := gossipimpl.NewDefaultGossip(&config.Gossip, transport, memList, vm) // TODO choose transport

	writeLog, err := walimpl.Open(config.Persistence.WalDir)
	if err != nil {
		return nil, err
	}

	buffer := bufferimpl.NewBuffer(1000) // TODO прокидывание из конфига
	marked := list.New()

	return &Storage{
		config:          config,
		engine:          eng,
		gossip:          goss,
		vm:              vm,
		memberlist:      memList,
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

	s.updatesChan = s.gossip.Start(ctx)
	s.startBufferRead(ctx)
	s.markedGC(ctx)

	if err := s.listenUpdates(ctx); err != nil {
		return err
	}

	return nil
}

// TODO нужно поправить VM
func (s *Storage) restoreFromWAL(ctx context.Context) error {
	slog.Info("restoring data from WAL")
	err := s.vm.RestoreFromWal(ctx, s.wal)
	if err != nil {
		return err
	}
	slog.Info("data restored successfully", "vectorClock", s.vm.VectorClockContiguous())
	return nil
}

func (s *Storage) GracefulStop() error {
	s.shutdown()
	_ = s.gossip.Shutdown()
	_ = s.wal.Close()
	time.Sleep(time.Second)
	return nil
}

func (s *Storage) listenUpdates(ctx context.Context) error {
	lis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", s.config.Node.BindAddress, s.config.Gossip.Port))
	if err != nil {
		return fmt.Errorf("cannot listen updates on port %d: %w", s.config.Gossip.Port, err)
	}

	updatesServer := grpc.NewUpdatesServer(s.buffer, s.wal, s.vm)
	serv := grpcserver.NewServer()
	transportpb.RegisterUpdatesServer(serv, updatesServer)
	go func() {
		if err := serv.Serve(lis); err != nil {
			slog.ErrorContext(ctx, "failed to serve", "port", s.config.Gossip.Port, "err", err)
			return
		}
	}()

	go func() {
		select {
		case <-ctx.Done():
			slog.Info("shutting down gossip receiver")
		}
	}()

	slog.InfoContext(ctx, "listening gossip updates", "port", s.config.Gossip.Port)

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
					slog.Error("error while reading buffered updates", "error", err)
				}
			case <-ctx.Done():
				slog.Info("shutting down buffer read")
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
		s.buffer.RemoveN(len(upds))
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
				slog.Info("shutting down garbage collection")
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
						s.engine.Delete(v.key)
					} else {
						v.Mu.RUnlock()
					}

				}
			}
		}
	}()
}

// нужно извлекать значение из crdt типа под мьютексом, отдавать crdt дальше не стоит, хоть они и потоко-безопасны, но от этого наверное нужно избавиться
func (s *Storage) Get(key string) (val any, t crdt.CRDTType, ok bool) {
	entry, ok := s.engine.Get(key)
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
	return val, entry.Object.Type(), true
}

// TODO выбрать в зависимости от политики когда возвращать результат, и что делать асинхронно
// сейчас для тестов ответ приходит после всех операций
func (s *Storage) Put(key string, t crdt.CRDTType) error {
	nodeID := s.config.Node.ID
	val, err := fabric.New(t, nodeID)
	if err != nil {
		return err
	}

	nilDelta, err := fabric.NilDelta(t)
	if err != nil {
		return err
	}

	// на этом этапе уже можно возвращать результат пользователю, остальное делать в другой горутине (worker pool чтобы не грузить)

	// put value
	ts := s.engine.Put(key, val)

	// increase sequence num
	seqNum := s.vm.Advance()

	update := &types.Update{
		NodeID:    nodeID,
		Type:      types.UpdateTypeSet,
		TimeStamp: ts,
		Range:     structs.Range{Start: seqNum, End: seqNum},
		Key:       key,
		Payload:   nilDelta, // since there is no data
	}

	s.buffer.Put(update)

	err = s.wal.Append(update)
	if err != nil {
		slog.Error("cannot append update to wal", "err", err)
		return ErrInternal
	}

	return nil
}

// TODO можно ускорить не дожидаясь ответа
func (s *Storage) Delete(key string) (bool, error) {
	entry, ok := s.engine.MarkDeleted(key)

	if ok {

		seqNum := s.vm.Advance()

		update := &types.Update{
			NodeID:    s.config.Node.ID,
			Type:      types.UpdateTypeDelete,
			TimeStamp: entry.LastUpdated,
			Range:     structs.Range{Start: seqNum, End: seqNum},
			Key:       key,
			Payload:   nil, // since its delete update, no data specified
		}

		s.buffer.Put(update)

		if err := s.wal.Append(update); err != nil {
			slog.Error("cannot append update to wal", "err", err)
			return false, ErrInternal
		}

		s.markChan <- listEl{key, entry}

	}

	return ok, nil
}

func (s *Storage) ApplyInc(key string, val int64) (bool, error) {
	entry, ok := s.engine.Get(key)
	if !ok {
		return false, nil
	}

	entry.Mu.Lock()
	defer entry.Mu.Unlock()
	if entry.Tombstone {
		return false, nil
	}
	switch t := entry.Object.(type) {
	case *crdt.PNCounter:

		seqNum := s.vm.Advance()

		delta := t.Increment(val)

		upd := &types.Update{
			NodeID:    s.config.Node.ID,
			Type:      types.UpdateTypeDelta,
			TimeStamp: s.engine.Clock().Now(),
			Payload:   delta,
			Range:     structs.Range{Start: seqNum, End: seqNum},
			Key:       key,
		}

		err := s.wal.Append(upd)

		if err != nil {
			slog.Error("cannot append update to wal", "err", err)
		}

		s.buffer.Put(upd)
	default:
		return false, fmt.Errorf("unexpected type for increment, expected: crdt.PNCounter, got: %T", entry.Object)
	}

	return true, nil
}

func (s *Storage) ApplyDec(key string, val int64) (bool, error) {
	entry, ok := s.engine.Get(key)
	if !ok {
		return false, nil
	}
	entry.Mu.Lock()
	defer entry.Mu.Unlock()
	if entry.Tombstone {
		return false, nil
	}
	switch t := entry.Object.(type) {
	case *crdt.PNCounter:
		seqNum := s.vm.Advance()
		delta := t.Decrement(val)

		upd := &types.Update{
			NodeID:    s.config.Node.ID,
			Type:      types.UpdateTypeDelta,
			TimeStamp: s.engine.Clock().Now(),
			Payload:   delta,
			Range:     structs.Range{Start: seqNum, End: seqNum},
			Key:       key,
		}

		err := s.wal.Append(upd)
		if err != nil {
			slog.Error("cannot append update to wal", "err", err)
		}

		s.buffer.Put(upd)
	default:
		return false, fmt.Errorf("unexpected type for increment, expected: crdt.PNCounter, got: %T", entry.Object)
	}
	return true, nil
}

func (s *Storage) ApplySetRegister(key string, val []byte) (bool, error) {
	entry, ok := s.engine.Get(key)
	if !ok {
		return false, nil
	}

	entry.Mu.Lock()
	defer entry.Mu.Unlock()
	if entry.Tombstone {
		return false, nil
	}
	switch t := entry.Object.(type) {
	case *crdt.LWWHLCRegister:
		seqNum := s.vm.Advance()

		delta := t.Write(val)

		upd := &types.Update{
			NodeID:    s.config.Node.ID,
			Type:      types.UpdateTypeDelta,
			TimeStamp: s.engine.Clock().Now(),
			Payload:   delta,
			Range:     structs.Range{Start: seqNum, End: seqNum},
			Key:       key,
		}

		err := s.wal.Append(upd)

		if err != nil {
			slog.Error("cannot append update to wal", "err", err) // TODO в этом случае нужно делать декремент seq_num, но такой ситуации не должно быть
			return false, ErrInternal
		}

		s.buffer.Put(upd)

	default:
		return false, fmt.Errorf("unexpected type for increment, expected: crdt.PNCounter, got: %T", entry.Object)
	}
	return true, nil
}

// TODO операции над crdt типами
