package block

import (
	"fmt"
	"math/big"
	"time"

	"github.com/multiversx/mx-chain-core-go/core"
	"github.com/multiversx/mx-chain-core-go/core/check"
	"github.com/multiversx/mx-chain-core-go/core/queue"
	"github.com/multiversx/mx-chain-core-go/data"
	"github.com/multiversx/mx-chain-core-go/data/block"
	"github.com/multiversx/mx-chain-core-go/data/outport"
	"github.com/multiversx/mx-chain-go/common"
	"github.com/multiversx/mx-chain-go/process"
	"github.com/multiversx/mx-chain-go/process/block/bootstrapStorage"
	"github.com/multiversx/mx-chain-go/process/block/processedMb"
	"github.com/multiversx/mx-chain-go/state"
	logger "github.com/multiversx/mx-chain-logger-go"
)

var _ process.BlockProcessor = (*shardProcessor)(nil)

const (
	timeBetweenCheckForEpochStart = 100 * time.Millisecond
	pruningDelayMultiplier        = 2
	defaultPruningDelay           = 10
)

// shardProcessor implements shardProcessor interface and actually it tries to execute block
type shardProcessor struct {
	*baseProcessor

	scToProtocol                 process.SmartContractToProtocolHandler
	validatorStatisticsProcessor process.ValidatorStatisticsProcessor
	userStatePruningQueue        core.Queue
	processStatusHandler         common.ProcessStatusHandler
}

// NewShardProcessor creates a new shardProcessor object
func NewShardProcessor(arguments ArgShardProcessor) (*shardProcessor, error) {
	err := checkProcessorNilParameters(arguments.ArgBaseProcessor)
	if err != nil {
		return nil, err
	}

	if check.IfNil(arguments.DataComponents.Datapool()) {
		return nil, process.ErrNilDataPoolHolder
	}
	if check.IfNil(arguments.DataComponents.Datapool().Headers()) {
		return nil, process.ErrNilHeadersDataPool
	}
	if check.IfNil(arguments.DataComponents.Datapool().Transactions()) {
		return nil, process.ErrNilTransactionPool
	}
	genesisHdr := arguments.DataComponents.Blockchain().GetGenesisHeader()
	if check.IfNil(genesisHdr) {
		return nil, fmt.Errorf("%w for genesis header in DataComponents.Blockchain", process.ErrNilHeaderHandler)
	}

	pruningQueueSize := arguments.Config.StateTriesConfig.UserStatePruningQueueSize
	pruningDelay := uint32(pruningQueueSize * pruningDelayMultiplier)
	if pruningDelay < defaultPruningDelay {
		log.Warn("using default pruning delay", "user state pruning queue size", pruningQueueSize)
		pruningDelay = defaultPruningDelay
	}

	base := &baseProcessor{
		accountsDB:                    arguments.AccountsDB,
		blockSizeThrottler:            arguments.BlockSizeThrottler,
		forkDetector:                  arguments.ForkDetector,
		hasher:                        arguments.CoreComponents.Hasher(),
		marshalizer:                   arguments.CoreComponents.InternalMarshalizer(),
		store:                         arguments.DataComponents.StorageService(),
		shardCoordinator:              arguments.BootstrapComponents.ShardCoordinator(),
		nodesCoordinator:              arguments.NodesCoordinator,
		uint64Converter:               arguments.CoreComponents.Uint64ByteSliceConverter(),
		requestHandler:                arguments.RequestHandler,
		appStatusHandler:              arguments.CoreComponents.StatusHandler(),
		blockChainHook:                arguments.BlockChainHook,
		txCoordinator:                 arguments.TxCoordinator,
		roundHandler:                  arguments.CoreComponents.RoundHandler(),
		epochStartTrigger:             arguments.EpochStartTrigger,
		headerValidator:               arguments.HeaderValidator,
		bootStorer:                    arguments.BootStorer,
		blockTracker:                  arguments.BlockTracker,
		dataPool:                      arguments.DataComponents.Datapool(),
		stateCheckpointModulus:        arguments.Config.StateTriesConfig.CheckpointRoundsModulus,
		blockChain:                    arguments.DataComponents.Blockchain(),
		feeHandler:                    arguments.FeeHandler,
		outportHandler:                arguments.StatusComponents.OutportHandler(),
		genesisNonce:                  genesisHdr.GetNonce(),
		versionedHeaderFactory:        arguments.BootstrapComponents.VersionedHeaderFactory(),
		headerIntegrityVerifier:       arguments.BootstrapComponents.HeaderIntegrityVerifier(),
		historyRepo:                   arguments.HistoryRepository,
		epochNotifier:                 arguments.EpochNotifier,
		roundNotifier:                 arguments.RoundNotifier,
		vmContainerFactory:            arguments.VMContainersFactory,
		vmContainer:                   arguments.VmContainer,
		processDataTriesOnCommitEpoch: arguments.Config.Debug.EpochStart.ProcessDataTrieOnCommitEpoch,
		gasConsumedProvider:           arguments.GasHandler,
		economicsData:                 arguments.CoreComponents.EconomicsData(),
		scheduledTxsExecutionHandler:  arguments.ScheduledTxsExecutionHandler,
		pruningDelay:                  pruningDelay,
		processedMiniBlocksTracker:    arguments.ProcessedMiniBlocksTracker,
		receiptsRepository:            arguments.ReceiptsRepository,
	}

	sp := shardProcessor{
		baseProcessor:        base,
		processStatusHandler: arguments.CoreComponents.ProcessStatusHandler(),
	}

	sp.txCounter, err = NewTransactionCounter(sp.hasher, sp.marshalizer)
	if err != nil {
		return nil, err
	}

	sp.requestBlockBodyHandler = &sp
	sp.hdrsForCurrBlock = newHdrForBlock()

	sp.userStatePruningQueue = queue.NewSliceQueue(arguments.Config.StateTriesConfig.UserStatePruningQueueSize)

	sp.epochNotifier.RegisterNotifyHandler(&sp)

	return &sp, nil
}

