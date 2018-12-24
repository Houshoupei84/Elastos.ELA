package blockhistory

import (
	"errors"

	"github.com/elastos/Elastos.ELA/blockchain"
	"github.com/elastos/Elastos.ELA/core/types"
	"github.com/elastos/Elastos.ELA/version"

	"github.com/elastos/Elastos.ELA/common"
)

var originalArbitrators = []string{
//	"0248df6705a909432be041e0baa25b8f648741018f70d1911f2ed28778db4b8fe4",
//	"02771faf0f4d4235744b30972d5f2c470993920846c761e4d08889ecfdc061cddf",
//	"0342196610e57d75ba3afa26e030092020aec56822104e465cba1d8f69f8d83c8e",
//	"02fa3e0d14e0e93ca41c3c0f008679e417cf2adb6375dd4bbbee9ed8e8db606a56",
//	"03ab3ecd1148b018d480224520917c6c3663a3631f198e3b25cf4c9c76786b7850",

	"03e333657c788a20577c0288559bd489ee65514748d18cb1dc7560ae4ce3d45613",
	"02dd22722c3b3a284929e4859b07e6a706595066ddd2a0b38e5837403718fb047c",
	"03e4473b918b499e4112d281d805fc8d8ae7ac0a71ff938cba78006bf12dd90a85",
	"03dd66833d28bac530ca80af0efbfc2ec43b4b87504a41ab4946702254e7f48961",
	"02c8a87c076112a1b344633184673cfb0bb6bce1aca28c78986a7b1047d257a448",
}

type BlockVersionV0 struct {
	version.BlockVersionMain
}

func (b *BlockVersionV0) GetVersion() uint32 {
	return 0
}

func (b *BlockVersionV0) GetNextOnDutyArbitrator(dutyChangedCount, offset uint32) []byte {
	arbitrators, _ := b.GetProducersDesc()
	height := blockchain.DefaultLedger.Store.GetHeight()
	index := (height + offset) % uint32(len(arbitrators))
	arbitrator := arbitrators[index]

	return arbitrator
}

func (b *BlockVersionV0) CheckConfirmedBlockOnFork(block *types.Block) error {
	return nil
}

func (b *BlockVersionV0) GetProducersDesc() ([][]byte, error) {
	if len(originalArbitrators) == 0 {
		return nil, errors.New("arbiters not configured")
	}

	arbitersByte := make([][]byte, 0)
	for _, arbiter := range originalArbitrators {
		arbiterByte, err := common.HexStringToBytes(arbiter)
		if err != nil {
			return nil, err
		}
		arbitersByte = append(arbitersByte, arbiterByte)
	}

	return arbitersByte, nil
}

func (b *BlockVersionV0) AddDposBlock(dposBlock *types.DposBlock) (bool, bool, error) {
	return blockchain.DefaultLedger.Blockchain.AddBlock(dposBlock.Block)
}

func (b *BlockVersionV0) AssignCoinbaseTxRewards(block *types.Block, totalReward common.Fixed64) error {
	// PoW miners and DPoS are each equally allocated 35%. The remaining 30% goes to the Cyber Republic fund
	rewardCyberRepublic := common.Fixed64(float64(totalReward) * 0.3)
	rewardMergeMiner := common.Fixed64(float64(totalReward) * 0.35)
	rewardDposArbiter := common.Fixed64(totalReward) - rewardCyberRepublic - rewardMergeMiner
	block.Transactions[0].Outputs[0].Value = rewardCyberRepublic
	block.Transactions[0].Outputs[1].Value = rewardMergeMiner
	block.Transactions[0].Outputs = append(block.Transactions[0].Outputs, &types.Output{
		AssetID:     blockchain.DefaultLedger.Blockchain.AssetID,
		Value:       rewardDposArbiter,
		ProgramHash: blockchain.FoundationAddress,
	})

	return nil
}
