package validator

import (
	"bytes"
	"fmt"
	"sync"

	"github.com/multiversx/mx-chain-core-go/core"
	"github.com/multiversx/mx-chain-core-go/core/check"
	"github.com/multiversx/mx-chain-core-go/data/endProcess"
	"github.com/multiversx/mx-chain-core-go/hashing"
	"github.com/multiversx/mx-chain-go/common"
	"github.com/multiversx/mx-chain-go/sharding/nodesCoordinator"
	"github.com/multiversx/mx-chain-go/state"
	logger "github.com/multiversx/mx-chain-logger-go"
	"github.com/pkg/errors"
)

const (
	keyFormat = "%s_%v"
)

var log = logger.GetOrCreate("nodesCoordinator")

// ArgSingleShardNodesCoordinator holds all dependencies required by the nodes coordinator in order to create new instances
type ArgSingleShardNodesCoordinator struct {
	ConsensusGroupSize  int
	Hasher              hashing.Hasher
	EligibleNodes       []nodesCoordinator.Validator
	SelfPublicKey       []byte
	ConsensusGroupCache nodesCoordinator.Cacher
	ChanStopNode        chan endProcess.ArgEndProcess
	NodeTypeProvider    nodesCoordinator.NodeTypeProviderHandler
	IsFullArchive       bool
}

type indexHashedNodesCoordinator struct {
	consensusGroupSize      int
	selfPubKey              []byte
	eligibleNodes           []nodesCoordinator.Validator
	selector                nodesCoordinator.RandomSelector
	hasher                  hashing.Hasher
	mutNodesConfig          sync.RWMutex
	consensusGroupCacher    nodesCoordinator.Cacher
	chanStopNode            chan endProcess.ArgEndProcess
	nodeTypeProvider        nodesCoordinator.NodeTypeProviderHandler
	chanceComputer          nodesCoordinator.ChanceComputer
	publicKeyToValidatorMap map[string]nodesCoordinator.Validator
}

// NewSingleShardIndexHashedNodesCoordinator creates a new index hashed group selector
func NewSingleShardIndexHashedNodesCoordinator(arguments ArgSingleShardNodesCoordinator) (*indexHashedNodesCoordinator, error) {
	err := checkArguments(arguments)
	if err != nil {
		return nil, err
	}

	i := &indexHashedNodesCoordinator{
		hasher:               arguments.Hasher,
		selfPubKey:           arguments.SelfPublicKey,
		consensusGroupSize:   arguments.ConsensusGroupSize,
		consensusGroupCacher: arguments.ConsensusGroupCache,
		chanStopNode:         arguments.ChanStopNode,
		nodeTypeProvider:     arguments.NodeTypeProvider,
		eligibleNodes:        arguments.EligibleNodes,
	}

	err = i.createSelector()
	if err != nil {
		return nil, err
	}

	i.publicKeyToValidatorMap = make(map[string]nodesCoordinator.Validator)
	for _, node := range i.eligibleNodes {
		i.publicKeyToValidatorMap[string(node.PubKey())] = node
	}

	return i, nil
}

func checkArguments(arguments ArgSingleShardNodesCoordinator) error {
	if arguments.ConsensusGroupSize < 1 {
		return ErrInvalidConsensusGroupSize
	}
	if check.IfNil(arguments.Hasher) {
		return ErrNilHasher
	}
	if len(arguments.SelfPublicKey) == 0 {
		return ErrNilPubKey
	}
	if check.IfNilReflect(arguments.ConsensusGroupCache) {
		return ErrNilCacher
	}
	if check.IfNil(arguments.NodeTypeProvider) {
		return ErrNilNodeTypeProvider
	}
	if nil == arguments.ChanStopNode {
		return ErrNilNodeStopChannel
	}
	if len(arguments.EligibleNodes) < arguments.ConsensusGroupSize {
		return ErrSmallShardEligibleListSize
	}

	return nil
}

// createSelector creates the consensus group selectors
func (i *indexHashedNodesCoordinator) createSelector() error {
	weights, err := i.ValidatorsWeights(i.eligibleNodes)
	if err != nil {
		return err
	}

	i.selector, err = nodesCoordinator.NewSelectorExpandedList(weights, i.hasher)
	if err != nil {
		return err
	}

	return nil
}

func (i *indexHashedNodesCoordinator) setNodeType(isValidator bool) {
	if isValidator {
		i.nodeTypeProvider.SetType(core.NodeTypeValidator)
		return
	}

	i.nodeTypeProvider.SetType(core.NodeTypeObserver)
}

