package shard

import (
	"fmt"

	"github.com/multiversx/mx-chain-core-go/core"
	"github.com/multiversx/mx-chain-core-go/core/check"
	"github.com/multiversx/mx-chain-core-go/hashing"
	"github.com/multiversx/mx-chain-core-go/marshal"
	"github.com/multiversx/mx-chain-go/common/forking"
	"github.com/multiversx/mx-chain-go/config"
	"github.com/multiversx/mx-chain-go/process"
	"github.com/multiversx/mx-chain-go/process/factory"
	"github.com/multiversx/mx-chain-go/process/factory/containers"
	"github.com/multiversx/mx-chain-go/process/smartContract/hooks"
	"github.com/multiversx/mx-chain-go/sharding"
	"github.com/multiversx/mx-chain-go/sharding/nodesCoordinator"
	"github.com/multiversx/mx-chain-go/state"
	"github.com/multiversx/mx-chain-go/vm"
	systemVMFactory "github.com/multiversx/mx-chain-go/vm/factory"
	systemVMProcess "github.com/multiversx/mx-chain-go/vm/process"
	"github.com/multiversx/mx-chain-go/vm/systemSmartContracts"
	logger "github.com/multiversx/mx-chain-logger-go"
	vmcommon "github.com/multiversx/mx-chain-vm-common-go"
	"github.com/multiversx/mx-chain-vm-common-go/parsers"
	arwen14 "github.com/multiversx/mx-chain-vm-v1_4-go/arwen"
	arwenHost14 "github.com/multiversx/mx-chain-vm-v1_4-go/arwen/host"
)

var _ process.VirtualMachinesContainerFactory = (*vmContainerFactory)(nil)

var logVMContainerFactory = logger.GetOrCreate("vmContainerFactory")

type vmContainerFactory struct {
	config             config.VirtualMachineConfig
	blockChainHook     process.BlockChainHookHandler
	cryptoHook         vmcommon.CryptoHook
	blockGasLimit      uint64
	gasSchedule        core.GasScheduleNotifier
	builtinFunctions   vmcommon.BuiltInFunctionContainer
	container          process.VirtualMachinesContainer
	esdtTransferParser vmcommon.ESDTTransferParser

	chanceComputer         nodesCoordinator.ChanceComputer
	validatorAccountsDB    state.AccountsAdapter
	systemContracts        vm.SystemSCContainer
	economics              process.EconomicsDataHandler
	messageSigVerifier     vm.MessageSignVerifier
	nodesConfigProvider    vm.NodesConfigProvider
	hasher                 hashing.Hasher
	marshalizer            marshal.Marshalizer
	systemSCConfig         *config.SystemSmartContractsConfig
	addressPubKeyConverter core.PubkeyConverter
	scFactory              vm.SystemSCContainerFactory
}

// ArgVMContainerFactory defines the arguments needed to the new VM factory
type ArgVMContainerFactory struct {
	Config              config.VirtualMachineConfig
	BlockGasLimit       uint64
	GasSchedule         core.GasScheduleNotifier
	ESDTTransferParser  vmcommon.ESDTTransferParser
	BuiltInFunctions    vmcommon.BuiltInFunctionContainer
	BlockChainHook      process.BlockChainHookHandler
	Economics           process.EconomicsDataHandler
	MessageSignVerifier vm.MessageSignVerifier
	NodesConfigProvider vm.NodesConfigProvider
	Hasher              hashing.Hasher
	Marshalizer         marshal.Marshalizer
	SystemSCConfig      *config.SystemSmartContractsConfig
	ValidatorAccountsDB state.AccountsAdapter
	ChanceComputer      nodesCoordinator.ChanceComputer
	PubkeyConv          core.PubkeyConverter
}