// ProcessBlock processes a block. It returns nil if all ok or the specific error
func (sp *shardProcessor) ProcessBlock(
	headerHandler data.HeaderHandler,
	bodyHandler data.BodyHandler,
	haveTime func() time.Duration,
) error {
	if haveTime == nil {
		return process.ErrNilHaveTimeHandler
	}

	sp.processStatusHandler.SetBusy("shardProcessor.ProcessBlock")
	defer sp.processStatusHandler.SetIdle()

	err := sp.checkBlockValidity(headerHandler, bodyHandler)
	if err != nil {
		if err == process.ErrBlockHashDoesNotMatch {
			log.Debug("requested missing shard header",
				"hash", headerHandler.GetPrevHash(),
				"for shard", headerHandler.GetShardID(),
			)

			go sp.requestHandler.RequestShardHeader(headerHandler.GetShardID(), headerHandler.GetPrevHash())
		}

		return err
	}

	sp.roundNotifier.CheckRound(headerHandler.GetRound())
	sp.epochNotifier.CheckEpoch(headerHandler)
	sp.requestHandler.SetEpoch(headerHandler.GetEpoch())

	log.Debug("started processing block",
		"epoch", headerHandler.GetEpoch(),
		"shard", headerHandler.GetShardID(),
		"round", headerHandler.GetRound(),
		"nonce", headerHandler.GetNonce(),
	)

	header, ok := headerHandler.(data.ShardHeaderHandler)
	if !ok {
		return process.ErrWrongTypeAssertion
	}

	body, ok := bodyHandler.(*block.Body)
	if !ok {
		return process.ErrWrongTypeAssertion
	}

	go getMetricsFromBlockBody(body, sp.marshalizer, sp.appStatusHandler)

	err = sp.checkHeaderBodyCorrelation(header.GetMiniBlockHeaderHandlers(), body)
	if err != nil {
		return err
	}

	txCounts, rewardCounts, unsignedCounts := sp.txCounter.getPoolCounts(sp.dataPool)
	log.Debug("total txs in pool", "counts", txCounts.String())
	log.Debug("total txs in rewards pool", "counts", rewardCounts.String())
	log.Debug("total txs in unsigned pool", "counts", unsignedCounts.String())

	go getMetricsFromHeader(header, uint64(txCounts.GetTotal()), sp.marshalizer, sp.appStatusHandler)

	err = sp.createBlockStarted()
	if err != nil {
		return err
	}

	sp.blockChainHook.SetCurrentHeader(header)

	sp.txCoordinator.RequestBlockTransactions(body)

	if haveTime() < 0 {
		return process.ErrTimeIsOut
	}

	err = sp.txCoordinator.IsDataPreparedForProcessing(haveTime)
	if err != nil {
		return err
	}

	err = sp.requestEpochStartInfo(header, haveTime)
	if err != nil {
		return err
	}

	if sp.accountsDB[state.UserAccountsState].JournalLen() != 0 {
		log.Error("shardProcessor.ProcessBlock first entry", "stack", string(sp.accountsDB[state.UserAccountsState].GetStackDebugFirstEntry()))
		return process.ErrAccountStateDirty
	}

	defer func() {
		if err != nil {
			sp.RevertCurrentBlock()
		}
	}()

	mbIndex := sp.getIndexOfFirstMiniBlockToBeExecuted(header)
	miniBlocks := body.MiniBlocks[mbIndex:]

	startTime := time.Now()
	err = sp.txCoordinator.ProcessBlockTransaction(header, &block.Body{MiniBlocks: miniBlocks}, haveTime)
	elapsedTime := time.Since(startTime)
	log.Debug("elapsed time to process block transaction",
		"time [s]", elapsedTime,
	)
	if err != nil {
		return err
	}

	err = sp.txCoordinator.VerifyCreatedBlockTransactions(header, &block.Body{MiniBlocks: miniBlocks})
	if err != nil {
		return err
	}

	err = sp.txCoordinator.VerifyCreatedMiniBlocks(header, body)
	if err != nil {
		return err
	}

	err = sp.verifyFees(header)
	if err != nil {
		return err
	}

	if !sp.verifyStateRoot(header.GetRootHash()) {
		err = process.ErrRootStateDoesNotMatch
		return err
	}

	return nil
}

