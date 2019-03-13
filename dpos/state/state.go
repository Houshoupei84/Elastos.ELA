package state

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/elastos/Elastos.ELA/blockchain/interfaces"
	"github.com/elastos/Elastos.ELA/common"
	"github.com/elastos/Elastos.ELA/common/config"
	"github.com/elastos/Elastos.ELA/core/types"
	"github.com/elastos/Elastos.ELA/core/types/outputpayload"
	"github.com/elastos/Elastos.ELA/core/types/payload"
	"math"
)

// ProducerState represents the state of a producer.
type ProducerState byte

const (
	// Pending indicates the producer is just registered and didn't get 6
	// confirmations yet.
	//刚注册 还没有达到6次确认的producer
	Pending ProducerState = iota

	// Activate indicates the producer is registered and confirmed by more than
	// 6 blocks.
	//已经注册 且确认超过6次
	Activate

	// Inactivate indicates the producer has been inactive for a period which shall
	// be punished and will be activate later
	//非激活状态 晚些时间会被惩罚 会被再次职位激活状态
	Inactivate

	// Canceled indicates the producer was canceled.
	//producer 被取消
	Canceled

	// FoundBad indicates the producer was found doing bad.
	//作恶
	FoundBad

	// ReturnedDeposit indicates the producer has canceled and returned deposit
	//已经取消 且退换了押金
	ReturnedDeposit
)

// producerStateStrings is a array of producer states back to their constant
// names for pretty printing.
var producerStateStrings = []string{"Pending", "Activate", "Inactivate",
	"Canceled", "FoundBad", "ReturnedDeposit"}

func (ps ProducerState) String() string {
	if int(ps) < len(producerStateStrings) {
		return producerStateStrings[ps]
	}
	return fmt.Sprintf("ProducerState-%d", ps)
}

// Producer holds a producer's info.  It provides read only methods to access
// producer's info.
type Producer struct {
	info                  payload.ProducerInfo	//生产者注册的信息
	state                 ProducerState			//生产者的状态Pending Activate Inactivate Canceled FoundBad ReturnedDeposit
	registerHeight        uint32                //注册高度
	cancelHeight          uint32				//取消高度
	inactiveSince         uint32                //??
	activateRequestHeight uint32				//激活高度
	penalty               common.Fixed64		//罚金
	votes                 common.Fixed64		//投票
}

// Info returns a copy of the origin registered producer info.
func (p *Producer) Info() payload.ProducerInfo {
	return p.info
}

// State returns the producer's state, can be pending, active or canceled.
func (p *Producer) State() ProducerState {
	return p.state
}

// RegisterHeight returns the height when the producer was registered.
func (p *Producer) RegisterHeight() uint32 {
	return p.registerHeight
}

// CancelHeight returns the height when the producer was canceled.
func (p *Producer) CancelHeight() uint32 {
	return p.cancelHeight
}

// Votes returns the votes of the producer.
func (p *Producer) Votes() common.Fixed64 {
	return p.votes
}

func (p *Producer) NodePublicKey() []byte {
	return p.info.NodePublicKey
}

func (p *Producer) OwnerPublicKey() []byte {
	return p.info.OwnerPublicKey
}

func (p *Producer) Penalty() common.Fixed64 {
	return p.penalty
}

func (p *Producer) InactiveSince() uint32 {
	return p.inactiveSince
}

const (
	// maxHistoryCapacity indicates the maximum capacity of change history.
	maxHistoryCapacity = 10

	// snapshotInterval is the time interval to take a snapshot of the state.
	snapshotInterval = 12

	// maxSnapshots is the maximum newest snapshots keeps in memory.
	maxSnapshots = 9
)

// State is a memory database storing DPOS producers state, like pending
// producers active producers and their votes.
//dpos 状态
type State struct {
	arbiters    interfaces.Arbitrators     //  Arbitrators的功能管理接口
	chainParams *config.Params			   //  链的配置参数

	mtx                 sync.RWMutex
	nodeOwnerKeys       map[string]string   // NodePublicKey as key（16进制的字符串）, OwnerPublicKey as value
	pendingProducers    map[string]*Producer//阻塞状态的Producer列表
	activityProducers   map[string]*Producer//激活状态的Producer列表
	inactiveProducers   map[string]*Producer//非激活
	canceledProducers   map[string]*Producer//已取消
	illegalProducers    map[string]*Producer//违法
	onDutyMissingCounts map[string]uint32   //当值的时间漏掉的个数 key为owner public key   value:漏掉的个数
	votes               map[string]*types.Output //投票 key为tx.Inputs.ReferKey() value为
	nicknames           map[string]struct{}      //昵称
	specialTxHashes     map[string]struct{}		 //特殊交易hash
	history             *history				 //记录所有的生产者 投票变化的状态

	// snapshots is the data set of DPOS state snapshots, it takes a snapshot of
	// state every 12 blocks, and keeps at most 9 newest snapshots in memory.
	snapshots [maxSnapshots]*State				 //dpos状态的快照， 每12个块快照一次，在内存保存最近的9个块。
	cursor    int  								 //snapshots 当前的索引
}

