package preprocess

import (
	"bytes"
	"github.com/ElrondNetwork/chain-go-sdk/integrationTests"
	"github.com/ElrondNetwork/elrond-go/process/block/preprocess"
	"sync"
	"time"

	"github.com/ElrondNetwork/elrond-go-core/core"
	"github.com/ElrondNetwork/elrond-go-core/core/atomic"
	"github.com/ElrondNetwork/elrond-go-core/core/check"
	"github.com/ElrondNetwork/elrond-go-core/data"
	"github.com/ElrondNetwork/elrond-go-core/data/block"
	"github.com/ElrondNetwork/elrond-go-core/hashing"
	"github.com/ElrondNetwork/elrond-go-core/marshal"
	logger "github.com/ElrondNetwork/elrond-go-logger"
	"github.com/ElrondNetwork/elrond-go/dataRetriever"
	"github.com/ElrondNetwork/elrond-go/process"
	"github.com/ElrondNetwork/elrond-go/sharding"
	"github.com/ElrondNetwork/elrond-go/state"
	"github.com/ElrondNetwork/elrond-go/storage"
)

var _ process.DataMarshalizer = (*transactions)(nil)
var _ process.PreProcessor = (*transactions)(nil)

var log = logger.GetOrCreate("process/block/preprocess")

// 200% bandwidth to allow 100% overshooting estimations
const selectionGasBandwidthIncreasePercent = 200

// 130% to allow 30% overshooting estimations for scheduled SC calls
const selectionGasBandwidthIncreaseScheduledPercent = 130

type accountTxsShards struct {
	accountsInfo map[string]*txShardInfo
	sync.RWMutex
}

// TODO: increase code coverage with unit test

type transactions struct {
	*basePreProcess
	chRcvAllTxs                    chan bool
	onRequestTransaction           func(shardID uint32, txHashes [][]byte)
	txsForCurrBlock                txsForBlock
	txPool                         dataRetriever.ShardedDataCacherNotifier
	storage                        dataRetriever.StorageService
	txProcessor                    process.TransactionProcessor
	orderedTxs                     map[string][]data.TransactionHandler
	orderedTxHashes                map[string][][]byte
	mutOrderedTxs                  sync.RWMutex
	blockTracker                   BlockTracker
	blockType                      block.Type
	accountTxsShards               accountTxsShards
	emptyAddress                   []byte
	scheduledMiniBlocksEnableEpoch uint32
	flagScheduledMiniBlocks        atomic.Flag
	txTypeHandler                  process.TxTypeHandler
	scheduledTxsExecutionHandler   process.ScheduledTxsExecutionHandler
	transactionsMain               *preprocess.transactions
}

// ArgsTransactionPreProcessor holds the arguments to create a txs pre processor
type ArgsTransactionPreProcessor struct {
	TxDataPool                                  dataRetriever.ShardedDataCacherNotifier
	Store                                       dataRetriever.StorageService
	Hasher                                      hashing.Hasher
	Marshalizer                                 marshal.Marshalizer
	TxProcessor                                 process.TransactionProcessor
	ShardCoordinator                            sharding.Coordinator
	Accounts                                    state.AccountsAdapter
	OnRequestTransaction                        func(shardID uint32, txHashes [][]byte)
	EconomicsFee                                process.FeeHandler
	GasHandler                                  process.GasHandler
	BlockTracker                                preprocess.BlockTracker
	BlockType                                   block.Type
	PubkeyConverter                             core.PubkeyConverter
	BlockSizeComputation                        preprocess.BlockSizeComputationHandler
	BalanceComputation                          preprocess.BalanceComputationHandler
	EpochNotifier                               process.EpochNotifier
	OptimizeGasUsedInCrossMiniBlocksEnableEpoch uint32
	FrontRunningProtectionEnableEpoch           uint32
	ScheduledMiniBlocksEnableEpoch              uint32
	TxTypeHandler                               process.TxTypeHandler
	ScheduledTxsExecutionHandler                process.ScheduledTxsExecutionHandler
	ProcessedMiniBlocksTracker                  process.ProcessedMiniBlocksTracker
}

