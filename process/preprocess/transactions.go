package preprocess

import (
	"github.com/multiversx/mx-chain-core-go/data"
	"github.com/multiversx/mx-chain-core-go/data/block"
	"github.com/multiversx/mx-chain-go/process"
	"github.com/multiversx/mx-chain-go/process/block/preprocess"
	"github.com/multiversx/mx-chain-sdk-go/integrationTests"
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

// CreateAndProcessMiniBlocks creates miniBlocks from selected transactions
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