// getProducerKey returns the producer's owner public key string, whether the
// given public key is the producer's node public key or owner public key.
//参数：
//		publicKey : arbiter的公钥
//功能
// 		获得生产者的 owner public key.
//		1,将publicKey编码成 16进制的字符串key
//		2,如果s.nodeOwnerKeys[key]存在，则它就是owner public key 返回即可。
func (s *State) getProducerKey(publicKey []byte) string {
	key := hex.EncodeToString(publicKey)

	// If the given public key is node public key, get the producer's owner
	// public key.
	if owner, ok := s.nodeOwnerKeys[key]; ok {
		return owner
	}

	return key
}

// getProducer returns a producer with the producer's node public key or it's
// owner public key, if no matches return nil.
func (s *State) getProducer(publicKey []byte) *Producer {
	key := s.getProducerKey(publicKey)
	if producer, ok := s.activityProducers[key]; ok {
		return producer
	}
	if producer, ok := s.canceledProducers[key]; ok {
		return producer
	}
	if producer, ok := s.illegalProducers[key]; ok {
		return producer
	}
	if producer, ok := s.pendingProducers[key]; ok {
		return producer
	}
	if producer, ok := s.inactiveProducers[key]; ok {
		return producer
	}
	return nil
}

// updateProducerInfo updates the producer's info with value compare, any change
// will be updated.
func (s *State) updateProducerInfo(origin *payload.ProducerInfo, update *payload.ProducerInfo) {
	producer := s.getProducer(origin.OwnerPublicKey)

	// compare and update node nickname.
	if origin.NickName != update.NickName {
		delete(s.nicknames, origin.NickName)
		s.nicknames[update.NickName] = struct{}{}
	}

	// compare and update node public key, we only query pending and active node
	// because canceled and illegal node can not be updated.
	if !bytes.Equal(origin.NodePublicKey, update.NodePublicKey) {
		oldKey := hex.EncodeToString(origin.NodePublicKey)
		newKey := hex.EncodeToString(update.NodePublicKey)
		delete(s.nodeOwnerKeys, oldKey)
		s.nodeOwnerKeys[newKey] = hex.EncodeToString(origin.OwnerPublicKey)
	}

	producer.info = *update
}

func (s *State) GetArbiters() interfaces.Arbitrators {
	return s.arbiters
}

// GetProducer returns a producer with the producer's node public key or it's
// owner public key including canceled and illegal producers.  If no matches
// return nil.
func (s *State) GetProducer(publicKey []byte) *Producer {
	s.mtx.RLock()
	producer := s.getProducer(publicKey)
	s.mtx.RUnlock()
	return producer
}

// GetProducers returns all producers including pending and active producers (no
// canceled and illegal producers).
func (s *State) GetProducers() []*Producer {
	s.mtx.RLock()
	producers := make([]*Producer, 0, len(s.pendingProducers)+
		len(s.activityProducers))
	for _, producer := range s.pendingProducers {
		producers = append(producers, producer)
	}
	for _, producer := range s.activityProducers {
		producers = append(producers, producer)
	}
	s.mtx.RUnlock()
	return producers
}

//func (s *State) GetInterfaceProducers() []interfaces.Producer {
//	s.mtx.RLock()
//	producers := s.getProducers()
//	s.mtx.RUnlock()
//	return producers
//}

func (s *State) getProducers() []*Producer {
	producers := make([]*Producer, 0, len(s.activityProducers))
	for _, producer := range s.activityProducers {
		producers = append(producers, producer)
	}
	return producers
}

// GetPendingProducers returns all producers that in pending state.
func (s *State) GetPendingProducers() []*Producer {
	s.mtx.RLock()
	producers := make([]*Producer, 0, len(s.pendingProducers))
	for _, producer := range s.pendingProducers {
		producers = append(producers, producer)
	}
	s.mtx.RUnlock()
	return producers
}

// GetActiveProducers returns all producers that in active state.
func (s *State) GetActiveProducers() []*Producer {
	s.mtx.RLock()
	producers := make([]*Producer, 0, len(s.activityProducers))
	for _, producer := range s.activityProducers {
		producers = append(producers, producer)
	}
	s.mtx.RUnlock()
	return producers
}

// GetCanceledProducers returns all producers that in cancel state.
func (s *State) GetCanceledProducers() []*Producer {
	s.mtx.RLock()
	producers := make([]*Producer, 0, len(s.canceledProducers))
	for _, producer := range s.canceledProducers {
		producers = append(producers, producer)
	}
	s.mtx.RUnlock()
	return producers
}