// NewVMContainerFactory is responsible for creating a new virtual machine factory object
func NewVMContainerFactory(args ArgVMContainerFactory) (*vmContainerFactory, error) {
	if check.IfNil(args.GasSchedule) {
		return nil, process.ErrNilGasSchedule
	}
	if check.IfNil(args.ESDTTransferParser) {
		return nil, process.ErrNilESDTTransferParser
	}
	if check.IfNil(args.BuiltInFunctions) {
		return nil, process.ErrNilBuiltInFunction
	}
	if check.IfNil(args.BlockChainHook) {
		return nil, process.ErrNilBlockChainHook
	}
	if check.IfNil(args.Economics) {
		return nil, fmt.Errorf("%w in NewVMContainerFactory", process.ErrNilEconomicsData)
	}
	if check.IfNil(args.MessageSignVerifier) {
		return nil, fmt.Errorf("%w in NewVMContainerFactory", process.ErrNilKeyGen)
	}
	if check.IfNil(args.NodesConfigProvider) {
		return nil, fmt.Errorf("%w in NewVMContainerFactory", process.ErrNilNodesConfigProvider)
	}
	if check.IfNil(args.Hasher) {
		return nil, fmt.Errorf("%w in NewVMContainerFactory", process.ErrNilHasher)
	}
	if check.IfNil(args.Marshalizer) {
		return nil, fmt.Errorf("%w in NewVMContainerFactory", process.ErrNilMarshalizer)
	}
	if args.SystemSCConfig == nil {
		return nil, fmt.Errorf("%w in NewVMContainerFactory", process.ErrNilSystemSCConfig)
	}
	if check.IfNil(args.ValidatorAccountsDB) {
		return nil, fmt.Errorf("%w in NewVMContainerFactory", vm.ErrNilValidatorAccountsDB)
	}
	if check.IfNil(args.ChanceComputer) {
		return nil, fmt.Errorf("%w in NewVMContainerFactory", vm.ErrNilChanceComputer)
	}
	if check.IfNil(args.GasSchedule) {
		return nil, fmt.Errorf("%w in NewVMContainerFactory", vm.ErrNilGasSchedule)
	}
	if check.IfNil(args.PubkeyConv) {
		return nil, fmt.Errorf("%w in NewVMContainerFactory", vm.ErrNilAddressPubKeyConverter)
	}
	if check.IfNil(args.BlockChainHook) {
		return nil, process.ErrNilBlockChainHook
	}

	cryptoHook := hooks.NewVMCryptoHook()

	vmf := &vmContainerFactory{
		config:                 args.Config,
		blockChainHook:         args.BlockChainHook,
		cryptoHook:             cryptoHook,
		blockGasLimit:          args.BlockGasLimit,
		gasSchedule:            args.GasSchedule,
		builtinFunctions:       args.BuiltInFunctions,
		container:              nil,
		esdtTransferParser:     args.ESDTTransferParser,
		economics:              args.Economics,
		messageSigVerifier:     args.MessageSignVerifier,
		nodesConfigProvider:    args.NodesConfigProvider,
		hasher:                 args.Hasher,
		marshalizer:            args.Marshalizer,
		systemSCConfig:         args.SystemSCConfig,
		validatorAccountsDB:    args.ValidatorAccountsDB,
		chanceComputer:         args.ChanceComputer,
		addressPubKeyConverter: args.PubkeyConv,
	}

	return vmf, nil
}

// Create sets up all the needed virtual machine returning a container of all the VMs
func (vmf *vmContainerFactory) Create() (process.VirtualMachinesContainer, error) {
	container := containers.NewVirtualMachinesContainer()

	currentVM, err := vmf.createArwenVM()
	if err != nil {
		return nil, err
	}

	err = container.Add(factory.ArwenVirtualMachine, currentVM)
	if err != nil {
		return nil, err
	}

	currVm, err := vmf.createSystemVM()
	if err != nil {
		return nil, err
	}

	err = container.Add(factory.SystemVirtualMachine, currVm)
	if err != nil {
		return nil, err
	}

	// The vmContainerFactory keeps a reference to the container it has created,
	// in order to replace, from within the container, the VM instances that
	// become out-of-date after specific epochs.
	vmf.container = container
	vmf.gasSchedule.RegisterNotifyHandler(vmf)

	return container, nil
}

// Close closes the vm container factory
func (vmf *vmContainerFactory) Close() error {
	errContainer := vmf.container.Close()
	if errContainer != nil {
		logVMContainerFactory.Error("cannot close container", "error", errContainer)
	}

	errBlockchain := vmf.blockChainHook.Close()
	if errBlockchain != nil {
		logVMContainerFactory.Error("cannot close blockchain hook implementation", "error", errBlockchain)
	}

	if errContainer != nil {
		return errContainer
	}

	return errBlockchain
}

// GasScheduleChange updates the gas schedule map in all the components
func (vmf *vmContainerFactory) GasScheduleChange(gasSchedule map[string]map[string]uint64) {
	// clear compiled codes always before changing gasSchedule
	vmf.blockChainHook.ClearCompiledCodes()
	for _, key := range vmf.container.Keys() {
		currentVM, err := vmf.container.Get(key)
		if err != nil {
			logVMContainerFactory.Error("cannot get VM on GasSchedule Change", "error", err)
			continue
		}

		currentVM.GasScheduleChange(gasSchedule)
	}
}