// RevertStateToBlock recreates the state tries to the root hashes indicated by the provided root hash and header
func (sp *shardProcessor) RevertStateToBlock(header data.HeaderHandler, rootHash []byte) error {

	err := sp.accountsDB[state.UserAccountsState].RecreateTrie(rootHash)
	if err != nil {
		log.Debug("recreate trie with error for header",
			"nonce", header.GetNonce(),
			"header root hash", header.GetRootHash(),
			"given root hash", rootHash,
			"error", err,
		)

		return err
	}

	return nil
}

// SetNumProcessedObj will set the num of processed transactions
func (sp *shardProcessor) SetNumProcessedObj(numObj uint64) {
	sp.txCounter.totalTxs = numObj
}

func (sp *shardProcessor) indexBlockIfNeeded(
	body data.BodyHandler,
	headerHash []byte,
	header data.HeaderHandler,
	lastBlockHeader data.HeaderHandler,
) {
	if !sp.outportHandler.HasDrivers() {
		return
	}
	if check.IfNil(header) {
		return
	}
	if check.IfNil(body) {
		return
	}

	log.Debug("preparing to index block", "hash", headerHash, "nonce", header.GetNonce(), "round", header.GetRound())

	pool := &outport.Pool{
		Txs:      sp.txCoordinator.GetAllCurrentUsedTxs(block.TxBlock),
		Scrs:     sp.txCoordinator.GetAllCurrentUsedTxs(block.SmartContractResultBlock),
		Rewards:  sp.txCoordinator.GetAllCurrentUsedTxs(block.RewardsBlock),
		Invalid:  sp.txCoordinator.GetAllCurrentUsedTxs(block.InvalidBlock),
		Receipts: sp.txCoordinator.GetAllCurrentUsedTxs(block.ReceiptBlock),
		Logs:     sp.txCoordinator.GetAllCurrentLogs(),
	}

	shardId := sp.shardCoordinator.SelfId()

	pubKeys, err := sp.nodesCoordinator.GetConsensusValidatorsPublicKeys(
		header.GetPrevRandSeed(),
		header.GetRound(),
		shardId,
		epoch,
	)
	if err != nil {
		log.Debug("indexBlockIfNeeded: GetConsensusValidatorsPublicKeys",
			"hash", headerHash,
			"epoch", epoch,
			"error", err.Error())
		return
	}

	nodesCoordinatorShardID, err := sp.nodesCoordinator.ShardIdForEpoch(epoch)
	if err != nil {
		log.Debug("indexBlockIfNeeded: ShardIdForEpoch",
			"hash", headerHash,
			"epoch", epoch,
			"error", err.Error())
		return
	}

	if shardId != nodesCoordinatorShardID {
		log.Debug("indexBlockIfNeeded: shardId != nodesCoordinatorShardID",
			"epoch", epoch,
			"shardCoordinator.ShardID", shardId,
			"nodesCoordinator.ShardID", nodesCoordinatorShardID)
		return
	}

	signersIndexes, err := sp.nodesCoordinator.GetValidatorsIndexes(pubKeys, epoch)
	if err != nil {
		log.Error("indexBlockIfNeeded: GetValidatorsIndexes",
			"round", header.GetRound(),
			"nonce", header.GetNonce(),
			"hash", headerHash,
			"error", err.Error(),
		)
		return
	}

	gasProvidedInHeader := sp.baseProcessor.gasConsumedProvider.TotalGasProvidedWithScheduled()
	gasPenalizedInheader := sp.baseProcessor.gasConsumedProvider.TotalGasPenalized()
	gasRefundedInHeader := sp.baseProcessor.gasConsumedProvider.TotalGasRefunded()
	maxGasInHeader := sp.baseProcessor.economicsData.MaxGasLimitPerBlock(sp.shardCoordinator.SelfId())

	args := &outport.ArgsSaveBlockData{
		HeaderHash:     headerHash,
		Body:           body,
		Header:         header,
		SignersIndexes: signersIndexes,
		HeaderGasConsumption: outport.HeaderGasConsumption{
			GasProvided:    gasProvidedInHeader,
			GasRefunded:    gasRefundedInHeader,
			GasPenalized:   gasPenalizedInheader,
			MaxGasPerBlock: maxGasInHeader,
		},
		NotarizedHeadersHashes: nil,
		TransactionsPool:       pool,
	}

	sp.outportHandler.SaveBlock(args)
	log.Debug("indexed block", "hash", headerHash, "nonce", header.GetNonce(), "round", header.GetRound())

	indexRoundInfo(sp.outportHandler, sp.nodesCoordinator, shardId, header, lastBlockHeader, signersIndexes)
}