// GetIllegalProducers returns all illegal producers.
func (s *State) GetIllegalProducers() []*Producer {
	s.mtx.RLock()
	producers := make([]*Producer, 0, len(s.illegalProducers))
	for _, producer := range s.illegalProducers {
		producers = append(producers, producer)
	}
	s.mtx.RUnlock()
	return producers
}

func (s *State) GetInactiveProducers() []*Producer {
	s.mtx.RLock()
	producers := make([]*Producer, 0, len(s.inactiveProducers))
	for _, producer := range s.inactiveProducers {
		producers = append(producers, producer)
	}
	s.mtx.RUnlock()
	return producers
}

// IsPendingProducer returns if a producer is in pending list according to the
// public key.
func (s *State) IsPendingProducer(publicKey []byte) bool {
	s.mtx.RLock()
	_, ok := s.pendingProducers[s.getProducerKey(publicKey)]
	s.mtx.RUnlock()
	return ok
}

// IsActiveProducer returns if a producer is in activate list according to the
// public key.
func (s *State) IsActiveProducer(publicKey []byte) bool {
	s.mtx.RLock()
	_, ok := s.activityProducers[s.getProducerKey(publicKey)]
	s.mtx.RUnlock()
	return ok
}

// IsInactiveProducer returns if a producer is in inactivate list according to
// the public key.
func (s *State) IsInactiveProducer(publicKey []byte) bool {
	s.mtx.RLock()
	ok := s.isInactiveProducer(publicKey)
	s.mtx.RUnlock()
	return ok
}

func (s *State) isInactiveProducer(publicKey []byte) bool {
	_, ok := s.inactiveProducers[s.getProducerKey(publicKey)]
	return ok
}

// IsCanceledProducer returns if a producer is in canceled list according to the
// public key.
func (s *State) IsCanceledProducer(publicKey []byte) bool {
	s.mtx.RLock()
	_, ok := s.canceledProducers[s.getProducerKey(publicKey)]
	s.mtx.RUnlock()
	return ok
}

// IsIllegalProducer returns if a producer is in illegal list according to the
// public key.
func (s *State) IsIllegalProducer(publicKey []byte) bool {
	s.mtx.RLock()
	_, ok := s.illegalProducers[s.getProducerKey(publicKey)]
	s.mtx.RUnlock()
	return ok
}

// NicknameExists returns if a nickname is exists.
func (s *State) NicknameExists(nickname string) bool {
	s.mtx.RLock()
	_, ok := s.nicknames[nickname]
	s.mtx.RUnlock()
	return ok
}

// ProducerExists returns if a producer is exists by it's node public key or
// owner public key.
func (s *State) ProducerExists(publicKey []byte) bool {
	s.mtx.RLock()
	producer := s.getProducer(publicKey)
	s.mtx.RUnlock()
	return producer != nil
}

// SpecialTxExists returns if a special tx (typically means illegal and
// inactive tx) is exists by it's hash
//hash是不是已存在的特殊交易
func (s *State) SpecialTxExists(hash *common.Uint256) bool {
	s.mtx.RLock()
	_, ok := s.specialTxHashes[hash.String()]
	s.mtx.RUnlock()
	return ok
}

// IsDPOSTransaction returns if a transaction will change the producers and
// votes state.
//判断一个交易是不是 dpos交易
func (s *State) IsDPOSTransaction(tx *types.Transaction) bool {
	s.mtx.RLock()
	defer s.mtx.RUnlock()

	switch tx.TxType {
	// Transactions will changes the producers state.
	case types.RegisterProducer, types.UpdateProducer, types.CancelProducer,
		types.ActivateProducer, types.IllegalProposalEvidence,
		types.IllegalVoteEvidence, types.IllegalBlockEvidence,
		types.IllegalSidechainEvidence:
		return true

	// Transactions will change the votes state.
	case types.TransferAsset:
		if tx.Version >= types.TxVersion09 {
			// Votes to producers.
			for _, output := range tx.Outputs {
				if output.Type == types.OTVote {
					return true
				}
			}
		}

		// Cancel votes.
		for _, input := range tx.Inputs {
			_, ok := s.votes[input.ReferKey()]
			if ok {
				return true
			}
		}
	}

	return false
}