func (vmf *vmContainerFactory) createArwenVM() (vmcommon.VMExecutionHandler, error) {
	currentVM, err := vmf.createInProcessArwenVMV14()
	if err != nil {
		return nil, err
	}

	return currentVM, nil
}

func (vmf *vmContainerFactory) createInProcessArwenVMV14() (vmcommon.VMExecutionHandler, error) {
	hostParameters := &arwen14.VMHostParameters{
		VMType:                              factory.ArwenVirtualMachine,
		BlockGasLimit:                       vmf.blockGasLimit,
		GasSchedule:                         vmf.gasSchedule.LatestGasSchedule(),
		BuiltInFuncContainer:                vmf.builtinFunctions,
		ProtectedKeyPrefix:                  []byte(core.ProtectedKeyPrefix),
		ESDTTransferParser:                  vmf.esdtTransferParser,
		EpochNotifier:                       forking.NewGenericEpochNotifier(),
		WasmerSIGSEGVPassthrough:            vmf.config.WasmerSIGSEGVPassthrough,
		TimeOutForSCExecutionInMilliseconds: vmf.config.TimeOutForSCExecutionInMilliseconds,
	}
	return arwenHost14.NewArwenVM(vmf.blockChainHook, hostParameters)
}

func (vmf *vmContainerFactory) createSystemVMFactoryAndEEI() (vm.SystemSCContainerFactory, vm.ContextHandler, error) {
	atArgumentParser := parsers.NewCallArgsParser()
	systemEI, err := systemSmartContracts.NewVMContext(
		vmf.blockChainHook,
		vmf.cryptoHook,
		atArgumentParser,
		vmf.validatorAccountsDB,
		vmf.chanceComputer,
	)
	if err != nil {
		return nil, nil, err
	}

	argsNewSystemScFactory := systemVMFactory.ArgsNewSystemSCFactory{
		SystemEI:               systemEI,
		SigVerifier:            vmf.messageSigVerifier,
		GasSchedule:            vmf.gasSchedule,
		NodesConfigProvider:    vmf.nodesConfigProvider,
		Hasher:                 vmf.hasher,
		Marshalizer:            vmf.marshalizer,
		SystemSCConfig:         vmf.systemSCConfig,
		Economics:              vmf.economics,
		EpochNotifier:          forking.NewGenericEpochNotifier(),
		AddressPubKeyConverter: vmf.addressPubKeyConverter,
		EpochConfig:            &config.EpochConfig{},
		ShardCoordinator:       &sharding.OneShardCoordinator{},
	}
	scFactory, err := systemVMFactory.NewSystemSCFactory(argsNewSystemScFactory)
	if err != nil {
		return nil, nil, err
	}

	return scFactory, systemEI, nil
}

func (vmf *vmContainerFactory) finalizeSystemVMCreation(systemEI vm.ContextHandler) (vmcommon.VMExecutionHandler, error) {
	err := systemEI.SetSystemSCContainer(vmf.systemContracts)
	if err != nil {
		return nil, err
	}

	argsNewSystemVM := systemVMProcess.ArgsNewSystemVM{
		SystemEI:        systemEI,
		SystemContracts: vmf.systemContracts,
		VmType:          factory.SystemVirtualMachine,
		GasSchedule:     vmf.gasSchedule,
	}
	systemVM, err := systemVMProcess.NewSystemVM(argsNewSystemVM)
	if err != nil {
		return nil, err
	}

	vmf.gasSchedule.RegisterNotifyHandler(systemVM)

	return systemVM, nil
}

func (vmf *vmContainerFactory) createSystemVM() (vmcommon.VMExecutionHandler, error) {
	scFactory, systemEI, err := vmf.createSystemVMFactoryAndEEI()
	if err != nil {
		return nil, err
	}

	vmf.systemContracts, err = scFactory.Create()
	if err != nil {
		return nil, err
	}

	vmf.scFactory = scFactory

	return vmf.finalizeSystemVMCreation(systemEI)
}

// BlockChainHookImpl returns the created blockChainHookImpl
func (vmf *vmContainerFactory) BlockChainHookImpl() process.BlockChainHookHandler {
	return vmf.blockChainHook
}

// SystemSmartContractContainer return the created system smart contracts
func (vmf *vmContainerFactory) SystemSmartContractContainer() vm.SystemSCContainer {
	return vmf.systemContracts
}

// SystemSmartContractContainerFactory returns the system smart contract container factory
func (vmf *vmContainerFactory) SystemSmartContractContainerFactory() vm.SystemSCContainerFactory {
	return vmf.scFactory
}

// IsInterfaceNil returns true if there is no value under the interface
func (vmf *vmContainerFactory) IsInterfaceNil() bool {
	return vmf == nil
}
