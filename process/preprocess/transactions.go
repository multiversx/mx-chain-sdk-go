package preprocess

import (
	"github.com/ElrondNetwork/chain-go-sdk/integrationTests"
	"github.com/ElrondNetwork/elrond-go-core/data"
	"github.com/ElrondNetwork/elrond-go-core/data/block"
	"github.com/ElrondNetwork/elrond-go/process"
	"github.com/ElrondNetwork/elrond-go/process/block/preprocess"
)

var _ process.DataMarshalizer = (*transactions)(nil)
var _ process.PreProcessor = (*transactions)(nil)

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
	_, _, err := txs.ProcessTxsFromMe(body, haveTime, header.GetPrevRandSeed())
	return err
}

// CreateAndProcessMiniBlocks creates miniBlocks from storage and processes the transactions added into the miniblocks
// as long as it has time
func (txs *transactions) CreateAndProcessMiniBlocks(haveTime func() bool, randomness []byte) (block.MiniBlockSlice, error) {
	gasBandwidth := integrationTests.MaxGasLimitPerBlock
	return txs.CreateScheduledMiniBlocks(haveTime, randomness, gasBandwidth)
}

// ProcessMiniBlock does nothing on sovereign chain
func (txs *transactions) ProcessMiniBlock(
	_ *block.MiniBlock,
	_ func() bool,
	_ func() bool,
	_ bool,
	_ bool,
	_ int,
	_ process.PreProcessorExecutionInfoHandler,
) ([][]byte, int, bool, error) {
	return nil, 0, false, nil
}
