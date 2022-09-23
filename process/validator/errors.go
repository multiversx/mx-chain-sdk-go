package validator

import (
	"errors"
)

// ErrNilPubKey signals that the public key is nil
var ErrNilPubKey = errors.New("nil public key")

// ErrInvalidNumberPubKeys signals that an invalid number of public keys was used
var ErrInvalidNumberPubKeys = errors.New("invalid number of public keys")

// ErrSmallShardEligibleListSize signals that the eligible validators list's size is less than the consensus size
var ErrSmallShardEligibleListSize = errors.New("small shard eligible list size")

// ErrInvalidConsensusGroupSize signals that the consensus size is invalid (e.g. value is negative)
var ErrInvalidConsensusGroupSize = errors.New("invalid consensus group size")

// ErrNilRandomness signals that a nil randomness source has been provided
var ErrNilRandomness = errors.New("nil randomness source")

// ErrNilHasher signals that a nil hasher has been provided
var ErrNilHasher = errors.New("nil hasher")

// ErrValidatorNotFound signals that the validator has not been found
var ErrValidatorNotFound = errors.New("validator not found")

// ErrNilCacher signals that a nil cacher has been provided
var ErrNilCacher = errors.New("nil cacher")

// ErrNilNodeStopChannel signals that a nil node stop channel has been provided
var ErrNilNodeStopChannel = errors.New("nil node stop channel")

// ErrNilNodeTypeProvider signals that a nil node type provider has been given
var ErrNilNodeTypeProvider = errors.New("nil node type provider")