// ComputeConsensusGroup will generate a list of validators based on the the eligible list
// and each eligible validator weight/chance
func (i *indexHashedNodesCoordinator) ComputeConsensusGroup(
	randomness []byte,
	round uint64,
	_, _ uint32,
) (validatorsGroup []nodesCoordinator.Validator, err error) {
	log.Trace("computing consensus group for",
		"randomness", randomness,
		"round", round)

	if len(randomness) == 0 {
		return nil, ErrNilRandomness
	}

	key := []byte(fmt.Sprintf(keyFormat, string(randomness), round))
	validators := i.searchConsensusForKey(key)
	if validators != nil {
		return validators, nil
	}

	consensusSize := i.ConsensusGroupSize(0)
	randomness = []byte(fmt.Sprintf("%d-%s", round, randomness))

	log.Trace("computeValidatorsGroup",
		"randomness", randomness,
		"consensus size", consensusSize,
		"round", round)

	tempList, err := i.selectValidators(randomness)
	if err != nil {
		return nil, err
	}

	size := 0
	for _, v := range tempList {
		size += v.Size()
	}

	i.consensusGroupCacher.Put(key, tempList, size)

	return tempList, nil
}

func (i *indexHashedNodesCoordinator) searchConsensusForKey(key []byte) []nodesCoordinator.Validator {
	value, ok := i.consensusGroupCacher.Get(key)
	if ok {
		consensusGroup, typeOk := value.([]nodesCoordinator.Validator)
		if typeOk {
			return consensusGroup
		}
	}
	return nil
}

// GetValidatorWithPublicKey gets the validator with the given public key
func (i *indexHashedNodesCoordinator) GetValidatorWithPublicKey(publicKey []byte) (nodesCoordinator.Validator, uint32, error) {
	if len(publicKey) == 0 {
		return nil, 0, ErrNilPubKey
	}
	i.mutNodesConfig.RLock()
	v, ok := i.publicKeyToValidatorMap[string(publicKey)]
	i.mutNodesConfig.RUnlock()
	if ok {
		return v, 0, nil
	}

	return nil, 0, ErrValidatorNotFound
}

// GetConsensusValidatorsPublicKeys calculates the validators consensus group for a specific shard, randomness and round number,
// returning their public keys
func (i *indexHashedNodesCoordinator) GetConsensusValidatorsPublicKeys(
	randomness []byte,
	round uint64,
	_, _ uint32,
) ([]string, error) {
	consensusNodes, err := i.ComputeConsensusGroup(randomness, round, 0, 0)
	if err != nil {
		return nil, err
	}

	pubKeys := make([]string, 0)

	for _, v := range consensusNodes {
		pubKeys = append(pubKeys, string(v.PubKey()))
	}

	return pubKeys, nil
}

// GetConsensusWhitelistedNodes will return the current whitelisted nodes
func (i *indexHashedNodesCoordinator) GetConsensusWhitelistedNodes(_ uint32) (map[string]struct{}, error) {
	publicKeysNewEpoch, errGetEligible := i.GetAllEligibleValidatorsPublicKeys(0)
	if errGetEligible != nil {
		return nil, errGetEligible
	}

	eligible := make(map[string]struct{})
	for _, pubKey := range publicKeysNewEpoch[0] {
		eligible[string(pubKey)] = struct{}{}
	}

	return eligible, nil
}

// GetAllEligibleValidatorsPublicKeys will return all validators public keys
func (i *indexHashedNodesCoordinator) GetAllEligibleValidatorsPublicKeys(_ uint32) (map[uint32][][]byte, error) {
	validatorsPubKeys := make(map[uint32][][]byte)

	i.mutNodesConfig.RLock()
	validatorsPubKeys[0] = make([][]byte, len(i.eligibleNodes))
	for idx, node := range i.eligibleNodes {
		validatorsPubKeys[0][idx] = node.PubKey()
	}
	i.mutNodesConfig.RUnlock()

	return validatorsPubKeys, nil
}

// GetValidatorsIndexes will return validators indexes for a block
func (i *indexHashedNodesCoordinator) GetValidatorsIndexes(
	publicKeys []string,
	_ uint32,
) ([]uint64, error) {
	signersIndexes := make([]uint64, 0)

	validatorsPubKeys, err := i.GetAllEligibleValidatorsPublicKeys(0)
	if err != nil {
		return nil, err
	}

	for _, pubKey := range publicKeys {
		for index, value := range validatorsPubKeys[0] {
			if bytes.Equal([]byte(pubKey), value) {
				signersIndexes = append(signersIndexes, uint64(index))
			}
		}
	}

	if len(publicKeys) != len(signersIndexes) {
		strHaving := "having the following keys: \n"
		for index, value := range validatorsPubKeys[0] {
			strHaving += fmt.Sprintf(" index %d  key %s\n", index, logger.DisplayByteSlice(value))
		}

		strNeeded := "needed the following keys: \n"
		for _, pubKey := range publicKeys {
			strNeeded += fmt.Sprintf(" key %s\n", logger.DisplayByteSlice([]byte(pubKey)))
		}

		log.Error("public keys not found\n"+strHaving+"\n"+strNeeded+"\n",
			"len pubKeys", len(publicKeys),
			"len signers", len(signersIndexes),
		)

		return nil, ErrInvalidNumberPubKeys
	}

	return signersIndexes, nil
}