// NewTransactionPreprocessor creates a new transaction preprocessor object
func NewTransactionPreprocessor(
	args ArgsTransactionPreProcessor,
) (*transactions, error) {
	if check.IfNil(args.Hasher) {
		return nil, process.ErrNilHasher
	}
	if check.IfNil(args.Marshalizer) {
		return nil, process.ErrNilMarshalizer
	}
	if check.IfNil(args.TxDataPool) {
		return nil, process.ErrNilTransactionPool
	}
	if check.IfNil(args.Store) {
		return nil, process.ErrNilTxStorage
	}
	if check.IfNil(args.TxProcessor) {
		return nil, process.ErrNilTxProcessor
	}
	if check.IfNil(args.ShardCoordinator) {
		return nil, process.ErrNilShardCoordinator
	}
	if check.IfNil(args.Accounts) {
		return nil, process.ErrNilAccountsAdapter
	}
	if args.OnRequestTransaction == nil {
		return nil, process.ErrNilRequestHandler
	}
	if check.IfNil(args.EconomicsFee) {
		return nil, process.ErrNilEconomicsFeeHandler
	}
	if check.IfNil(args.GasHandler) {
		return nil, process.ErrNilGasHandler
	}
	if check.IfNil(args.BlockTracker) {
		return nil, process.ErrNilBlockTracker
	}
	if check.IfNil(args.PubkeyConverter) {
		return nil, process.ErrNilPubkeyConverter
	}
	if check.IfNil(args.BlockSizeComputation) {
		return nil, process.ErrNilBlockSizeComputationHandler
	}
	if check.IfNil(args.BalanceComputation) {
		return nil, process.ErrNilBalanceComputationHandler
	}
	if check.IfNil(args.EpochNotifier) {
		return nil, process.ErrNilEpochNotifier
	}
	if check.IfNil(args.TxTypeHandler) {
		return nil, process.ErrNilTxTypeHandler
	}
	if check.IfNil(args.ScheduledTxsExecutionHandler) {
		return nil, process.ErrNilScheduledTxsExecutionHandler
	}
	if check.IfNil(args.ProcessedMiniBlocksTracker) {
		return nil, process.ErrNilProcessedMiniBlocksTracker
	}

	bpp := basePreProcess{
		hasher:      args.Hasher,
		marshalizer: args.Marshalizer,
		gasTracker: gasTracker{
			shardCoordinator: args.ShardCoordinator,
			gasHandler:       args.GasHandler,
			economicsFee:     args.EconomicsFee,
		},
		blockSizeComputation: args.BlockSizeComputation,
		balanceComputation:   args.BalanceComputation,
		accounts:             args.Accounts,
		pubkeyConverter:      args.PubkeyConverter,

		optimizeGasUsedInCrossMiniBlocksEnableEpoch: args.OptimizeGasUsedInCrossMiniBlocksEnableEpoch,
		frontRunningProtectionEnableEpoch:           args.FrontRunningProtectionEnableEpoch,
		processedMiniBlocksTracker:                  args.ProcessedMiniBlocksTracker,
	}

	txs := &transactions{
		basePreProcess:                 &bpp,
		storage:                        args.Store,
		txPool:                         args.TxDataPool,
		onRequestTransaction:           args.OnRequestTransaction,
		txProcessor:                    args.TxProcessor,
		blockTracker:                   args.BlockTracker,
		blockType:                      args.BlockType,
		scheduledMiniBlocksEnableEpoch: args.ScheduledMiniBlocksEnableEpoch,
		txTypeHandler:                  args.TxTypeHandler,
		scheduledTxsExecutionHandler:   args.ScheduledTxsExecutionHandler,
	}

	txs.chRcvAllTxs = make(chan bool)
	txs.txPool.RegisterOnAdded(txs.receivedTransaction)

	txs.txsForCurrBlock.txHashAndInfo = make(map[string]*txInfo)
	txs.orderedTxs = make(map[string][]data.TransactionHandler)
	txs.orderedTxHashes = make(map[string][][]byte)
	txs.accountTxsShards.accountsInfo = make(map[string]*txShardInfo)

	txs.emptyAddress = make([]byte, txs.pubkeyConverter.Len())

	log.Debug("transactions: enable epoch for optimize gas used in cross shard mini blocks", "epoch", txs.optimizeGasUsedInCrossMiniBlocksEnableEpoch)
	log.Debug("transactions: enable epoch for front running protection", "epoch", txs.frontRunningProtectionEnableEpoch)
	log.Debug("transactions: enable epoch for scheduled mini blocks", "epoch", txs.scheduledMiniBlocksEnableEpoch)

	args.EpochNotifier.RegisterNotifyHandler(txs)

	return txs, nil
}

// IsDataPrepared returns non error if all the requested transactions arrived and were saved into the pool
func (txs *transactions) IsDataPrepared(requestedTxs int, haveTime func() time.Duration) error {
	return txs.transactionsMain.IsDataPrepared(requestedTxs, haveTime)
}

// RemoveBlockDataFromPools removes transactions and miniblocks from associated pools
func (txs *transactions) RemoveBlockDataFromPools(body *block.Body, miniBlockPool storage.Cacher) error {
	return txs.transactionsMain.RemoveBlockDataFromPools(body, miniBlockPool)
}