// ProcessBlock takes a block and it's confirm to update producers state and
// votes accordingly.
/*
	参数：
		block *types.Block       ： 块
		confirm *payload.Confirm ： 确认情况
	功能：
		1，处理块中的所有的交易
		2，高于投票开始高度，每12个块快照一下
*/
func (s *State) ProcessBlock(block *types.Block, confirm *payload.Confirm) {
	// get on duty arbiter before lock in case of recurrent lock
	onDutyArbitrator := s.arbiters.GetOnDutyArbitratorByHeight(block.Height)
	arbitersCount := s.arbiters.GetArbitersCount()

	s.mtx.Lock()
	defer s.mtx.Unlock()

	s.processTransactions(block.Transactions, block.Height)

	if confirm != nil {
		s.countArbitratorsInactivity(block.Height, arbitersCount,
			onDutyArbitrator, confirm)
	}
	//arbiters增加或者减少一个高度
	s.processArbitrators(block, block.Height)

	// Take snapshot when snapshot point arrives.
	if (block.Height-s.chainParams.VoteStartHeight)%snapshotInterval == 0 {
		s.cursor = s.cursor % maxSnapshots
		s.snapshots[s.cursor] = s.snapshot()
		s.cursor++
	}

	// Commit changes here if no errors found.
	s.history.commit(block.Height)
}
//arbiters增加或者减少一个高度，
func (s *State) processArbitrators(block *types.Block, height uint32) {
	s.history.append(height, func() {
		s.arbiters.IncreaseChainHeight(block.Height)
	}, func() {
		s.arbiters.DecreaseChainHeight(block.Height)
	})
}

// processTransactions takes the transactions and the height when they have been
// packed into a block.  Then loop through the transactions to update producers
// state and votes according to transactions content.
/*
参数：
	txs []*types.Transaction	交易slice
	height uint32				块高度
功能：
	遍历每个交易，并处理交易
	如果有pendingProducers 遍历每一个producer如果当前高度大于请求高度5个块则将这个producer激活

*/
func (s *State) processTransactions(txs []*types.Transaction, height uint32) {
	for _, tx := range txs {
		s.processTransaction(tx, height)
	}

	// Check if any pending producers has got 6 confirms, set them to activate.
	//从阻塞态 转换为激活太
	activateProducerFromPending := func(key string, producer *Producer) {
		s.history.append(height, func() {
			producer.state = Activate
			s.activityProducers[key] = producer
			delete(s.pendingProducers, key)
		}, func() {
			producer.state = Pending
			s.pendingProducers[key] = producer
			delete(s.activityProducers, key)
		})
	}

	// Check if any pending producers has got 6 confirms, set them to activate.
	//从非激活态 转换为激活态
	activateProducerFromInactive := func(key string, producer *Producer) {
		s.history.append(height, func() {
			producer.state = Activate
			s.activityProducers[key] = producer
			delete(s.inactiveProducers, key)
		}, func() {
			producer.state = Inactivate
			s.inactiveProducers[key] = producer
			delete(s.activityProducers, key)
		})
	}

	if len(s.pendingProducers) > 0 {
		for key, producer := range s.pendingProducers {
			// ？？？？？ 似乎早了一个块
			if height-producer.registerHeight+1 >= 6 {
				activateProducerFromPending(key, producer)
			}
		}

	}
	//对于非激活状态的Producer 当前高度大于激活需要的高度，且当前高度大于请求高度6个块则激活这个producer
	if len(s.inactiveProducers) > 0 {
		for key, producer := range s.inactiveProducers {
			//????????? 这里条件的前半截不需要吧   从非激活态转过来需要6个块高
			if height > producer.activateRequestHeight &&
				height-producer.activateRequestHeight+1 >= 6 {
				activateProducerFromInactive(key, producer)
			}
		}
	}
}

// processTransaction take a transaction and the height it has been packed into
// a block, then update producers state and votes according to the transaction
// content.
//根据交易的类型 来处理对应的函数。分别有
// 1,注册producer
// 2,更新producer
// 3,取消producer
// 4,激活producer
// 5, TransferAsset
// 6, 非法证据
// 7, InactiveArbitrators
// 8, ReturnDepositCoin
func (s *State) processTransaction(tx *types.Transaction, height uint32) {
	switch tx.TxType {
	case types.RegisterProducer:
		s.registerProducer(tx.Payload.(*payload.ProducerInfo),
			height)

	case types.UpdateProducer:
		s.updateProducer(tx.Payload.(*payload.ProducerInfo),
			height)

	case types.CancelProducer:
		s.cancelProducer(tx.Payload.(*payload.ProcessProducer),
			height)

	case types.ActivateProducer:
		s.activateProducer(tx.Payload.(*payload.ProcessProducer), height)

	case types.TransferAsset:
		s.processVotes(tx, height)

	case types.IllegalProposalEvidence, types.IllegalVoteEvidence,
		types.IllegalBlockEvidence, types.IllegalSidechainEvidence:
		s.processIllegalEvidence(tx.Payload, height)
		s.recordSpecialTx(tx, height)

	case types.InactiveArbitrators:
		s.processEmergencyInactiveArbitrators(
			tx.Payload.(*payload.InactiveArbitrators), height)
		s.recordSpecialTx(tx, height)

	case types.ReturnDepositCoin:
		s.returnDeposit(tx, height)
	}
}