// RestoreBlockIntoPools restores the TxBlock and MetaBlock into associated pools
func (sp *shardProcessor) RestoreBlockIntoPools(headerHandler data.HeaderHandler, bodyHandler data.BodyHandler) error {
	if check.IfNil(headerHandler) {
		return process.ErrNilBlockHeader
	}

	sp.restoreBlockBody(bodyHandler)
	sp.blockTracker.RemoveLastNotarizedHeaders()

	return nil
}

// CreateBlock creates the final block and header for the current round
func (sp *shardProcessor) CreateBlock(
	initialHdr data.HeaderHandler,
	haveTime func() bool,
) (data.HeaderHandler, data.BodyHandler, error) {
	if check.IfNil(initialHdr) {
		return nil, nil, process.ErrNilBlockHeader
	}
	shardHdr, ok := initialHdr.(data.ShardHeaderHandler)
	if !ok {
		return nil, nil, process.ErrWrongTypeAssertion
	}

	sp.processStatusHandler.SetBusy("shardProcessor.CreateBlock")
	defer sp.processStatusHandler.SetIdle()

	err := sp.createBlockStarted()
	if err != nil {
		return nil, nil, err
	}

	// placeholder for shardProcessor.CreateBlock script 1

	// placeholder for shardProcessor.CreateBlock script 2

	sp.blockChainHook.SetCurrentHeader(shardHdr)
	body, processedMiniBlocksDestMeInfo, err := sp.createBlockBody(shardHdr, haveTime)
	if err != nil {
		return nil, nil, err
	}

	finalBody, err := sp.applyBodyToHeader(shardHdr, body, processedMiniBlocksDestMeInfo)
	if err != nil {
		return nil, nil, err
	}

	for _, miniBlock := range finalBody.MiniBlocks {
		log.Trace("CreateBlock: miniblock",
			"sender shard", miniBlock.SenderShardID,
			"receiver shard", miniBlock.ReceiverShardID,
			"type", miniBlock.Type,
			"num txs", len(miniBlock.TxHashes))
	}

	return shardHdr, finalBody, nil
}

// createBlockBody creates a a list of miniblocks by filling them with transactions out of the transactions pools
// as long as the transactions limit for the block has not been reached and there is still time to add transactions
func (sp *shardProcessor) createBlockBody(shardHdr data.HeaderHandler, haveTime func() bool) (*block.Body, map[string]*processedMb.ProcessedMiniBlockInfo, error) {
	sp.blockSizeThrottler.ComputeCurrentMaxSize()

	log.Debug("started creating block body",
		"epoch", shardHdr.GetEpoch(),
		"round", shardHdr.GetRound(),
		"nonce", shardHdr.GetNonce(),
	)

	miniBlocks, processedMiniBlocksDestMeInfo, err := sp.createMiniBlocks(haveTime, shardHdr.GetPrevRandSeed())
	if err != nil {
		return nil, nil, err
	}

	sp.requestHandler.SetEpoch(shardHdr.GetEpoch())

	return miniBlocks, processedMiniBlocksDestMeInfo, nil
}

