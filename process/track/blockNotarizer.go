package track

import (
	"sort"
	"sync"

	"github.com/ElrondNetwork/elrond-go-core/core"
	"github.com/ElrondNetwork/elrond-go-core/core/check"
	"github.com/ElrondNetwork/elrond-go-core/data"
	"github.com/ElrondNetwork/elrond-go-core/hashing"
	"github.com/ElrondNetwork/elrond-go-core/marshal"
	"github.com/ElrondNetwork/elrond-go/process"
	"github.com/ElrondNetwork/elrond-go/sharding"
)

type blockNotarizer struct {
	hasher           hashing.Hasher
	marshalizer      marshal.Marshalizer
	shardCoordinator sharding.Coordinator

	mutNotarizedHeaders sync.RWMutex
	notarizedHeaders    []*HeaderInfo
}

// NewBlockNotarizer creates a block notarizer object which implements blockNotarizerHandler interface
func NewBlockNotarizer(
	hasher hashing.Hasher,
	marshalizer marshal.Marshalizer,
	shardCoordinator sharding.Coordinator,
) (*blockNotarizer, error) {
	if check.IfNil(hasher) {
		return nil, process.ErrNilHasher
	}
	if check.IfNil(marshalizer) {
		return nil, process.ErrNilMarshalizer
	}
	if check.IfNil(shardCoordinator) {
		return nil, process.ErrNilShardCoordinator
	}

	bn := blockNotarizer{
		hasher:           hasher,
		marshalizer:      marshalizer,
		shardCoordinator: shardCoordinator,
	}

	bn.notarizedHeaders = make([]*HeaderInfo, 0)

	return &bn, nil
}

// AddNotarizedHeader adds a notarized header
func (bn *blockNotarizer) AddNotarizedHeader(
	notarizedHeader data.HeaderHandler,
	notarizedHeaderHash []byte,
) {
	if check.IfNil(notarizedHeader) {
		return
	}

	bn.mutNotarizedHeaders.Lock()
	bn.notarizedHeaders = append(bn.notarizedHeaders, &HeaderInfo{Header: notarizedHeader, Hash: notarizedHeaderHash})
	sort.Slice(bn.notarizedHeaders, func(i, j int) bool {
		return bn.notarizedHeaders[i].Header.GetNonce() < bn.notarizedHeaders[j].Header.GetNonce()
	})
	bn.mutNotarizedHeaders.Unlock()
}

// CleanupNotarizedHeadersBehindNonce cleanups notarized headers behind a given nonce
func (bn *blockNotarizer) CleanupNotarizedHeadersBehindNonce(nonce uint64) {
	if nonce == 0 {
		return
	}

	bn.mutNotarizedHeaders.Lock()
	defer bn.mutNotarizedHeaders.Unlock()

	if bn.notarizedHeaders == nil {
		return
	}

	headersInfo := make([]*HeaderInfo, 0)
	for _, hdrInfo := range bn.notarizedHeaders {
		if hdrInfo.Header.GetNonce() < nonce {
			continue
		}

		headersInfo = append(headersInfo, hdrInfo)
	}

	if len(headersInfo) == 0 {
		hdrInfo := bn.lastNotarizedHeaderInfo()
		if hdrInfo == nil {
			return
		}

		headersInfo = append(headersInfo, hdrInfo)
	}

	bn.notarizedHeaders = headersInfo
}

// DisplayNotarizedHeaders displays notarized headers
func (bn *blockNotarizer) DisplayNotarizedHeaders(message string) {
	bn.mutNotarizedHeaders.RLock()
	defer bn.mutNotarizedHeaders.RUnlock()

	if bn.notarizedHeaders == nil {
		return
	}

	if len(bn.notarizedHeaders) > 1 {
		sort.Slice(bn.notarizedHeaders, func(i, j int) bool {
			return bn.notarizedHeaders[i].Header.GetNonce() < bn.notarizedHeaders[j].Header.GetNonce()
		})
	}

	shouldNotDisplay := len(bn.notarizedHeaders) == 0 ||
		len(bn.notarizedHeaders) == 1 && bn.notarizedHeaders[0].Header.GetNonce() == 0
	if shouldNotDisplay {
		return
	}

	log.Debug(message,
		"nb", len(bn.notarizedHeaders))

	for _, hdrInfo := range bn.notarizedHeaders {
		log.Trace("notarized header info",
			"round", hdrInfo.Header.GetRound(),
			"nonce", hdrInfo.Header.GetNonce(),
			"hash", hdrInfo.Hash)
	}
}