// registerProducer handles the register producer transaction.
//注册Producer
func (s *State) registerProducer(payload *payload.ProducerInfo, height uint32) {
	nickname := payload.NickName
	nodeKey := hex.EncodeToString(payload.NodePublicKey)
	ownerKey := hex.EncodeToString(payload.OwnerPublicKey)
	producer := Producer{
		info:                  *payload,
		registerHeight:        height,
		votes:                 0,
		inactiveSince:         0,
		penalty:               common.Fixed64(0),
		activateRequestHeight: math.MaxUint32,
	}

	s.history.append(height, func() {
		s.nicknames[nickname] = struct{}{}
		s.nodeOwnerKeys[nodeKey] = ownerKey
		s.pendingProducers[ownerKey] = &producer
	}, func() {
		delete(s.nicknames, nickname)
		delete(s.nodeOwnerKeys, nodeKey)
		delete(s.pendingProducers, ownerKey)
	})
}

// updateProducer handles the update producer transaction.
//更新ProducerInfo
func (s *State) updateProducer(info *payload.ProducerInfo, height uint32) {
	producer := s.getProducer(info.OwnerPublicKey)
	producerInfo := producer.info
	s.history.append(height, func() {
		s.updateProducerInfo(&producerInfo, info)
	}, func() {
		s.updateProducerInfo(info, &producerInfo)
	})
}

// cancelProducer handles the cancel producer transaction.
//取消一个Producer
func (s *State) cancelProducer(payload *payload.ProcessProducer, height uint32) {
	key := hex.EncodeToString(payload.OwnerPublicKey)
	producer := s.getProducer(payload.OwnerPublicKey)
	s.history.append(height, func() {
		producer.state = Canceled
		producer.cancelHeight = height
		s.canceledProducers[key] = producer
		delete(s.activityProducers, key)
		delete(s.nicknames, producer.info.NickName)
	}, func() {
		producer.state = Activate
		producer.cancelHeight = 0
		delete(s.canceledProducers, key)
		s.activityProducers[key] = producer
		s.nicknames[producer.info.NickName] = struct{}{}
	})
}

// activateProducer handles the activate producer transaction.
//激活Producer
func (s *State) activateProducer(p *payload.ProcessProducer, height uint32) {
	producer := s.getProducer(p.OwnerPublicKey)
	s.history.append(height, func() {
		producer.activateRequestHeight = height
	}, func() {
		producer.activateRequestHeight = math.MaxUint32
	})
}

// processVotes takes a transaction, if the transaction including any vote
// inputs or outputs, validate and update producers votes.

/*
参数：
	tx *types.Transaction  交易
	height uint32          块高度
功能：
	1，如果交易的版本号 >= TxVersion09 遍历每一个输出如果输出的类型为OTVote 则s.votes增加记录 key为 output的ReferKey value为这个output
	2, 遍历这个交易的input， 如果这个input在s.votes中存在，则对这个output处理取消投票
*/
func (s *State) processVotes(tx *types.Transaction, height uint32) {

	if tx.Version >= types.TxVersion09 {
		// Votes to producers.
		for i, output := range tx.Outputs {
			if output.Type == types.OTVote {
				op := types.NewOutPoint(tx.Hash(), uint16(i))
				s.votes[op.ReferKey()] = output
				s.processVoteOutput(output, height)
			}
		}
	}

	// Cancel votes.
	for _, input := range tx.Inputs {
		output, ok := s.votes[input.ReferKey()]
		if ok {
			s.processVoteCancel(output, height)
		}
	}
}
/*
参数：
	output *types.Output : 输出
	height uint32		 : 区块高度

功能：
	遍历output的 vote.Candidates ，增加每个生产者的票数
*/
// processVoteOutput takes a transaction output with vote payload.
func (s *State) processVoteOutput(output *types.Output, height uint32) {
	payload := output.Payload.(*outputpayload.VoteOutput)
	for _, vote := range payload.Contents {
		for _, candidate := range vote.Candidates {
			key := hex.EncodeToString(candidate)
			producer := s.activityProducers[key]
			switch vote.VoteType {
			case outputpayload.CRC:
				// TODO separate CRC and Delegate votes.
				fallthrough
			case outputpayload.Delegate:
				s.history.append(height, func() {
					producer.votes += output.Value
				}, func() {
					producer.votes -= output.Value
				})
			}
		}
	}
}

// processVoteCancel takes a previous vote output and decrease producers votes.