// CommitBlock commits the block in the blockchain if everything was checked successfully
func (sp *shardProcessor) CommitBlock(
	headerHandler data.HeaderHandler,
	bodyHandler data.BodyHandler,
) error {
	var err error
	sp.processStatusHandler.SetBusy("shardProcessor.CommitBlock")
	defer func() {
		if err != nil {
			sp.RevertCurrentBlock()
		}
		sp.processStatusHandler.SetIdle()
	}()

	err = checkForNils(headerHandler, bodyHandler)
	if err != nil {
		return err
	}

	sp.store.SetEpochForPutOperation(headerHandler.GetEpoch())

	log.Debug("started committing block",
		"epoch", headerHandler.GetEpoch(),
		"shard", headerHandler.GetShardID(),
		"round", headerHandler.GetRound(),
		"nonce", headerHandler.GetNonce(),
	)

	err = sp.checkBlockValidity(headerHandler, bodyHandler)
	if err != nil {
		return err
	}

	header, ok := headerHandler.(data.ShardHeaderHandler)
	if !ok {
		err = process.ErrWrongTypeAssertion
		return err
	}

	marshalizedHeader, err := sp.marshalizer.Marshal(header)
	if err != nil {
		return err
	}

	headerHash := sp.hasher.Compute(string(marshalizedHeader))

	sp.saveShardHeader(header, headerHash, marshalizedHeader)

	body, ok := bodyHandler.(*block.Body)
	if !ok {
		err = process.ErrWrongTypeAssertion
		return err
	}

	sp.saveBody(body, header, headerHash)

	selfNotarizedHeaders, selfNotarizedHeadersHashes, err := sp.getHighestHdrForOwnShardFromMetachain(processedMetaHdrs)
	if err != nil {
		return err
	}

	err = sp.saveLastNotarizedHeader(core.MetachainShardId, processedMetaHdrs)
	if err != nil {
		return err
	}

	err = sp.commitAll(headerHandler)
	if err != nil {
		return err
	}

	log.Info("shard block has been committed successfully",
		"epoch", header.GetEpoch(),
		"shard", header.GetShardID(),
		"round", header.GetRound(),
		"nonce", header.GetNonce(),
		"hash", headerHash,
	)

	errNotCritical := sp.forkDetector.AddHeader(header, headerHash, process.BHProcessed, selfNotarizedHeaders, selfNotarizedHeadersHashes)
	if errNotCritical != nil {
		log.Debug("forkDetector.AddHeader", "error", errNotCritical.Error())
	}

	currentHeader, currentHeaderHash := getLastSelfNotarizedHeaderByItself(sp.blockChain)
	sp.blockTracker.AddSelfNotarizedHeader(sp.shardCoordinator.SelfId(), currentHeader, currentHeaderHash)

	if sp.lastRestartNonce == 0 {
		sp.lastRestartNonce = header.GetNonce()
	}

	sp.updateState(selfNotarizedHeaders, header)

	highestFinalBlockNonce := sp.forkDetector.GetHighestFinalBlockNonce()
	log.Debug("highest final shard block",
		"shard", sp.shardCoordinator.SelfId(),
		"nonce", highestFinalBlockNonce,
	)

	lastBlockHeader := sp.blockChain.GetCurrentBlockHeader()

	committedRootHash, err := sp.accountsDB[state.UserAccountsState].RootHash()
	if err != nil {
		return err
	}

	err = sp.blockChain.SetCurrentBlockHeaderAndRootHash(header, committedRootHash)
	if err != nil {
		return err
	}

	sp.blockChain.SetCurrentBlockHeaderHash(headerHash)
	sp.indexBlockIfNeeded(bodyHandler, headerHash, headerHandler, lastBlockHeader)
	sp.recordBlockInHistory(headerHash, headerHandler, bodyHandler)

	lastCrossNotarizedHeader, _, err := sp.blockTracker.GetLastCrossNotarizedHeader(core.MetachainShardId)
	if err != nil {
		return err
	}

	saveMetricsForCommittedShardBlock(
		sp.nodesCoordinator,
		sp.appStatusHandler,
		logger.DisplayByteSlice(headerHash),
		highestFinalBlockNonce,
		lastCrossNotarizedHeader,
		header,
	)

	headerInfo := bootstrapStorage.BootstrapHeaderInfo{
		ShardId: header.GetShardID(),
		Epoch:   header.GetEpoch(),
		Nonce:   header.GetNonce(),
		Hash:    headerHash,
	}

	nodesCoordinatorKey := sp.nodesCoordinator.GetSavedStateKey()
	epochStartKey := sp.epochStartTrigger.GetSavedStateKey()

	args := bootStorerDataArgs{
		headerInfo:                 headerInfo,
		round:                      header.GetRound(),
		lastSelfNotarizedHeaders:   sp.getBootstrapHeadersInfo(selfNotarizedHeaders, selfNotarizedHeadersHashes),
		highestFinalBlockNonce:     sp.forkDetector.GetHighestFinalBlockNonce(),
		processedMiniBlocks:        sp.processedMiniBlocksTracker.ConvertProcessedMiniBlocksMapToSlice(),
		nodesCoordinatorConfigKey:  nodesCoordinatorKey,
		epochStartTriggerConfigKey: epochStartKey,
	}

	sp.prepareDataForBootStorer(args)

	// write data to log
	go sp.txCounter.displayLogInfo(
		header,
		body,
		headerHash,
		sp.shardCoordinator.NumberOfShards(),
		sp.shardCoordinator.SelfId(),
		sp.dataPool,
		sp.appStatusHandler,
		sp.blockTracker,
	)

	sp.blockSizeThrottler.Succeed(header.GetRound())

	sp.displayPoolsInfo()

	errNotCritical = sp.removeTxsFromPools(header, body)
	if errNotCritical != nil {
		log.Debug("removeTxsFromPools", "error", errNotCritical.Error())
	}

	sp.cleanupPools(headerHandler)

	return nil
}

