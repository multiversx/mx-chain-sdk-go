package track

import (
	"github.com/ElrondNetwork/elrond-go-core/data"
	"github.com/ElrondNetwork/elrond-go/process"
	"github.com/ElrondNetwork/elrond-go/sharding"
)

// shardBlockTrack

func (sbt *shardBlockTrack) GetTrackedShardHeaderWithNonceAndHash(nonce uint64, hash []byte) (data.HeaderHandler, error) {
	return sbt.getTrackedShardHeaderWithNonceAndHash(nonce, hash)
}

// baseBlockTrack

func (bbt *baseBlockTrack) ReceivedHeader(headerHandler data.HeaderHandler, headerHash []byte) {
	bbt.receivedHeader(headerHandler, headerHash)
}

func CheckTrackerNilParameters(arguments ArgBaseTracker) error {
	return checkTrackerNilParameters(arguments)
}

func (bbt *baseBlockTrack) InitNotarizedHeaders(startHeader data.HeaderHandler) error {
	return bbt.initNotarizedHeaders(startHeader)
}

func (bbt *baseBlockTrack) ReceivedShardHeader(headerHandler data.HeaderHandler, shardHeaderHash []byte) {
	bbt.receivedShardHeader(headerHandler, shardHeaderHash)
}

func (bbt *baseBlockTrack) GetMaxNumHeadersToKeep() int {
	return bbt.maxNumHeadersToKeep
}

func (bbt *baseBlockTrack) ShouldAddHeader(headerHandler data.HeaderHandler) bool {
	return bbt.shouldAddHeader(headerHandler, bbt.selfNotarizer)
}

func (bbt *baseBlockTrack) AddHeader(header data.HeaderHandler, hash []byte) bool {
	return bbt.addHeader(header, hash)
}

func (bbt *baseBlockTrack) AppendTrackedHeader(headerHandler data.HeaderHandler) {
	bbt.mutHeaders.Lock()
	if  bbt.headers == nil {
		bbt.headers = make(map[uint64][]*HeaderInfo)
	}

	bbt.headers[headerHandler.GetNonce()] = append(bbt.headers[headerHandler.GetNonce()], &HeaderInfo{Header: headerHandler})
	bbt.mutHeaders.Unlock()
}

func (bbt *baseBlockTrack) CleanupTrackedHeadersBehindNonce(nonce uint64) {
	bbt.cleanupTrackedHeadersBehindNonce(nonce)
}

func (bbt *baseBlockTrack) DisplayTrackedHeaders(message string) {
	bbt.displayTrackedHeaders(message)
}

func (bbt *baseBlockTrack) SetRoundHandler(roundHandler process.RoundHandler) {
	bbt.roundHandler = roundHandler
}

func (bbt *baseBlockTrack) SetSelfNotarizer(notarizer blockNotarizerHandler) {
	bbt.selfNotarizer = notarizer
}

func (bbt *baseBlockTrack) SetShardCoordinator(coordinator sharding.Coordinator) {
	bbt.shardCoordinator = coordinator
}

func NewBaseBlockTrack() *baseBlockTrack {
	return &baseBlockTrack{}
}

func (bbt *baseBlockTrack) IsHeaderOutOfRange(headerHandler data.HeaderHandler) bool {
	return bbt.isHeaderOutOfRange(headerHandler)
}

// blockNotifier

func (bn *blockNotifier) GetNotarizedHeadersHandlers() []func(headers []data.HeaderHandler, headersHashes [][]byte) {
	bn.mutNotarizedHeadersHandlers.RLock()
	notarizedHeadersHandlers := bn.notarizedHeadersHandlers
	bn.mutNotarizedHeadersHandlers.RUnlock()

	return notarizedHeadersHandlers
}

// blockNotarizer

func (bn *blockNotarizer) AppendNotarizedHeader(headerHandler data.HeaderHandler) {
	bn.mutNotarizedHeaders.Lock()
	bn.notarizedHeaders = append(bn.notarizedHeaders, &HeaderInfo{Header: headerHandler})
	bn.mutNotarizedHeaders.Unlock()
}

func (bn *blockNotarizer) GetNotarizedHeaders() []*HeaderInfo {
	bn.mutNotarizedHeaders.RLock()
	notarizedHeaders := bn.notarizedHeaders
	bn.mutNotarizedHeaders.RUnlock()

	return notarizedHeaders
}

func (bn *blockNotarizer) GetNotarizedHeaderWithIndex(index int) data.HeaderHandler {
	bn.mutNotarizedHeaders.RLock()
	notarizedHeader := bn.notarizedHeaders[index].Header
	bn.mutNotarizedHeaders.RUnlock()

	return notarizedHeader
}

func (bn *blockNotarizer) LastNotarizedHeaderInfo() *HeaderInfo {
	return bn.lastNotarizedHeaderInfo()
}

// blockProcessor

func (bp *blockProcessor) DoJobOnReceivedHeader() {
	bp.doJobOnReceivedHeader()
}

func (bp *blockProcessor) ComputeSelfNotarizedHeaders(headers []data.HeaderHandler) ([]data.HeaderHandler, [][]byte) {
	return bp.computeSelfNotarizedHeaders(headers)
}

func (bp *blockProcessor) GetNextHeader(longestChainHeadersIndexes *[]int, headersIndexes []int, prevHeader data.HeaderHandler, sortedHeaders []data.HeaderHandler, index int) {
	bp.getNextHeader(longestChainHeadersIndexes, headersIndexes, prevHeader, sortedHeaders, index)
}

func (bp *blockProcessor) CheckHeaderFinality(header data.HeaderHandler, sortedHeaders []data.HeaderHandler, index int) error {
	return bp.checkHeaderFinality(header, sortedHeaders, index)
}

func (bp *blockProcessor) RequestHeadersIfNeeded(lastNotarizedHeader data.HeaderHandler, sortedHeaders []data.HeaderHandler, longestChainHeaders []data.HeaderHandler) {
	bp.requestHeadersIfNeeded(lastNotarizedHeader, sortedHeaders, longestChainHeaders)
}

func (bp *blockProcessor) GetLatestValidHeader(lastNotarizedHeader data.HeaderHandler, longestChainHeaders []data.HeaderHandler) data.HeaderHandler {
	return bp.getLatestValidHeader(lastNotarizedHeader, longestChainHeaders)
}

func (bp *blockProcessor) GetHighestRoundInReceivedHeaders(latestValidHeader data.HeaderHandler, sortedReceivedHeaders []data.HeaderHandler) uint64 {
	return bp.getHighestRoundInReceivedHeaders(latestValidHeader, sortedReceivedHeaders)
}

func (bp *blockProcessor) RequestHeadersIfNothingNewIsReceived(lastNotarizedHeaderNonce uint64, latestValidHeader data.HeaderHandler, highestRoundInReceivedHeaders uint64) {
	bp.requestHeadersIfNothingNewIsReceived(lastNotarizedHeaderNonce, latestValidHeader, highestRoundInReceivedHeaders)
}

func (bp *blockProcessor) RequestHeaders(shardID uint32, fromNonce uint64) {
	bp.requestHeaders(shardID, fromNonce)
}

func (bp *blockProcessor) ShouldProcessReceivedHeader(headerHandler data.HeaderHandler) bool {
	return bp.shouldProcessReceivedHeader(headerHandler)
}