/*
参数：
	output *types.Output : 输出
	height uint32		 : 区块高度

功能：
	遍历output的 vote.Candidates ，减去每个生产者的票数
*/
func (s *State) processVoteCancel(output *types.Output, height uint32) {
	payload := output.Payload.(*outputpayload.VoteOutput)
	for _, vote := range payload.Contents {
		for _, candidate := range vote.Candidates {
			producer := s.getProducer(candidate)
			if producer == nil {
				// This should not happen, just in case.
				continue
			}
			switch vote.VoteType {
			case outputpayload.CRC:
				// TODO separate CRC and Delegate votes.
				fallthrough
			case outputpayload.Delegate:
				s.history.append(height, func() {
					producer.votes -= output.Value
				}, func() {
					producer.votes += output.Value
				})
			}
		}
	}
}

/*
参数：
	tx *types.Transaction ： 交易
	height uint32         ： 高度
功能:
	遍历tx.Programs，根据得到的publickey获得producer, 如果producer的状态为Canceled
	则将producer的状态设置为ReturnedDeposit
*/
func (s *State) returnDeposit(tx *types.Transaction, height uint32) {

	returnAction := func(producer *Producer) {
		s.history.append(height, func() {
			producer.state = ReturnedDeposit
		}, func() {
			producer.state = Canceled
		})
	}

	for _, program := range tx.Programs {
		pk := program.Code[1 : len(program.Code)-1]
		if producer := s.GetProducer(pk); producer != nil && producer.state == Canceled {
			returnAction(producer)
		}
	}
}

// processEmergencyInactiveArbitrators change producer state according to
// emergency inactive arbitrators
/*
inactivePayload	 : *payload.InactiveArbitrators  非激活的Arbitrators
height			 : uint32                        块高

功能：
	遍历传入的InactiveArbitrators，设置每一个为inactive
*/
func (s *State) processEmergencyInactiveArbitrators(
	inactivePayload *payload.InactiveArbitrators, height uint32) {

	addEmergencyInactiveArbitrator := func(key string, producer *Producer) {
		s.history.append(height, func() {
			s.setInactiveProducer(producer, key, height)
		}, func() {
			s.revertSettingInactiveProducer(producer, key, height)
		})
	}

	for _, v := range inactivePayload.Arbitrators {
		key := hex.EncodeToString(v)
		if p, ok := s.activityProducers[key]; ok {
			addEmergencyInactiveArbitrator(key, p)
		}
	}

}

// recordSpecialTx record hash of a special tx
func (s *State) recordSpecialTx(tx *types.Transaction, height uint32) {
	s.history.append(height, func() {
		s.specialTxHashes[tx.Hash().String()] = struct{}{}
	}, func() {
		delete(s.specialTxHashes, tx.Hash().String())
	})
}

// processIllegalEvidence takes the illegal evidence payload and change producer
// state according to the evidence.
/*
参数：
	payloadData	:	types.Payload
	height      :   uint32			块高

功能：
	根据payloadData的类型
		如果是提案的话记录提案的发起者
		如果是非法的投票的话，		记录投票的签名者
		如果是非法的块的话，   	记录每一个签名者
		如果是非法的侧链的块的话	记录非法的签名者
		非法的producer 的状态设置为FoundBad。并将其设置到illegalProducers 并从activityProducers删除
*/
func (s *State) processIllegalEvidence(payloadData types.Payload,
	height uint32) {
	// Get illegal producers from evidence.
	var illegalProducers [][]byte
	switch p := payloadData.(type) {
	case *payload.DPOSIllegalProposals:
		illegalProducers = [][]byte{p.Evidence.Proposal.Sponsor}

	case *payload.DPOSIllegalVotes:
		illegalProducers = [][]byte{p.Evidence.Vote.Signer}

	case *payload.DPOSIllegalBlocks:
		signers := make(map[string]interface{})
		for _, pk := range p.Evidence.Signers {
			signers[hex.EncodeToString(pk)] = nil
		}

		for _, pk := range p.CompareEvidence.Signers {
			key := hex.EncodeToString(pk)
			if _, ok := signers[key]; ok {
				illegalProducers = append(illegalProducers, pk)
			}
		}

	case *payload.SidechainIllegalData:
		illegalProducers = [][]byte{p.IllegalSigner}

	default:
		return
	}

	// Set illegal producers to FoundBad state
	for _, pk := range illegalProducers {
		key := hex.EncodeToString(pk)
		if producer, ok := s.activityProducers[key]; ok {
			s.history.append(height, func() {
				producer.state = FoundBad
				s.illegalProducers[key] = producer
				delete(s.activityProducers, key)
				delete(s.nicknames, producer.info.NickName)
			}, func() {
				producer.state = Activate
				s.activityProducers[key] = producer
				delete(s.illegalProducers, key)
				s.nicknames[producer.info.NickName] = struct{}{}
			})
			continue
		}
		//canceledProducers 还能作恶吗？？？？？ 怎么作恶。各个状态的切换，以及其职能。
		if producer, ok := s.canceledProducers[key]; ok {
			s.history.append(height, func() {
				producer.state = FoundBad
				s.illegalProducers[key] = producer
				delete(s.canceledProducers, key)
				delete(s.nicknames, producer.info.NickName)
			}, func() {
				producer.state = Canceled
				s.canceledProducers[key] = producer
				delete(s.illegalProducers, key)
				s.nicknames[producer.info.NickName] = struct{}{}
			})
			continue
		}
	}
}