func (sp *shardProcessor) displayPoolsInfo() {
	headersPool := sp.dataPool.Headers()
	miniBlocksPool := sp.dataPool.MiniBlocks()

	log.Trace("pools info",
		"shard", sp.shardCoordinator.SelfId(),
		"num headers", headersPool.GetNumHeaders(sp.shardCoordinator.SelfId()))

	log.Trace("pools info",
		"shard", core.MetachainShardId,
		"num headers", headersPool.GetNumHeaders(core.MetachainShardId))

	// numShardsToKeepHeaders represents the total number of shards for which shard node would keep tracking headers
	// (in this case this number is equal with: self shard + metachain)
	numShardsToKeepHeaders := 2
	capacity := headersPool.MaxSize() * numShardsToKeepHeaders
	log.Debug("pools info",
		"total headers", headersPool.Len(),
		"headers pool capacity", capacity,
		"total miniblocks", miniBlocksPool.Len(),
		"miniblocks pool capacity", miniBlocksPool.MaxSize(),
	)

	sp.displayMiniBlocksPool()
}

func (sp *shardProcessor) updateState(headers []data.HeaderHandler, currentHeader data.ShardHeaderHandler) {
	for _, header := range headers {
		if sp.forkDetector.GetHighestFinalBlockNonce() < header.GetNonce() {
			break
		}

		prevHeaderHash := header.GetPrevHash()
		prevHeader, errNotCritical := process.GetShardHeader(
			prevHeaderHash,
			sp.dataPool.Headers(),
			sp.marshalizer,
			sp.store,
		)
		if errNotCritical != nil {
			log.Debug("could not get shard header from storage")
			return
		}
		if header.IsStartOfEpochBlock() {
			sp.nodesCoordinator.ShuffleOutForEpoch(header.GetEpoch())
		}

		headerHash, err := core.CalculateHash(sp.marshalizer, sp.hasher, header)
		if err != nil {
			log.Debug("updateState.CalculateHash", "error", err.Error())
			return
		}

		headerRootHashForPruning := header.GetRootHash()
		prevHeaderRootHashForPruning := prevHeader.GetRootHash()

		log.Trace("updateState: prevHeader",
			"shard", prevHeader.GetShardID(),
			"epoch", prevHeader.GetEpoch(),
			"round", prevHeader.GetRound(),
			"nonce", prevHeader.GetNonce(),
			"root hash", prevHeader.GetRootHash(),
		)

		log.Trace("updateState: currHeader",
			"shard", header.GetShardID(),
			"epoch", header.GetEpoch(),
			"round", header.GetRound(),
			"nonce", header.GetNonce(),
			"root hash", header.GetRootHash(),
		)

		sp.updateStateStorage(
			header,
			headerRootHashForPruning,
			prevHeaderRootHashForPruning,
			sp.accountsDB[state.UserAccountsState],
			sp.userStatePruningQueue,
		)

		sp.setFinalizedHeaderHashInIndexer(header.GetPrevHash())
		sp.blockChain.SetFinalBlockInfo(header.GetNonce(), headerHash, header.GetRootHash())
	}
}

// CreateNewHeader creates a new header
func (sp *shardProcessor) CreateNewHeader(round uint64, nonce uint64) (data.HeaderHandler, error) {
	sp.roundNotifier.CheckRound(round)
	epoch := sp.epochStartTrigger.MetaEpoch()
	header := sp.versionedHeaderFactory.Create(epoch)

	shardHeader, ok := header.(data.ShardHeaderHandler)
	if !ok {
		return nil, process.ErrWrongTypeAssertion
	}

	err := shardHeader.SetRound(round)
	if err != nil {
		return nil, err
	}

	err = shardHeader.SetNonce(nonce)
	if err != nil {
		return nil, err
	}

	err = shardHeader.SetAccumulatedFees(big.NewInt(0))
	if err != nil {
		return nil, err
	}

	err = shardHeader.SetDeveloperFees(big.NewInt(0))
	if err != nil {
		return nil, err
	}

	err = sp.setHeaderVersionData(shardHeader)
	if err != nil {
		return nil, err
	}

	return header, nil
}

