package track

import (
	"github.com/ElrondNetwork/elrond-go-core/data"
)

type blockNotarizerHandler interface {
	AddNotarizedHeader(notarizedHeader data.HeaderHandler, notarizedHeaderHash []byte)
	CleanupNotarizedHeadersBehindNonce(nonce uint64)
	DisplayNotarizedHeaders(message string)
	GetLastNotarizedHeader() (data.HeaderHandler, []byte, error)
	GetFirstNotarizedHeader() (data.HeaderHandler, []byte, error)
	GetLastNotarizedHeaderNonce() uint64
	GetNotarizedHeader(offset uint64) (data.HeaderHandler, []byte, error)
	InitNotarizedHeaders(startHeader data.HeaderHandler) error
	RemoveLastNotarizedHeader()
	RestoreNotarizedHeadersToGenesis()
	IsInterfaceNil() bool
}

type blockNotifierHandler interface {
	CallHandlers(headers []data.HeaderHandler, headersHashes [][]byte)
	RegisterHandler(handler func(headers []data.HeaderHandler, headersHashes [][]byte))
	GetNumRegisteredHandlers() int
	IsInterfaceNil() bool
}

type blockProcessorHandler interface {
	ComputeLongestChain(header data.HeaderHandler) ([]data.HeaderHandler, [][]byte)
	ProcessReceivedHeader(header data.HeaderHandler)
	IsInterfaceNil() bool
}

type blockTrackerHandler interface {
	ComputeLongestSelfChain() (data.HeaderHandler, []byte, []data.HeaderHandler, [][]byte)
	SortHeadersFromNonce(nonce uint64) ([]data.HeaderHandler, [][]byte)
	AddHeaderFromPool(nonce uint64)
	IsInterfaceNil() bool
}