// RemoveTxsFromPools removes transactions from associated pools
func (txs *transactions) RemoveTxsFromPools(body *block.Body) error {
	return txs.transactionsMain.RemoveTxsFromPools(body)
}

// RestoreBlockDataIntoPools restores the transactions and miniblocks to associated pools
func (txs *transactions) RestoreBlockDataIntoPools(
	body *block.Body,
	miniBlockPool storage.Cacher,
) (int, error) {
	return txs.transactionsMain.RestoreBlockDataIntoPools(body, miniBlockPool)
}

// ProcessBlockTransactions processes all the transaction from the block.Body, updates the state
func (txs *transactions) ProcessBlockTransactions(
	header data.HeaderHandler,
	body *block.Body,
	haveTime func() bool,
) error {
	return txs.processTxsFromMe(body, haveTime, header.GetPrevRandSeed())
}

func (txs *transactions) processTxsFromMe(
	body *block.Body,
	haveTime func() bool,
	randomness []byte,
) error {
	if check.IfNil(body) {
		return process.ErrNilBlockBody
	}

	isShardStuckFalse := func(uint32) bool {
		return false
	}
	isMaxBlockSizeReachedFalse := func(int, int) bool {
		return false
	}
	haveAdditionalTimeFalse := func() bool {
		return false
	}

	scheduledMiniBlocks, err := txs.transactionsMain.createAndProcessScheduledMiniBlocksFromMeAsValidator(
		body,
		haveTime,
		haveAdditionalTimeFalse,
		isShardStuckFalse,
		isMaxBlockSizeReachedFalse,
		make(map[string]struct{}),
		randomness,
	)
	if err != nil {
		return err
	}

	err = txs.verifyCreatedMiniblock(body, scheduledMiniBlocks)
	if err != nil {
		return err
	}

	return nil
}

// TODO: move this function to elrond-go and make it public
func (txs *transactions) verifyCreatedMiniblock(body *block.Body, createdMiniblocks block.MiniBlockSlice) error {
	receivedMiniBlocks := make(block.MiniBlockSlice, 0)
	for _, miniBlock := range body.MiniBlocks {
		if miniBlock.Type == block.InvalidBlock {
			continue
		}

		receivedMiniBlocks = append(receivedMiniBlocks, miniBlock)
	}

	receivedBodyHash, err := core.CalculateHash(txs.marshalizer, txs.hasher, &block.Body{MiniBlocks: receivedMiniBlocks})
	if err != nil {
		return err
	}

	calculatedBodyHash, err := core.CalculateHash(txs.marshalizer, txs.hasher, &block.Body{MiniBlocks: createdMiniblocks})
	if err != nil {
		return err
	}

	if !bytes.Equal(receivedBodyHash, calculatedBodyHash) {
		for _, mb := range receivedMiniBlocks {
			log.Debug("received miniblock", "type", mb.Type, "sender", mb.SenderShardID, "receiver", mb.ReceiverShardID, "numTxs", len(mb.TxHashes))
		}

		for _, mb := range createdMiniblocks {
			log.Debug("calculated miniblock", "type", mb.Type, "sender", mb.SenderShardID, "receiver", mb.ReceiverShardID, "numTxs", len(mb.TxHashes))
		}

		log.Debug("block body missmatch",
			"received body hash", receivedBodyHash,
			"calculated body hash", calculatedBodyHash)
		return process.ErrBlockBodyHashMismatch
	}
	return nil
}

// SaveTxsToStorage saves transactions from body into storage
func (txs *transactions) SaveTxsToStorage(body *block.Body) error {
	return txs.transactionsMain.SaveTxsToStorage(body)
}

// receivedTransaction is a call back function which is called when a new transaction
// is added in the transaction pool
func (txs *transactions) receivedTransaction(key []byte, value interface{}) {
	txs.transactionsMain.receivedTransaction(key, value)
}

// CreateBlockStarted cleans the local cache map for processed/created transactions at this round
func (txs *transactions) CreateBlockStarted() {
	txs.transactionsMain.CreateBlockStarted()
}

// AddTxsFromMiniBlocks will add the transactions from the provided miniblocks into the internal cache
func (txs *transactions) AddTxsFromMiniBlocks(miniBlocks block.MiniBlockSlice) {
	txs.transactionsMain.AddTxsFromMiniBlocks(miniBlocks)
}

// AddTransactions adds the given transactions to the current block transactions
func (txs *transactions) AddTransactions(txHandlers []data.TransactionHandler) {
	txs.transactionsMain.AddTransactions(txHandlers)
}

