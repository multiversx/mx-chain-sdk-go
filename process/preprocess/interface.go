package preprocess

import (
	"github.com/multiversx/mx-chain-core-go/data/block"
	"github.com/multiversx/mx-chain-go/process"
)

// TransactionPreprocessor complete interface for transaction preprocessor from mx-chain-go
type TransactionPreprocessor interface {
	process.PreProcessor
	ProcessTxsFromMe(body *block.Body, haveTime func() bool, randomness []byte) (block.MiniBlockSlice, map[string]struct{}, error)
	CreateScheduledMiniBlocks(haveTime func() bool, randomness []byte, gasBandwidth uint64) (block.MiniBlockSlice, error)
	CreateMarshalledData(txHashes [][]byte) ([][]byte, error)
}
