package preprocess

import (
	"github.com/ElrondNetwork/chain-go-sdk/integrationTests"
	"github.com/ElrondNetwork/elrond-go/process/block/preprocess"
	"time"

	"github.com/ElrondNetwork/elrond-go-core/core/check"
	"github.com/ElrondNetwork/elrond-go-core/data"
	"github.com/ElrondNetwork/elrond-go-core/data/block"
	logger "github.com/ElrondNetwork/elrond-go-logger"
	"github.com/ElrondNetwork/elrond-go/process"
)

var _ process.DataMarshalizer = (*transactions)(nil)
var _ process.PreProcessor = (*transactions)(nil)

var log = logger.GetOrCreate("process/block/preprocess")

type transactions struct {
	TransactionPreprocessor
}

// NewTransactionPreprocessor creates a new transaction preprocessor object
func NewTransactionPreprocessor(
	args preprocess.ArgsTransactionPreProcessor,
) (*transactions, error) {
	txPreProcess, err := preprocess.NewTransactionPreprocessor(args)
	if err != nil {
		return nil, err
	}

	txs := &transactions{
		txPreProcess,
	}

	return txs, nil
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

	scheduledMiniBlocks, err := txs.CreateAndProcessScheduledMiniBlocksFromMeAsValidator(
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

	return nil
}

// CreateAndProcessMiniBlocks creates miniBlocks from storage and processes the transactions added into the miniblocks
// as long as it has time
func (txs *transactions) CreateAndProcessMiniBlocks(haveTime func() bool, randomness []byte) (block.MiniBlockSlice, error) {
	startTime := time.Now()

	gasBandwidth := integrationTests.MaxGasLimitPerBlock
	sortedTxs, remainingTxsForScheduled, err := txs.ComputeSortedTxs(0, 0, gasBandwidth, randomness)
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

	sortedTxsForScheduled, _ = txs.PrefilterTransactions(nil, sortedTxsForScheduled, 0, gasBandwidth)
	txs.SortTransactionsBySenderAndNonce(sortedTxsForScheduled, randomness)

	haveAdditionalTime := process.HaveAdditionalTime()
	scheduledMiniBlocks, err := txs.CreateAndProcessScheduledMiniBlocksFromMeAsProposer(
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

// ProcessMiniBlock does nothing on sovereign chain
func (txs *transactions) ProcessMiniBlock(
	_ *block.MiniBlock,
	haveTime func() bool,
	_ func() bool,
	_ bool,
	_ bool,
	_ int,
	_ process.PreProcessorExecutionInfoHandler,
) ([][]byte, int, bool, error) {
	return nil, 0, false, nil
}
