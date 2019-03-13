package dpos

import (
	"encoding/hex"
	"errors"
	"math/rand"
	"sync"

	"github.com/elastos/Elastos.ELA/blockchain"
	"github.com/elastos/Elastos.ELA/blockchain/interfaces"
	"github.com/elastos/Elastos.ELA/common"
	"github.com/elastos/Elastos.ELA/common/config"
	"github.com/elastos/Elastos.ELA/core/types"
	"github.com/elastos/Elastos.ELA/core/types/payload"
	"github.com/elastos/Elastos.ELA/dpos/account"
	"github.com/elastos/Elastos.ELA/dpos/log"
	"github.com/elastos/Elastos.ELA/dpos/manager"
	"github.com/elastos/Elastos.ELA/dpos/p2p"
	"github.com/elastos/Elastos.ELA/dpos/p2p/msg"
	"github.com/elastos/Elastos.ELA/dpos/p2p/peer"
	elap2p "github.com/elastos/Elastos.ELA/p2p"
	elamsg "github.com/elastos/Elastos.ELA/p2p/msg"
)

type PeerItem struct {
	Address     p2p.PeerAddr//IP地址 以及公钥ID
	NeedConnect bool        //????
	Peer        *peer.Peer //网络连接相关的数据
	Sequence    uint32     //arbitor的序号 原来的序号+len(arbitrators)
}

type blockItem struct {
	Block     *types.Block  //区块
	Confirmed bool          //是否确认
}

type messageItem struct {
	ID      peer.PID       //公钥的数据 public key data
	Message elap2p.Message //消息interface 这种作用是什么？？？？
}

type network struct {
	listener           manager.NetworkEventListener   //网络监听接口， 网络消息相关的东东
	currentHeight      uint32                         //当前高度
	account            account.DposAccount            //DposAccount接口 提案签名， 投票签名等。
	proposalDispatcher *manager.ProposalDispatcher    ////一次提案 相关的数据。
	directPeers        map[string]*PeerItem           //直连接的对端（超级节点）。保存的有地址以及public key data
	peersLock          sync.Mutex
	store              interfaces.IDposStore          //存储相关的接口
	publicKey          []byte

	p2pServer    p2p.Server							  //p2p.Server 	接口
	messageQueue chan *messageItem                    //消息队列管道
	quit         chan bool                            //退出管道

	changeViewChan           chan bool                //变更视图管道
	blockReceivedChan        chan blockItem           //块接收管道
	confirmReceivedChan      chan *payload.Confirm    // 确认收到管道
	illegalBlocksEvidence    chan *payload.DPOSIllegalBlocks    //非法块证据管道
	sidechainIllegalEvidence chan *payload.SidechainIllegalData //侧链非法证据
}