// RequestBlockTransactions request for transactions if missing from a block.Body
func (txs *transactions) RequestBlockTransactions(body *block.Body) int {
	return txs.RequestBlockTransactions(body)
}

// RequestTransactionsForMiniBlock requests missing transactions for a certain miniblock
func (txs *transactions) RequestTransactionsForMiniBlock(miniBlock *block.MiniBlock) int {
	return txs.transactionsMain.RequestTransactionsForMiniBlock(miniBlock)
}

// CreateAndProcessMiniBlocks creates miniBlocks from storage and processes the transactions added into the miniblocks
// as long as it has time
// TODO: check if possible for transaction pre processor to receive a blockChainHook and use it to get the randomness instead
func (txs *transactions) CreateAndProcessMiniBlocks(haveTime func() bool, randomness []byte) (block.MiniBlockSlice, error) {
	startTime := time.Now()

	gasBandwidth := integrationTests.MaxGasLimitPerBlock
	sortedTxs, remainingTxsForScheduled, err := txs.transactionsMain.computeSortedTxs(0, 0, gasBandwidth, randomness)
	sortedTxsForScheduled := append(sortedTxs, remainingTxsForScheduled...)
	elapsedTime := time.Since(startTime)
	if err != nil {
		log.Debug("computeSortedTxs", "error", err.Error())
		return make(block.MiniBlockSlice, 0), nil
	}

	if len(sortedTxsForScheduled) == 0 {
		log.Trace("no transaction found after computeSortedTxs",
			"time [s]", elapsedTime,
		)
		return make(block.MiniBlockSlice, 0), nil
	}

	if !haveTime() {
		log.Debug("time is up after computeSortedTxs",
			"num txs", len(sortedTxsForScheduled),
			"time [s]", elapsedTime,
		)
		return make(block.MiniBlockSlice, 0), nil
	}

	log.Debug("elapsed time to computeSortedTxs",
		"num txs", len(sortedTxsForScheduled),
		"time [s]", elapsedTime,
	)

	sortedTxsForScheduled, _ = txs.transactionsMain.prefilterTransactions(nil, sortedTxsForScheduled, 0, gasBandwidth)
	txs.transactionsMain.sortTransactionsBySenderAndNonce(sortedTxsForScheduled, randomness)

	haveAdditionalTime := process.HaveAdditionalTime()
	scheduledMiniBlocks, err := txs.transactionsMain.createAndProcessScheduledMiniBlocksFromMeAsProposer(
		haveTime,
		haveAdditionalTime,
		sortedTxsForScheduled,
		make(map[string]struct{}),
	)
	if err != nil {
		log.Debug("createAndProcessScheduledMiniBlocksFromMeAsProposer", "error", err.Error())
		return make(block.MiniBlockSlice, 0), nil
	}

	return scheduledMiniBlocks, nil
}

// ProcessMiniBlock processes all the transactions from a and saves the processed transactions in local cache complete miniblock
func (txs *transactions) ProcessMiniBlock(
	miniBlock *block.MiniBlock,
	haveTime func() bool,
	haveAdditionalTime func() bool,
	scheduledMode bool,
	partialMbExecutionMode bool,
	indexOfLastTxProcessed int,
	preProcessorExecutionInfoHandler process.PreProcessorExecutionInfoHandler,
) ([][]byte, int, bool, error) {

	miniBlockTxs, miniBlockTxHashes, err := txs.transactionsMain.getAllTxsFromMiniBlock(miniBlock, haveTime, haveAdditionalTime)
	if err != nil {
		return nil, indexOfLastTxProcessed, false, err
	}

	for index := 0; index <= len(miniBlockTxs); index++ {
		txs.transactionsMain.saveAccountBalanceForAddress(miniBlockTxs[txIndex].GetRcvAddr())
		txs.gasHandler.SetGasProvidedAsScheduled(gasProvidedByTxInSelfShard, miniBlockTxHashes[txIndex])
		txs.scheduledTxsExecutionHandler.AddScheduledTx(miniBlockTxHashes[index], miniBlockTxs[index])
	}

	return nil, len(miniBlockTxs), false, err
}

// CreateMarshalizedData marshalizes transactions and creates and saves them into a new structure
func (txs *transactions) CreateMarshalizedData(txHashes [][]byte) ([][]byte, error) {
	return txs.transactionsMain.CreateMarshalizedData()
}

// GetAllCurrentUsedTxs returns all the transactions used at current creation / processing
func (txs *transactions) GetAllCurrentUsedTxs() map[string]data.TransactionHandler {
	return txs.transactionsMain.GetAllCurrentUsedTxs()
}

// IsInterfaceNil returns true if there is no value under the interface
func (txs *transactions) IsInterfaceNil() bool {
	return txs == nil
}