// ProcessIllegalBlockEvidence takes a illegal block payload and change the
// producers state immediately.  This is a spacial case that can be handled
// before it packed into a block.
func (s *State) ProcessSpecialTxPayload(p types.Payload) {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	if inactivePayload, ok := p.(*payload.InactiveArbitrators); ok {
		s.processEmergencyInactiveArbitrators(inactivePayload, 0)
	} else {
		s.processIllegalEvidence(p, 0)
	}

	// Commit changes here if no errors found.
	s.history.commit(0)
}

// setInactiveProducer set active producer to inactive state
//设置Producer为inactive 并且罚金增加InactivePenalty
func (s *State) setInactiveProducer(producer *Producer, key string,
	height uint32) {
	//同样这里没有检查原来是激活状态，？？？？？？
	producer.inactiveSince = height
	producer.state = Inactivate
	s.inactiveProducers[key] = producer
	delete(s.activityProducers, key)
	//?????????????????????????? 这里感觉是不是不太对 对照revertSettingInactiveProducer的penalty
	producer.penalty += s.chainParams.InactivePenalty
}

// revertSettingInactiveProducer revert operation about setInactiveProducer
/*
	撤销Producer的inactive状态。这里参数height 没用。
*/
func (s *State) revertSettingInactiveProducer(producer *Producer, key string,
	height uint32) {
	//producer状态设置为激活,
	//罚金小于非激活罚金的话  罚金为0
	//罚金如果大于Inactive Penalty  减去InactivePenalty
	//???????????????? 这里并没有检查说当前的状态是Inactive
	producer.inactiveSince = 0
	producer.state = Activate
	s.activityProducers[key] = producer
	delete(s.inactiveProducers, key)

	if producer.penalty < s.chainParams.InactivePenalty {
		producer.penalty = common.Fixed64(0)
	} else {
		producer.penalty -= s.chainParams.InactivePenalty
	}
}

// countArbitratorsInactivity count arbitrators inactive rounds, and change to
// inactive if more than "MaxInactiveRounds"

/*
参数
	height			   : uint32    			块高
	totalArbitersCount : uint32    			arbiter个数
	onDutyArbiter      : []byte    			当值的Arbiter publicKey
	confirm            : *payload.Confirm	某个提案的确认（大家的投票情况）

功能：
	1，如果总的arbiters个数 跟CRCArbiters个数相等返回
	2，处理当值的Producer 是否应该设置为非激活状态
*/
func (s *State) countArbitratorsInactivity(height, totalArbitersCount uint32,
	onDutyArbiter []byte, confirm *payload.Confirm) {
	// check inactive arbitrators after producers has participated in
	//????????????这里是否会出现，CRCArbiters掉线了的情况
	if int(totalArbitersCount) == len(s.chainParams.CRCArbiters) {
		return
	}

	key := s.getProducerKey(onDutyArbiter)

	//当值的arbiter  不是这个提案的发起人????????
	isMissOnDuty := !bytes.Equal(onDutyArbiter, confirm.Proposal.Sponsor)

	if producer, ok := s.activityProducers[key]; ok {
		//count漏掉的个数
		//existMissingRecord bool 是否有漏掉个数的记录
		count, existMissingRecord := s.onDutyMissingCounts[key]

		s.history.append(height, func() {
			s.tryUpdateOnDutyInactivity(isMissOnDuty, existMissingRecord,
				key, producer, height)
		}, func() {
			s.tryRevertOnDutyInactivity(isMissOnDuty, existMissingRecord, key,
				producer, height, count)
		})
	}
}
/*
参数：
	isMissOnDuty:           当值的arbitor 是否是这个提案的发起者
	existMissingRecord：    是否有漏签记录
	key:                    当前arbitor的 owner public key.
	producer:			    当值的arbitor的producer结构
	height:					块高
	count :					漏掉的次数
功能：
	1，如果当前漏处理区块，   且原来存在漏处理记录，则增加次数。如果原来不存在漏处理区块则次数设为1 如果次数超过配置的次数则设置这个producer为非激活状态
	3，如果当前不是漏处理区块， 则删除这个producer的漏处理记录
*/
func (s *State) tryRevertOnDutyInactivity(isMissOnDuty bool,
	existMissingRecord bool, key string, producer *Producer,
	height uint32, count uint32) {

	if isMissOnDuty {
		if existMissingRecord {
			if producer.state == Inactivate {
				//撤销设置非激活的Producer
				s.revertSettingInactiveProducer(producer, key, height)
				//这里是不是应该设置为s.chainParams.MaxInactiveRounds -1  ？？？？？？？、
				s.onDutyMissingCounts[key] = s.chainParams.MaxInactiveRounds
			} else {
				if count > 1 {
					s.onDutyMissingCounts[key]--
				} else {
					delete(s.onDutyMissingCounts, key)
				}
			}
		}
	} else {
		if existMissingRecord {
			s.onDutyMissingCounts[key] = count
		}
	}
}
/*
参数：
	isMissOnDuty:           当值的arbitor 是否是这个提案的发起者
	existMissingRecord：    是否有漏签记录
	key:                    当前arbitor的 owner public key.
	producer:			    当值的arbitor的producer结构
	height:					块高
功能：
	1，如果当前漏处理区块，   且原来存在漏处理记录，则增加次数。如果原来不存在漏处理区块则次数设为1 如果次数超过配置的次数则设置这个producer为非激活状态
	3，如果当前不是漏处理区块， 则删除这个producer的漏处理记录
*/
func (s *State) tryUpdateOnDutyInactivity(isMissOnDuty bool,
	existMissingRecord bool, key string, producer *Producer, height uint32) {
	//？？？？？？
	if isMissOnDuty {
		if existMissingRecord {
			s.onDutyMissingCounts[key]++

			if s.onDutyMissingCounts[key] >=
				s.chainParams.MaxInactiveRounds {
				s.setInactiveProducer(producer, key, height)
				delete(s.onDutyMissingCounts, key)
			}
		} else {
			s.onDutyMissingCounts[key] = 1
		}
	} else {
		if existMissingRecord {
			delete(s.onDutyMissingCounts, key)
		}
	}
}