// GetChance returns the chance from an actual rating
func (i *indexHashedNodesCoordinator) GetChance(rating uint32) uint32 {
	return i.chanceComputer.GetChance(rating)
}

// ValidatorsWeights returns the weights/chances for each given validator
func (i *indexHashedNodesCoordinator) ValidatorsWeights(validators []nodesCoordinator.Validator) ([]uint32, error) {
	minChance := i.GetChance(0)
	weights := make([]uint32, len(validators))

	for i, validator := range validators {
		weights[i] = validator.Chances()
		if weights[i] < minChance {
			//default weight if all validators need to be selected
			weights[i] = minChance
		}
	}

	return weights, nil
}

func (i *indexHashedNodesCoordinator) selectValidators(
	randomness []byte,
) ([]nodesCoordinator.Validator, error) {
	if len(randomness) == 0 {
		return nil, ErrNilRandomness
	}

	i.mutNodesConfig.RLock()
	defer i.mutNodesConfig.RUnlock()

	selectedIndexes, err := i.selector.Select(randomness, uint32(i.consensusGroupSize))
	if err != nil {
		return nil, err
	}

	consensusGroup := make([]nodesCoordinator.Validator, uint32(i.consensusGroupSize))
	for index := range consensusGroup {
		consensusGroup[index] = i.eligibleNodes[selectedIndexes[index]]
	}

	return consensusGroup, nil
}

// ReplaceNode is called by validatorAccountsDB when changes are happening on the validator list
func (i *indexHashedNodesCoordinator) ReplaceNode(pubKeyToDelete []byte, newValidator nodesCoordinator.Validator) error {
	i.mutNodesConfig.Lock()
	defer i.mutNodesConfig.Unlock()

	delete(i.publicKeyToValidatorMap, string(pubKeyToDelete))
	i.publicKeyToValidatorMap[string(newValidator.PubKey())] = newValidator

	replaced := false
	for index, node := range i.eligibleNodes {
		if bytes.Equal(node.PubKey(), pubKeyToDelete) {
			replaced = true
			i.eligibleNodes[index] = newValidator
		}
	}

	if !replaced {
		return errors.New("node with pubkey not found in eligible list")
	}

	err := i.createSelector()
	if err != nil {
		return err
	}

	return nil
}

// NotifyOrder returns the notification order for a start of epoch event
func (i *indexHashedNodesCoordinator) NotifyOrder() uint32 {
	return common.NodesCoordinatorOrder
}

// ConsensusGroupSize returns the consensus group size for a specific shard
func (i *indexHashedNodesCoordinator) ConsensusGroupSize(_ uint32) int {
	return i.consensusGroupSize
}

// GetNumTotalEligible returns the number of total eligible accross all shards from current setup
func (i *indexHashedNodesCoordinator) GetNumTotalEligible() uint64 {
	return uint64(i.consensusGroupSize)
}

// GetOwnPublicKey will return current node public key  for block sign
func (i *indexHashedNodesCoordinator) GetOwnPublicKey() []byte {
	return i.selfPubKey
}

// GetAllWaitingValidatorsPublicKeys returns empty - as it is not used
func (i *indexHashedNodesCoordinator) GetAllWaitingValidatorsPublicKeys(_ uint32) (map[uint32][][]byte, error) {
	return make(map[uint32][][]byte), nil
}

// GetAllLeavingValidatorsPublicKeys returns empty - as it is not used
func (i *indexHashedNodesCoordinator) GetAllLeavingValidatorsPublicKeys(_ uint32) (map[uint32][][]byte, error) {
	return make(map[uint32][][]byte), nil
}

// ComputeAdditionalLeaving returns empty
func (i *indexHashedNodesCoordinator) ComputeAdditionalLeaving(_ []*state.ShardValidatorInfo) (map[uint32][]nodesCoordinator.Validator, error) {
	return make(map[uint32][]nodesCoordinator.Validator), nil
}

// LoadState -
func (i *indexHashedNodesCoordinator) LoadState(_ []byte) error {
	return nil
}

// GetSavedStateKey -
func (i *indexHashedNodesCoordinator) GetSavedStateKey() []byte {
	return nil
}

// ShardIdForEpoch -
func (i *indexHashedNodesCoordinator) ShardIdForEpoch(_ uint32) (uint32, error) {
	return 0, nil
}

// ShuffleOutForEpoch -
func (i *indexHashedNodesCoordinator) ShuffleOutForEpoch(_ uint32) {
}

// IsInterfaceNil returns true if there is no value under the interface
func (i *indexHashedNodesCoordinator) IsInterfaceNil() bool {
	return i == nil
}