func (sp *shardProcessor) createMiniBlocks(haveTime func() bool, randomness []byte) (*block.Body, map[string]*processedMb.ProcessedMiniBlockInfo, error) {
	var miniBlocks block.MiniBlockSlice
	processedMiniBlocksDestMeInfo := make(map[string]*processedMb.ProcessedMiniBlockInfo)

	if sp.flagScheduledMiniBlocks.IsSet() {
		miniBlocks = sp.scheduledTxsExecutionHandler.GetScheduledMiniBlocks()
		sp.txCoordinator.AddTxsFromMiniBlocks(miniBlocks)

		scheduledIntermediateTxs := sp.scheduledTxsExecutionHandler.GetScheduledIntermediateTxs()
		sp.txCoordinator.AddTransactions(scheduledIntermediateTxs[block.InvalidBlock], block.TxBlock)
	}

	// placeholder for shardProcessor.createMiniBlocks script

	if sp.accountsDB[state.UserAccountsState].JournalLen() != 0 {
		log.Error("shardProcessor.createMiniBlocks",
			"error", process.ErrAccountStateDirty,
			"stack", string(sp.accountsDB[state.UserAccountsState].GetStackDebugFirstEntry()))

		interMBs := sp.txCoordinator.CreatePostProcessMiniBlocks()
		if len(interMBs) > 0 {
			miniBlocks = append(miniBlocks, interMBs...)
		}

		log.Debug("creating mini blocks has been finished", "num miniblocks", len(miniBlocks))
		return &block.Body{MiniBlocks: miniBlocks}, processedMiniBlocksDestMeInfo, nil
	}

	if !haveTime() {
		log.Debug("shardProcessor.createMiniBlocks", "error", process.ErrTimeIsOut)

		interMBs := sp.txCoordinator.CreatePostProcessMiniBlocks()
		if len(interMBs) > 0 {
			miniBlocks = append(miniBlocks, interMBs...)
		}

		log.Debug("creating mini blocks has been finished", "num miniblocks", len(miniBlocks))
		return &block.Body{MiniBlocks: miniBlocks}, processedMiniBlocksDestMeInfo, nil
	}

	startTime := time.Now()
	mbsFromMe := sp.txCoordinator.CreateMbsAndProcessTransactionsFromMe(haveTime, randomness)
	elapsedTime := time.Since(startTime)
	log.Debug("elapsed time to create mbs from me", "time", elapsedTime)

	if len(mbsFromMe) > 0 {
		miniBlocks = append(miniBlocks, mbsFromMe...)

		numTxs := 0
		for _, mb := range mbsFromMe {
			numTxs += len(mb.TxHashes)
		}

		log.Debug("processed miniblocks and txs from self shard",
			"num miniblocks", len(mbsFromMe),
			"num txs", numTxs)
	}

	log.Debug("creating mini blocks has been finished", "num miniblocks", len(miniBlocks))
	return &block.Body{MiniBlocks: miniBlocks}, processedMiniBlocksDestMeInfo, nil
}

// applyBodyToHeader creates a miniblock header list given a block body
func (sp *shardProcessor) applyBodyToHeader(
	shardHeader data.ShardHeaderHandler,
	body *block.Body,
) (*block.Body, error) {
	sw := core.NewStopWatch()
	sw.Start("applyBodyToHeader")
	defer func() {
		sw.Stop("applyBodyToHeader")
		log.Debug("measurements", sw.GetMeasurements()...)
	}()

	var err error
	err = shardHeader.SetMiniBlockHeaderHandlers(nil)
	if err != nil {
		return nil, err
	}

	err = shardHeader.SetRootHash(sp.getRootHash())
	if err != nil {
		return nil, err
	}

	if check.IfNil(body) {
		return nil, process.ErrNilBlockBody
	}

	var receiptsHash []byte
	sw.Start("CreateReceiptsHash")
	receiptsHash, err = sp.txCoordinator.CreateReceiptsHash()
	sw.Stop("CreateReceiptsHash")
	if err != nil {
		return nil, err
	}

	err = shardHeader.SetReceiptsHash(receiptsHash)
	if err != nil {
		return nil, err
	}

	newBody := deleteSelfReceiptsMiniBlocks(body)

	sw.Start("createMiniBlockHeaders")
	totalTxCount, miniBlockHeaderHandlers, err := sp.createMiniBlockHeaderHandlers(newBody)
	sw.Stop("createMiniBlockHeaders")
	if err != nil {
		return nil, err
	}

	err = shardHeader.SetMiniBlockHeaderHandlers(miniBlockHeaderHandlers)
	if err != nil {
		return nil, err
	}

	err = shardHeader.SetTxCount(uint32(totalTxCount))
	if err != nil {
		return nil, err
	}

	err = shardHeader.SetAccumulatedFees(sp.feeHandler.GetAccumulatedFees())
	if err != nil {
		return nil, err
	}

	err = shardHeader.SetDeveloperFees(sp.feeHandler.GetDeveloperFees())
	if err != nil {
		return nil, err
	}

	err = sp.txCoordinator.VerifyCreatedMiniBlocks(shardHeader, newBody)
	if err != nil {
		return nil, err
	}

	sp.appStatusHandler.SetUInt64Value(common.MetricNumTxInBlock, uint64(totalTxCount))
	sp.appStatusHandler.SetUInt64Value(common.MetricNumMiniBlocks, uint64(len(body.MiniBlocks)))

	marshalizedBody, err := sp.marshalizer.Marshal(newBody)
	if err != nil {
		return nil, err
	}
	sp.blockSizeThrottler.Add(shardHeader.GetRound(), uint32(len(marshalizedBody)))

	return newBody, nil
}