// GetFirstNotarizedHeader returns the first notarized header
func (bn *blockNotarizer) GetFirstNotarizedHeader() (data.HeaderHandler, []byte, error) {
	bn.mutNotarizedHeaders.RLock()
	defer bn.mutNotarizedHeaders.RUnlock()

	hdrInfo := bn.firstNotarizedHeaderInfo()
	if hdrInfo == nil {
		return nil, nil, process.ErrNotarizedHeadersSliceForShardIsNil
	}

	return hdrInfo.Header, hdrInfo.Hash, nil
}

// GetLastNotarizedHeader gets the last notarized header
func (bn *blockNotarizer) GetLastNotarizedHeader() (data.HeaderHandler, []byte, error) {
	bn.mutNotarizedHeaders.RLock()
	defer bn.mutNotarizedHeaders.RUnlock()

	hdrInfo := bn.lastNotarizedHeaderInfo()
	if hdrInfo == nil {
		return nil, nil, process.ErrNotarizedHeadersSliceForShardIsNil
	}

	return hdrInfo.Header, hdrInfo.Hash, nil
}

// GetLastNotarizedHeaderNonce gets the nonce of the last notarized header
func (bn *blockNotarizer) GetLastNotarizedHeaderNonce() uint64 {
	bn.mutNotarizedHeaders.RLock()
	defer bn.mutNotarizedHeaders.RUnlock()

	hdrInfo := bn.lastNotarizedHeaderInfo()
	if hdrInfo == nil {
		return 0
	}

	return hdrInfo.Header.GetNonce()
}

func (bn *blockNotarizer) firstNotarizedHeaderInfo() *HeaderInfo {
	notarizedHeadersCount := len(bn.notarizedHeaders)
	if notarizedHeadersCount > 0 {
		return bn.notarizedHeaders[0]
	}

	return nil
}

func (bn *blockNotarizer) lastNotarizedHeaderInfo() *HeaderInfo {
	notarizedHeadersCount := len(bn.notarizedHeaders)
	if notarizedHeadersCount > 0 {
		return bn.notarizedHeaders[notarizedHeadersCount-1]
	}

	return nil
}

// GetNotarizedHeader gets notarized header with a given offset
func (bn *blockNotarizer) GetNotarizedHeader(offset uint64) (data.HeaderHandler, []byte, error) {
	bn.mutNotarizedHeaders.RLock()
	defer bn.mutNotarizedHeaders.RUnlock()

	if bn.notarizedHeaders == nil {
		return nil, nil, process.ErrNotarizedHeadersSliceForShardIsNil
	}

	notarizedHeadersCount := uint64(len(bn.notarizedHeaders))
	if notarizedHeadersCount <= offset {
		return nil, nil, ErrNotarizedHeaderOffsetIsOutOfBound
	}

	hdrInfo := bn.notarizedHeaders[notarizedHeadersCount-offset-1]

	return hdrInfo.Header, hdrInfo.Hash, nil
}

// InitNotarizedHeaders initializes all notarized headers with the genesis value (nonce 0)
func (bn *blockNotarizer) InitNotarizedHeaders(startHeader data.HeaderHandler) error {
	if startHeader == nil {
		return process.ErrNotarizedHeadersSliceIsNil
	}

	bn.mutNotarizedHeaders.Lock()
	defer bn.mutNotarizedHeaders.Unlock()

	bn.notarizedHeaders = make([]*HeaderInfo, 0)

	startHeaderHash, err := core.CalculateHash(bn.marshalizer, bn.hasher, startHeader)
	if err != nil {
			return err
		}

	bn.notarizedHeaders = append(bn.notarizedHeaders, &HeaderInfo{Header: startHeader, Hash: startHeaderHash})

	return nil
}

// RemoveLastNotarizedHeader removes last notarized header
func (bn *blockNotarizer) RemoveLastNotarizedHeader() {
	bn.mutNotarizedHeaders.Lock()
	notarizedHeadersCount := len(bn.notarizedHeaders)
	if notarizedHeadersCount > 1 {
		bn.notarizedHeaders = bn.notarizedHeaders[:notarizedHeadersCount-1]
	}
	bn.mutNotarizedHeaders.Unlock()
}

// RestoreNotarizedHeadersToGenesis restores all notarized headers to the genesis value (nonce 0)
func (bn *blockNotarizer) RestoreNotarizedHeadersToGenesis() {
	bn.mutNotarizedHeaders.Lock()
	notarizedHeadersCount := len(bn.notarizedHeaders)
	if notarizedHeadersCount > 1 {
		bn.notarizedHeaders = bn.notarizedHeaders[:1]
	}
	bn.mutNotarizedHeaders.Unlock()
}

// IsInterfaceNil returns true if there is no value under the interface
func (bn *blockNotarizer) IsInterfaceNil() bool {
	return bn == nil
}
