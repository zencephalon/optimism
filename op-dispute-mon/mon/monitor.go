package mon

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum-optimism/optimism/op-challenger/game/types"
	"github.com/ethereum-optimism/optimism/op-service/clock"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"
)

type Detect func(ctx context.Context, games []types.GameMetadata)
type Forecast func(ctx context.Context, games []types.GameMetadata)
type BlockHashFetcher func(ctx context.Context, number *big.Int) (common.Hash, error)
type BlockNumberFetcher func(ctx context.Context) (uint64, error)
type FactoryGameFetcher func(ctx context.Context, blockHash common.Hash, earliestTimestamp uint64) ([]types.GameMetadata, error)

type gameMonitor struct {
	logger log.Logger
	clock  clock.Clock

	done   chan struct{}
	ctx    context.Context
	cancel context.CancelFunc

	gameWindow      time.Duration
	monitorInterval time.Duration

	detect           Detect
	forecast         Forecast
	fetchGames       FactoryGameFetcher
	fetchBlockHash   BlockHashFetcher
	fetchBlockNumber BlockNumberFetcher
}

func newGameMonitor(
	ctx context.Context,
	logger log.Logger,
	cl clock.Clock,
	monitorInterval time.Duration,
	gameWindow time.Duration,
	detect Detect,
	forecast Forecast,
	factory FactoryGameFetcher,
	fetchBlockNumber BlockNumberFetcher,
	fetchBlockHash BlockHashFetcher,
) *gameMonitor {
	return &gameMonitor{
		logger:           logger,
		clock:            cl,
		ctx:              ctx,
		done:             make(chan struct{}),
		monitorInterval:  monitorInterval,
		gameWindow:       gameWindow,
		detect:           detect,
		forecast:         forecast,
		fetchGames:       factory,
		fetchBlockNumber: fetchBlockNumber,
		fetchBlockHash:   fetchBlockHash,
	}
}

func (m *gameMonitor) minGameTimestamp() uint64 {
	if m.gameWindow.Seconds() == 0 {
		return 0
	}
	// time: "To compute t-d for a duration d, use t.Add(-d)."
	// https://pkg.go.dev/time#Time.Sub
	if m.clock.Now().Unix() > int64(m.gameWindow.Seconds()) {
		return uint64(m.clock.Now().Add(-m.gameWindow).Unix())
	}
	return 0
}

func (m *gameMonitor) monitorGames() error {
	blockNumber, err := m.fetchBlockNumber(m.ctx)
	if err != nil {
		return fmt.Errorf("Failed to fetch block number: %w", err)
	}
	m.logger.Debug("Fetched block number", "blockNumber", blockNumber)
	blockHash, err := m.fetchBlockHash(context.Background(), new(big.Int).SetUint64(blockNumber))
	if err != nil {
		return fmt.Errorf("Failed to fetch block hash: %w", err)
	}
	games, err := m.fetchGames(m.ctx, blockHash, m.minGameTimestamp())
	if err != nil {
		return fmt.Errorf("failed to load games: %w", err)
	}
	m.detect(m.ctx, games)
	m.forecast(m.ctx, games)
	return nil
}

func (m *gameMonitor) loop() {
	ticker := m.clock.NewTicker(m.monitorInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.Ch():
			if err := m.monitorGames(); err != nil {
				m.logger.Error("Failed to monitor games", "err", err)
			}
		case <-m.done:
			m.logger.Info("Stopping game monitor")
			return
		}
	}
}

func (m *gameMonitor) StartMonitoring() {
	// Setup the cancellation only if it's not already set.
	// This prevents overwriting the context and cancel function
	// if, for example, this function is called multiple times.
	if m.cancel == nil {
		ctx, cancel := context.WithCancel(m.ctx)
		m.ctx = ctx
		m.cancel = cancel
	}
	m.logger.Info("Starting game monitor")
	go m.loop()
}

func (m *gameMonitor) StopMonitoring() {
	m.logger.Info("Stopping game monitor")
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	close(m.done)
}