// MarshalizedDataToBroadcast prepares underlying data into a marshalized object according to destination
func (sp *shardProcessor) MarshalizedDataToBroadcast(
	header data.HeaderHandler,
	bodyHandler data.BodyHandler,
) (map[uint32][]byte, map[string][][]byte, error) {

	if check.IfNil(bodyHandler) {
		return nil, nil, process.ErrNilMiniBlocks
	}

	body, ok := bodyHandler.(*block.Body)
	if !ok {
		return nil, nil, process.ErrWrongTypeAssertion
	}

	// Remove mini blocks which are not final from "body" to avoid sending them cross shard
	newBodyToBroadcast, err := sp.getFinalMiniBlocks(header, body)
	if err != nil {
		return nil, nil, err
	}

	mrsTxs := sp.txCoordinator.CreateMarshalizedData(newBodyToBroadcast)

	bodies := make(map[uint32]block.MiniBlockSlice)
	for _, miniBlock := range newBodyToBroadcast.MiniBlocks {
		if miniBlock.SenderShardID != sp.shardCoordinator.SelfId() ||
			miniBlock.ReceiverShardID == sp.shardCoordinator.SelfId() {
			continue
		}
		bodies[miniBlock.ReceiverShardID] = append(bodies[miniBlock.ReceiverShardID], miniBlock)
	}

	mrsData := make(map[uint32][]byte, len(bodies))
	for shardId, subsetBlockBody := range bodies {
		bodyForShard := block.Body{MiniBlocks: subsetBlockBody}
		buff, errMarshal := sp.marshalizer.Marshal(&bodyForShard)
		if errMarshal != nil {
			log.Error("shardProcessor.MarshalizedDataToBroadcast.Marshal", "error", errMarshal.Error())
			continue
		}
		mrsData[shardId] = buff
	}

	return mrsData, mrsTxs, nil
}

// IsInterfaceNil returns true if there is no value under the interface
func (sp *shardProcessor) IsInterfaceNil() bool {
	return sp == nil
}

// GetBlockBodyFromPool returns block body from pool for a given header
func (sp *shardProcessor) GetBlockBodyFromPool(headerHandler data.HeaderHandler) (data.BodyHandler, error) {
	miniBlocksPool := sp.dataPool.MiniBlocks()
	var miniBlocks block.MiniBlockSlice

	for _, mbHeader := range headerHandler.GetMiniBlockHeaderHandlers() {
		obj, hashInPool := miniBlocksPool.Get(mbHeader.GetHash())
		if !hashInPool {
			continue
		}

		miniBlock, typeOk := obj.(*block.MiniBlock)
		if !typeOk {
			return nil, process.ErrWrongTypeAssertion
		}

		miniBlocks = append(miniBlocks, miniBlock)
	}

	return &block.Body{MiniBlocks: miniBlocks}, nil
}

func (sp *shardProcessor) getBootstrapHeadersInfo(
	selfNotarizedHeaders []data.HeaderHandler,
	selfNotarizedHeadersHashes [][]byte,
) []bootstrapStorage.BootstrapHeaderInfo {

	numSelfNotarizedHeaders := len(selfNotarizedHeaders)

	highestNonceInSelfNotarizedHeaders := uint64(0)
	if numSelfNotarizedHeaders > 0 {
		highestNonceInSelfNotarizedHeaders = selfNotarizedHeaders[numSelfNotarizedHeaders-1].GetNonce()
	}

	isFinalNonceHigherThanSelfNotarized := sp.forkDetector.GetHighestFinalBlockNonce() > highestNonceInSelfNotarizedHeaders
	if isFinalNonceHigherThanSelfNotarized {
		numSelfNotarizedHeaders++
	}

	if numSelfNotarizedHeaders == 0 {
		return nil
	}

	lastSelfNotarizedHeaders := make([]bootstrapStorage.BootstrapHeaderInfo, 0, numSelfNotarizedHeaders)

	for index := range selfNotarizedHeaders {
		headerInfo := bootstrapStorage.BootstrapHeaderInfo{
			ShardId: selfNotarizedHeaders[index].GetShardID(),
			Nonce:   selfNotarizedHeaders[index].GetNonce(),
			Hash:    selfNotarizedHeadersHashes[index],
		}

		lastSelfNotarizedHeaders = append(lastSelfNotarizedHeaders, headerInfo)
	}

	if isFinalNonceHigherThanSelfNotarized {
		headerInfo := bootstrapStorage.BootstrapHeaderInfo{
			ShardId: sp.shardCoordinator.SelfId(),
			Nonce:   sp.forkDetector.GetHighestFinalBlockNonce(),
			Hash:    sp.forkDetector.GetHighestFinalBlockHash(),
		}

		lastSelfNotarizedHeaders = append(lastSelfNotarizedHeaders, headerInfo)
	}

	return lastSelfNotarizedHeaders
}

// Close - closes all underlying components
func (sp *shardProcessor) Close() error {
	return sp.baseProcessor.Close()
}

// DecodeBlockHeader method decodes block header from a given byte array
func (sp *shardProcessor) DecodeBlockHeader(dta []byte) data.HeaderHandler {
	if dta == nil {
		return nil
	}

	header, err := process.CreateShardHeader(sp.marshalizer, dta)
	if err != nil {
		log.Debug("DecodeBlockHeader.CreateShardHeader", "error", err.Error())
		return nil
	}

	return header
}