//输入参数： dnConfig
//输出参数： 无
//功能： 根据输入参数 初始化network的proposalDispatcher store publicKey。并根据dnConfig.Store获得其他超级节点的信息。便利这超级节点信息
//填充directPeers
func (n *network) Initialize(dnConfig manager.DPOSNetworkConfig) {
	n.proposalDispatcher = dnConfig.ProposalDispatcher
	n.store = dnConfig.Store
	n.publicKey = dnConfig.PublicKey
	if peers, err := dnConfig.Store.GetDirectPeers(); err == nil {
		for _, p := range peers {
			pid := peer.PID{}
			copy(pid[:], p.PublicKey)
			n.directPeers[common.BytesToHexString(p.PublicKey)] = &PeerItem{
				Address: p2p.PeerAddr{
					PID:  pid,
					Addr: p.Address,
				},
				NeedConnect: true,
				Peer:        nil,
				Sequence:    p.Sequence,
			}
		}
	}
}
//1，无限循环直到quit管道来消息 2，select接收到任何管道消息到对应的消息处理
func (n *network) Start() {
	n.p2pServer.Start()

	n.UpdateProducersInfo()
	arbiters := blockchain.DefaultLedger.Arbitrators.GetArbitrators()
	if err := n.UpdatePeers(arbiters); err != nil {
		log.Error(err)
	}

	go func() {
	out:
		for {
			select {
			case msgItem := <-n.messageQueue:
				n.processMessage(msgItem)
			case <-n.changeViewChan:
				n.changeView()
			case blockItem := <-n.blockReceivedChan:
				n.blockReceived(blockItem.Block, blockItem.Confirmed)
			case confirm := <-n.confirmReceivedChan:
				n.confirmReceived(confirm)
			case evidence := <-n.illegalBlocksEvidence:
				n.illegalBlocksReceived(evidence)
			case sidechainEvidence := <-n.sidechainIllegalEvidence:
				n.sidechainIllegalEvidenceReceived(sidechainEvidence)
			case <-n.quit:
				break out
			}
		}
	}()
}
//1, 遍历每一个直接连接的Peers 如果他已经不再是激活的producer则把他放入needDeletedPeers
//2, 对于已经直连的超级节点，更新他的连接信息注意NeedConnect为false Sequence为0  Peer为nil
//3, 保存直连的超级节点信息
func (n *network) UpdateProducersInfo() {
	log.Info("[UpdateProducersInfo] start")
	defer log.Info("[UpdateProducersInfo] end")
	////获得生产者的连接信息
	connectionInfoMap := n.getProducersConnectionInfo()

	n.peersLock.Lock()
	defer n.peersLock.Unlock()

	needDeletedPeers := make([]string, 0)
	//遍历每一个直接连接的Peers 如果他已经不再是激活的producer则把他放入needDeletedPeers
	for k := range n.directPeers {
		if _, ok := connectionInfoMap[k]; !ok {
			needDeletedPeers = append(needDeletedPeers, k)
		}
	}
	//删除不再激活的直连的连接
	for _, v := range needDeletedPeers {
		delete(n.directPeers, v)
	}
	//对于已经直连的超级节点，更新他的连接信息注意NeedConnect为false Sequence为0  Peer为nil
	for k, v := range connectionInfoMap {
		if _, ok := n.directPeers[k]; !ok {
			n.directPeers[k] = &PeerItem{
				Address:     v,
				NeedConnect: false,
				Peer:        nil,
				Sequence:    0,
			}
		}
	}
	//存储直连的节点的信息
	//???????????????? 这里如果有个节点被删除了 没有删除存储，下次重连还是有可能连接的吧。
	n.saveDirectPeers()
}
//获得生产者的连接信息
func (n *network) getProducersConnectionInfo() (result map[string]p2p.PeerAddr) {
	result = make(map[string]p2p.PeerAddr)
	//获得共制委员会的arbitrator
	crcs := blockchain.DefaultLedger.Arbitrators.GetCRCArbitrators()
	for _, c := range crcs {
		if len(c.PublicKey) != 33 {
			log.Warn("[getProducersConnectionInfo] invalid public key")
			continue
		}
		pid := peer.PID{}
		copy(pid[:], c.PublicKey)
		result[hex.EncodeToString(c.PublicKey)] =
			p2p.PeerAddr{PID: pid, Addr: c.NetAddress}
	}
	//获得激活的生产者
	producers := blockchain.DefaultLedger.Blockchain.GetState().GetActiveProducers()
	for _, p := range producers {
		if len(p.Info().NodePublicKey) != 33 {
			log.Warn("[getProducersConnectionInfo] invalid public key")
			continue
		}
		pid := peer.PID{}
		copy(pid[:], p.Info().NodePublicKey)
		result[hex.EncodeToString(p.Info().NodePublicKey)] =
			p2p.PeerAddr{PID: pid, Addr: p.Info().NetAddress}
	}

	return result
}
//停止 p2pServer.Stop
func (n *network) Stop() error {
	n.quit <- true
	return n.p2pServer.Stop()
}
//根据传入的arbitrators ，如果directPeers存在则 NeedConnect为true 和 更新Sequence
//注意crc的Arbitrators 也一起更新
func (n *network) UpdatePeers(arbitrators [][]byte) error {
	log.Info("[UpdatePeers] arbitrators:", arbitrators)
	for _, v := range arbitrators {
		pubKey := common.BytesToHexString(v)

		n.peersLock.Lock()
		//这里注意 ad为指针
		ad, ok := n.directPeers[pubKey]
		if !ok {
			log.Error("can not find arbitrator related connection information, arbitrator public key is: ", pubKey)
			n.peersLock.Unlock()
			continue
		}
		ad.NeedConnect = true// 这次为啥要设置为true ???
		ad.Sequence += uint32(len(arbitrators))
		n.peersLock.Unlock()
	}
	for _, c := range blockchain.DefaultLedger.Arbitrators.GetCRCArbitrators() {
		pubKey := common.BytesToHexString(c.PublicKey)

		n.peersLock.Lock()
		ad, ok := n.directPeers[pubKey]
		if !ok {
			log.Error("can not find crc arbitrator related connection information, arbitrator public key is: ", pubKey)
			n.peersLock.Unlock()
			continue
		}
		ad.NeedConnect = true
		ad.Sequence += uint32(len(arbitrators))
		n.peersLock.Unlock()
	}
	n.saveDirectPeers()

	return nil
}
//发送消息给peer id
func (n *network) SendMessageToPeer(id peer.PID, msg elap2p.Message) error {
	return n.p2pServer.SendMessageToPeer(id, msg)
}
//广播消息给已连接上的节点
func (n *network) BroadcastMessage(msg elap2p.Message) {
	log.Info("[BroadcastMessage] current connected peers:", len(n.getValidPeers()))
	n.p2pServer.BroadcastMessage(msg)
}
//
func (n *network) ChangeHeight(height uint32) error {
	if height != 0 && height < n.currentHeight {
		return errors.New("changing height lower than current height")
	}

	offset := height - n.currentHeight
	if offset == 0 {
		return nil
	}

	n.peersLock.Lock()
	crcArbiters := blockchain.DefaultLedger.Arbitrators.GetCRCArbitrators()
	crcArbitersMap := make(map[string]struct{}, 0)
	for _, a := range crcArbiters {
		crcArbitersMap[common.BytesToHexString(a.PublicKey)] = struct{}{}
	}
	//这里注意v  也是一个指针
	for _, v := range n.directPeers {
		if _, ok := crcArbitersMap[common.BytesToHexString(v.Address.PID[:])]; ok {
			continue
		}
		if v.Sequence < offset {
			v.NeedConnect = false
			v.Sequence = 0
			continue
		}

		v.Sequence -= offset//??????????? 这个算法是啥意义呢
	}

	peers := n.getValidPeers()
	for i, peer := range peers {
		log.Info(" peer[", i, "] addr:", peer.Addr, " pid:", common.BytesToHexString(peer.PID[:]))
	}

	n.p2pServer.ConnectPeers(peers)
	n.peersLock.Unlock()

	go n.UpdateProducersInfo()

	n.currentHeight = height //?????????????????这个不用加锁吗
	return nil
}
//获得激活的producer，如果直接连接的peers为0则返回nil,为1则返回peers[0]的。否则随机选择一个返回
func (n *network) GetActivePeer() *peer.PID {
	peers := n.p2pServer.ConnectedPeers()
	log.Debug("[GetActivePeer] current connected peers:", len(peers))
	if len(peers) == 0 {
		return nil
	}
	if len(peers) == 1 {
		id := peers[0].PID()
		return &id
	}
	i := rand.Int31n(int32(len(peers)) - 1)
	id := peers[i].PID()
	return &id
}
//发送一个切换视图的消息
func (n *network) PostChangeViewTask() {
	n.changeViewChan <- true
}
//发送收到块的消息
func (n *network) PostBlockReceivedTask(b *types.Block, confirmed bool) {
	n.blockReceivedChan <- blockItem{b, confirmed}
}
//发送非法块的证据
func (n *network) PostIllegalBlocksTask(i *payload.DPOSIllegalBlocks) {
	n.illegalBlocksEvidence <- i
}
//发送侧链非法证据 可能还没实现
func (n *network) PostSidechainIllegalDataTask(s *payload.SidechainIllegalData) {
	n.sidechainIllegalEvidence <- s
}
//发送某个块确认的 消息
func (n *network) PostConfirmReceivedTask(p *payload.Confirm) {
	n.confirmReceivedChan <- p
}
//判断n是不是超级节点
func (n *network) InProducerList() bool {
	return len(n.getValidPeers()) != 0
}
//查看directPeers公钥为n的key的peeritem是否存在， 如果不存在返回空，
// 如果存在且NeedConnect为true 则返回它的PeerAddr信息
func (n *network) getValidPeers() (result []p2p.PeerAddr) {
	result = make([]p2p.PeerAddr, 0)
	if _, ok := n.directPeers[common.BytesToHexString(n.publicKey)]; !ok {
		log.Info("self not in direct peers list")
		return result
	}
	for _, v := range n.directPeers {
		if v.NeedConnect {
			result = append(result, v.Address)
		}
	}
	return result
}
//处理NFBadNetwork请求。随机找个超级节点发送请求共识的消息
func (n *network) notifyFlag(flag p2p.NotifyFlag) {
	if flag == p2p.NFBadNetwork {
		n.listener.OnBadNetwork()
	}
}
//向messageQueue发送messageItem{pid, msg}消息
func (n *network) handleMessage(pid peer.PID, msg elap2p.Message) {
	n.messageQueue <- &messageItem{pid, msg}
}
//处理dpos相关的网络消息
func (n *network) processMessage(msgItem *messageItem) {
	m := msgItem.Message
	switch m.CMD() {
	case msg.CmdReceivedProposal:
		msgProposal, processed := m.(*msg.Proposal)
		if processed {
			n.listener.OnProposalReceived(msgItem.ID, msgProposal.Proposal)
		}
	case msg.CmdAcceptVote:
		msgVote, processed := m.(*msg.Vote)
		if processed {
			n.listener.OnVoteReceived(msgItem.ID, msgVote.Vote)
		}
	case msg.CmdRejectVote:
		msgVote, processed := m.(*msg.Vote)
		if processed {
			n.listener.OnVoteRejected(msgItem.ID, msgVote.Vote)
		}
	case msg.CmdPing:
		msgPing, processed := m.(*msg.Ping)
		if processed {
			n.listener.OnPing(msgItem.ID, uint32(msgPing.Nonce))
		}
	case msg.CmdPong:
		msgPong, processed := m.(*msg.Pong)
		if processed {
			n.listener.OnPong(msgItem.ID, uint32(msgPong.Nonce))
		}
	case elap2p.CmdBlock:
		msgBlock, processed := m.(*elamsg.Block)
		if processed {
			if block, ok := msgBlock.Serializable.(*types.Block); ok {
				n.listener.OnBlock(msgItem.ID, block)
			}
		}
	case msg.CmdInv:
		msgInv, processed := m.(*msg.Inventory)
		if processed {
			n.listener.OnInv(msgItem.ID, msgInv.BlockHash)
		}
	case msg.CmdGetBlock:
		msgGetBlock, processed := m.(*msg.GetBlock)
		if processed {
			n.listener.OnGetBlock(msgItem.ID, msgGetBlock.BlockHash)
		}
	case msg.CmdGetBlocks:
		msgGetBlocks, processed := m.(*msg.GetBlocks)
		if processed {
			n.listener.OnGetBlocks(msgItem.ID, msgGetBlocks.StartBlockHeight, msgGetBlocks.EndBlockHeight)
		}
	case msg.CmdResponseBlocks:
		msgResponseBlocks, processed := m.(*msg.ResponseBlocks)
		if processed {
			n.listener.OnResponseBlocks(msgItem.ID, msgResponseBlocks.BlockConfirms)
		}
	case msg.CmdRequestConsensus:
		msgRequestConsensus, processed := m.(*msg.RequestConsensus)
		if processed {
			n.listener.OnRequestConsensus(msgItem.ID, msgRequestConsensus.Height)
		}
	case msg.CmdResponseConsensus:
		msgResponseConsensus, processed := m.(*msg.ResponseConsensus)
		if processed {
			n.listener.OnResponseConsensus(msgItem.ID, &msgResponseConsensus.Consensus)
		}
	case msg.CmdRequestProposal:
		msgRequestProposal, processed := m.(*msg.RequestProposal)
		if processed {
			n.listener.OnRequestProposal(msgItem.ID, msgRequestProposal.ProposalHash)
		}
	case msg.CmdIllegalProposals:
		msgIllegalProposals, processed := m.(*msg.IllegalProposals)
		if processed {
			n.listener.OnIllegalProposalReceived(msgItem.ID, &msgIllegalProposals.Proposals)
		}
	case msg.CmdIllegalVotes:
		msgIllegalVotes, processed := m.(*msg.IllegalVotes)
		if processed {
			n.listener.OnIllegalVotesReceived(msgItem.ID, &msgIllegalVotes.Votes)
		}
	case msg.CmdSidechainIllegalData:
		msgSidechainIllegal, processed := m.(*msg.SidechainIllegalData)
		if processed {
			n.listener.OnSidechainIllegalEvidenceReceived(&msgSidechainIllegal.Data)
		}
	case elap2p.CmdTx:
		msgTx, processed := m.(*elamsg.Tx)
		if processed {
			if tx, ok := msgTx.Serializable.(*types.Transaction); ok && tx.IsInactiveArbitrators() {
				n.listener.OnInactiveArbitratorsReceived(tx)
			}
		}
	case msg.CmdResponseInactiveArbitrators:
		msgResponse, processed := m.(*msg.ResponseInactiveArbitrators)
		if processed {
			n.listener.OnResponseInactiveArbitratorsReceived(
				&msgResponse.TxHash, msgResponse.Signer, msgResponse.Sign)
		}
	}
}
//保存直连的超级节点信息
func (n *network) saveDirectPeers() {
	var peers []*interfaces.DirectPeers
	for k, v := range n.directPeers {
		if !v.NeedConnect {
			continue
		}
		pk, err := common.HexStringToBytes(k)
		if err != nil {
			continue
		}
		peers = append(peers, &interfaces.DirectPeers{
			PublicKey: pk,
			Address:   v.Address.Addr,
			Sequence:  v.Sequence,
		})
	}
	n.store.SaveDirectPeers(peers)
}
//切换视图
func (n *network) changeView() {
	n.listener.OnChangeView()
}
//收到区块
func (n *network) blockReceived(b *types.Block, confirmed bool) {
	n.listener.OnBlockReceived(b, confirmed)
}
//区块确认
func (n *network) confirmReceived(p *payload.Confirm) {
	n.listener.OnConfirmReceived(p)
}
//收到非法的块
func (n *network) illegalBlocksReceived(i *payload.DPOSIllegalBlocks) {
	n.listener.OnIllegalBlocksTxReceived(i)
}

