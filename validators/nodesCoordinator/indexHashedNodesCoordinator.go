package nodesCoordinator

import (
	"bytes"
	"fmt"
	"github.com/pkg/errors"
	"sync"

	"github.com/ElrondNetwork/elrond-go-core/core"
	"github.com/ElrondNetwork/elrond-go-core/core/check"
	"github.com/ElrondNetwork/elrond-go-core/data/endProcess"
	"github.com/ElrondNetwork/elrond-go-core/hashing"
	logger "github.com/ElrondNetwork/elrond-go-logger"
	"github.com/ElrondNetwork/elrond-go/common"
	"github.com/ElrondNetwork/elrond-go/sharding/nodesCoordinator"
)

const (
	keyFormat = "%s_%v"
)

// ArgNodesCoordinator holds all dependencies required by the nodes coordinator in order to create new instances
type ArgNodesCoordinator struct {
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

// NewIndexHashedNodesCoordinator creates a new index hashed group selector
func NewIndexHashedNodesCoordinator(arguments ArgNodesCoordinator) (*indexHashedNodesCoordinator, error) {
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

func checkArguments(arguments ArgNodesCoordinator) error {
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

	consensusSize := i.ConsensusGroupSize()
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
func (i *indexHashedNodesCoordinator) GetValidatorWithPublicKey(publicKey []byte) (nodesCoordinator.Validator, error) {
	if len(publicKey) == 0 {
		return nil, ErrNilPubKey
	}
	i.mutNodesConfig.RLock()
	v, ok := i.publicKeyToValidatorMap[string(publicKey)]
	i.mutNodesConfig.RUnlock()
	if ok {
		return v, nil
	}

	return nil, ErrValidatorNotFound
}

// GetConsensusValidatorsPublicKeys calculates the validators consensus group for a specific shard, randomness and round number,
// returning their public keys
func (i *indexHashedNodesCoordinator) GetConsensusValidatorsPublicKeys(
	randomness []byte,
	round uint64,
) ([]string, error) {
	consensusNodes, err := i.ComputeConsensusGroup(randomness, round)
	if err != nil {
		return nil, err
	}

	pubKeys := make([]string, 0)

	for _, v := range consensusNodes {
		pubKeys = append(pubKeys, string(v.PubKey()))
	}

	return pubKeys, nil
}

// GetAllEligibleValidatorsPublicKeys will return all validators public keys
func (i *indexHashedNodesCoordinator) GetAllEligibleValidatorsPublicKeys() ([][]byte, error) {
	validatorsPubKeys := make([][]byte, 0)

	i.mutNodesConfig.RLock()
	for _, node := range i.eligibleNodes {
		validatorsPubKeys = append(validatorsPubKeys, node.PubKey())
	}
	i.mutNodesConfig.RUnlock()

	return validatorsPubKeys, nil
}

// GetValidatorsIndexes will return validators indexes for a block
func (i *indexHashedNodesCoordinator) GetValidatorsIndexes(
	publicKeys []string,
) ([]uint64, error) {
	signersIndexes := make([]uint64, 0)

	validatorsPubKeys, err := i.GetAllEligibleValidatorsPublicKeys()
	if err != nil {
		return nil, err
	}

	for _, pubKey := range publicKeys {
		for index, value := range validatorsPubKeys {
			if bytes.Equal([]byte(pubKey), value) {
				signersIndexes = append(signersIndexes, uint64(index))
			}
		}
	}

	if len(publicKeys) != len(signersIndexes) {
		strHaving := "having the following keys: \n"
		for index, value := range validatorsPubKeys {
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
	displayValidatorsForRandomness(consensusGroup, randomness)

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
func (i *indexHashedNodesCoordinator) ConsensusGroupSize() int {
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

// IsInterfaceNil returns true if there is no value under the interface
func (i *indexHashedNodesCoordinator) IsInterfaceNil() bool {
	return i == nil
}