// RollbackTo restores the database state to the given height, if no enough
// history to rollback to return error.
//回退到高度height
func (s *State) RollbackTo(height uint32) error {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	return s.history.rollbackTo(height)
}

// GetHistory returns a history state instance storing the producers and votes
// on the historical height.
func (s *State) GetHistory(height uint32) (*State, error) {
	s.mtx.RLock()
	defer s.mtx.RUnlock()

	// Seek to state to target height.
	if err := s.history.seekTo(height); err != nil {
		return nil, err
	}

	// Take a snapshot of the history.
	return s.snapshot(), nil
}

// snapshot takes a snapshot of current state and returns the copy.
//获取state s的阻塞，激活，非激活，已取消的 非法行为的Producer 以及onDutyMissingCounts
func (s *State) snapshot() *State {
	state := State{
		pendingProducers:    make(map[string]*Producer),
		activityProducers:   make(map[string]*Producer),
		inactiveProducers:   make(map[string]*Producer),
		canceledProducers:   make(map[string]*Producer),
		illegalProducers:    make(map[string]*Producer),
		onDutyMissingCounts: make(map[string]uint32),
	}
	copyMap(state.pendingProducers, s.pendingProducers)
	copyMap(state.activityProducers, s.activityProducers)
	copyMap(state.inactiveProducers, s.inactiveProducers)
	copyMap(state.canceledProducers, s.canceledProducers)
	copyMap(state.illegalProducers, s.illegalProducers)
	copyCountMap(state.onDutyMissingCounts, s.onDutyMissingCounts)
	return &state
}

// GetSnapshot returns a snapshot of the state according to the given height.
//根据给定的高度 获取快照
func (s *State) GetSnapshot(height uint32) *State {
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	offset := (s.history.height - height) / snapshotInterval
	index := (s.cursor - 1 - int(offset) + maxSnapshots) % maxSnapshots
	return s.snapshots[index]
}

// copyMap copy the src map's key, value pairs into dst map.
// value 是指针的用这个
func copyMap(dst map[string]*Producer, src map[string]*Producer) {
	for k, v := range src {
		p := *v
		dst[k] = &p
	}
}
//value 是值的用这个
// copyMap copy the src map's key, value pairs into dst map.
func copyCountMap(dst map[string]uint32, src map[string]uint32) {
	for k, v := range src {
		dst[k] = v
	}
}

// NewState returns a new State instance.
func NewState(arbiters interfaces.Arbitrators, chainParams *config.Params) *State {
	return &State{
		arbiters:            arbiters,
		chainParams:         chainParams,
		nodeOwnerKeys:       make(map[string]string),
		pendingProducers:    make(map[string]*Producer),
		activityProducers:   make(map[string]*Producer),
		inactiveProducers:   make(map[string]*Producer),
		canceledProducers:   make(map[string]*Producer),
		illegalProducers:    make(map[string]*Producer),
		onDutyMissingCounts: make(map[string]uint32),
		votes:               make(map[string]*types.Output),
		nicknames:           make(map[string]struct{}),
		specialTxHashes:     make(map[string]struct{}),
		history:             newHistory(maxHistoryCapacity),
	}
}