//收到侧链非法证据
func (n *network) sidechainIllegalEvidenceReceived(
	s *payload.SidechainIllegalData) {
	n.BroadcastMessage(&msg.SidechainIllegalData{Data: *s})
	n.listener.OnSidechainIllegalEvidenceReceived(s)
}
//获得proposalDispatcher当前高度
func (n *network) getCurrentHeight(pid peer.PID) uint64 {
	return uint64(n.proposalDispatcher.CurrentHeight())
}
//新建一个DposNetwork
func NewDposNetwork(pid peer.PID, listener manager.NetworkEventListener, dposAccount account.DposAccount) (*network, error) {
	network := &network{
		listener:                 listener,
		directPeers:              make(map[string]*PeerItem),
		messageQueue:             make(chan *messageItem, 10000), //todo config handle capacity though config file
		quit:                     make(chan bool),
		changeViewChan:           make(chan bool),
		blockReceivedChan:        make(chan blockItem, 10),        //todo config handle capacity though config file
		confirmReceivedChan:      make(chan *payload.Confirm, 10), //todo config handle capacity though config file
		illegalBlocksEvidence:    make(chan *payload.DPOSIllegalBlocks),
		sidechainIllegalEvidence: make(chan *payload.SidechainIllegalData),
		currentHeight:            blockchain.DefaultLedger.Blockchain.GetHeight() - 1,
		account:                  dposAccount,
	}

	notifier := p2p.NewNotifier(p2p.NFNetStabled|p2p.NFBadNetwork, network.notifyFlag)

	server, err := p2p.NewServer(&p2p.Config{
		PID:              pid,
		MagicNumber:      config.Parameters.ArbiterConfiguration.Magic,
		ProtocolVersion:  config.Parameters.ArbiterConfiguration.ProtocolVersion,
		Services:         config.Parameters.ArbiterConfiguration.Services,
		DefaultPort:      config.Parameters.ArbiterConfiguration.NodePort,
		MakeEmptyMessage: makeEmptyMessage,
		HandleMessage:    network.handleMessage,
		PingNonce:        network.getCurrentHeight,
		PongNonce:        network.getCurrentHeight,
		SignNonce:        dposAccount.SignPeerNonce,
		StateNotifier:    notifier,
	})
	if err != nil {
		return nil, err
	}

	network.p2pServer = server
	return network, nil
}
//生成各个网络包的空消息
func makeEmptyMessage(cmd string) (message elap2p.Message, err error) {
	switch cmd {
	case elap2p.CmdBlock:
		message = elamsg.NewBlock(&types.Block{})
	case msg.CmdAcceptVote:
		message = &msg.Vote{Command: msg.CmdAcceptVote}
	case msg.CmdReceivedProposal:
		message = &msg.Proposal{}
	case msg.CmdRejectVote:
		message = &msg.Vote{Command: msg.CmdRejectVote}
	case msg.CmdInv:
		message = &msg.Inventory{}
	case msg.CmdGetBlock:
		message = &msg.GetBlock{}
	case msg.CmdGetBlocks:
		message = &msg.GetBlocks{}
	case msg.CmdResponseBlocks:
		message = &msg.ResponseBlocks{}
	case msg.CmdRequestConsensus:
		message = &msg.RequestConsensus{}
	case msg.CmdResponseConsensus:
		message = &msg.ResponseConsensus{}
	case msg.CmdRequestProposal:
		message = &msg.RequestProposal{}
	case msg.CmdIllegalProposals:
		message = &msg.IllegalProposals{}
	case msg.CmdIllegalVotes:
		message = &msg.IllegalVotes{}
	case msg.CmdSidechainIllegalData:
		message = &msg.SidechainIllegalData{}
	default:
		return nil, errors.New("Received unsupported message, CMD " + cmd)
	}
	return message, nil
}
