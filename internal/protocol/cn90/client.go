package cn90

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"robot/internal/foundation/lockhub"
	"robot/internal/foundation/network"
)

// defaultWriteTimeout bounds a single framed write. Without it a peer that
// stops reading can block the write path (and the shared sendMu) forever.
const defaultWriteTimeout = 15 * time.Second

// Client is one DNF90 game-channel connection. The adapter binds the account
// through the launcher admin API before dialing; the client itself only speaks
// the game wire.
type Client struct {
	conn         net.Conn
	sendMu       lockhub.Locker
	sequence     uint16
	headerSize   int
	maxSize      int
	writeTimeout time.Duration
}

// Dial opens a game-channel connection. The address must be the channel game
// port (GamePortBase + channel id), not the channel directory port.
func Dial(ctx context.Context, address string) (*Client, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// Keepalive lets the kernel eventually surface a half-open peer (crashed
	// server or black-holed link) instead of blocking reads forever.
	conn, err := (&net.Dialer{KeepAlive: 30 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	return NewClient(conn), nil
}

func NewClient(conn net.Conn) *Client {
	return &Client{
		conn:         conn,
		headerSize:   upperHeaderSize16,
		maxSize:      DefaultMaxPacketSize,
		writeTimeout: defaultWriteTimeout,
	}
}

// SetUpperHeaderSize switches the inbound parser between the locked server16
// profile (16, the default) and the historical 13-byte channel header.
func (c *Client) SetUpperHeaderSize(size int) {
	if c == nil {
		return
	}
	if size != upperHeaderSize16 && size != upperHeaderSize13 {
		return
	}
	c.headerSize = size
}

func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// CompleteHandshake drives the bound-session login exchange: an account-bound
// connection first receives the class0/op1 CHANNELINFO notice and must answer
// it with the class1/op1 endpoint request; an unbound connection already
// received the class1/op1 success from the server fallback account.
func (c *Client) CompleteHandshake(ctx context.Context) (Packet, error) {
	return c.CompleteHandshakeFrom(ctx, nil)
}

// CompleteHandshakeFrom continues the handshake when the caller already read
// the first inbound packet (the account-binding critical section keeps that
// read together with registration and dial).
func (c *Client) CompleteHandshakeFrom(ctx context.Context, first *Packet) (Packet, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	noticeSeen := false
	pending := first
	for {
		var packet Packet
		if pending != nil {
			packet = *pending
			pending = nil
		} else {
			var err error
			packet, err = c.Read(ctx)
			if err != nil {
				return Packet{}, err
			}
		}
		switch {
		case packet.Class == ClassCommand && packet.Type == ResponseEndpoint:
			if !EndpointResultOK(packet.Body) {
				return Packet{}, fmt.Errorf("cn90 endpoint response is not a success")
			}
			return packet, nil
		case packet.Class == ClassNotice && packet.Type == NotiChannelInfo:
			if noticeSeen {
				continue
			}
			noticeSeen = true
			if err := c.send(ctx, CmdEndpointRequest, EndpointRequestBody()); err != nil {
				return Packet{}, err
			}
		}
	}
}

// RequestRoster asks for the account character list (class0/op2 response).
func (c *Client) RequestRoster(ctx context.Context) error {
	return c.send(ctx, CmdGetUserInfo, RosterRequestBody())
}

// SelectCharacter selects one roster slot (class1/op4 response).
func (c *Client) SelectCharacter(ctx context.Context, slot uint16) error {
	return c.send(ctx, CmdSelectCharacter, SelectCharacterBody(slot))
}

// ProgressInitialTown emits the op143 checkpoint that the server's initial
// town route waits for after a completed character selection.
func (c *Client) ProgressInitialTown(ctx context.Context) error {
	return c.send(ctx, CmdChangeTutorial, TutorialProgressBody(InitialTownProgress))
}

// SetUserArea requests one town/area location (class1/op36 request shape).
func (c *Client) SetUserArea(ctx context.Context, town, area byte, x, y int16, direction byte) error {
	return c.send(ctx, CmdSetUserArea, SetUserAreaBody(town, area, x, y, direction))
}

// SetUserAreaPortal requests an area in another town using the portal shape:
// the character's current town travels in the first opaque field, which the
// server requires before it admits a cross-town transition.
func (c *Client) SetUserAreaPortal(ctx context.Context, town, area byte, x, y int16, direction byte, sourceTown uint16) error {
	return c.send(ctx, CmdSetUserArea, SetUserAreaPortalBody(town, area, x, y, direction, sourceTown))
}

// SetUserPosition reports a town position update.
func (c *Client) SetUserPosition(ctx context.Context, x, y int16, movementCode byte, opaqueScaled uint16) error {
	return c.send(ctx, CmdSetUserPosition, SetUserPositionBody(x, y, movementCode, opaqueScaled))
}

// ChangeEmotion sets the character's mood/emotion index. The server persists
// it and answers on the same opcode.
func (c *Client) ChangeEmotion(ctx context.Context, emotionIndex uint16) error {
	return c.send(ctx, CmdChangeEmotion, ChangeEmotionBody(emotionIndex))
}

// EnterDungeonSelect opens the dungeon selector. The server ignores the body
// and answers with the selector context; that reply is what binds the
// character's current town position as the return origin a later entry needs,
// so it has to be sent once the character has a valid town position.
func (c *Client) EnterDungeonSelect(ctx context.Context) error {
	return c.send(ctx, CmdEnterSelectDungeon, nil)
}

// SelectDungeon commits a dungeon entry. The server makes the run active
// before it answers, so a successful acknowledgement means the dungeon is
// already running.
func (c *Client) SelectDungeon(ctx context.Context, dungeonID uint32, difficulty byte) error {
	return c.send(ctx, CmdSelectDungeon, SelectDungeonBody(dungeonID, difficulty))
}

// FinishLoading acknowledges that the dungeon scene has loaded. The server
// gates the body on its length alone and reads no field, but it releases the
// character-state push, the skill frame and the room placement only once this
// arrives, so an entry that stops at the op16 acknowledgement leaves the
// character inside a run it never finishes loading into.
func (c *Client) FinishLoading(ctx context.Context) error {
	return c.send(ctx, CmdFinishLoading, FinishLoadingBody())
}

// GiveUpDungeon abandons the run the character is inside. The server answers
// with the typed scene transition back to the town the entry froze, and with
// nothing else; it drops the request without a reply while no run is active.
func (c *Client) GiveUpDungeon(ctx context.Context) error {
	return c.send(ctx, CmdGiveupGame, nil)
}

// ReportMonsterDeath reports one actor of the current room dead. The body is
// the shape a live native client writes, with only its target and owner keys
// replaced; the server validates those two and retains the rest.
func (c *Client) ReportMonsterDeath(ctx context.Context, targetObjectKey uint32, ownerObjectKey uint16) error {
	return c.send(ctx, CmdDieMonster, DieMonsterTemplateBody(targetObjectKey, ownerObjectKey))
}

// MoveDungeonRoom asks the server to walk the run into the room at nextX/nextY.
// The answer is the room itself - another op29 - so this returns as soon as the
// request is on the wire; a refused move is never answered at all.
func (c *Client) MoveDungeonRoom(ctx context.Context, nextX, nextY byte) error {
	return c.send(ctx, CmdMoveMap, MoveMapBody(nextX, nextY))
}

// CheckBossDeath asks the server to close the run on a boss it has already been
// told is dead. The related actor is the acting character's own object key.
func (c *Client) CheckBossDeath(ctx context.Context, relatedActorObjectKey, targetObjectKey uint16) error {
	return c.send(ctx, CmdBossDieCheck, BossDieCheckBody(relatedActorObjectKey, targetObjectKey))
}

// SetPlayResult answers the settlement entry with the client's end-of-run
// statement.
func (c *Client) SetPlayResult(ctx context.Context) error {
	return c.send(ctx, CmdSetPlayResult, SetPlayResultBody())
}

// SetCharacterStatistic sends the op123 barrier the server requires after the
// play result before a completed run can be left. The server answers nothing;
// the packet itself is the statement.
func (c *Client) SetCharacterStatistic(ctx context.Context) error {
	return c.send(ctx, CmdCharacterStatistic, CharacterStatisticBody())
}

// RequestScoreScrollState asks the settlement page for its score-scroll state.
// The server answers with the class-1 op69 acknowledgement, which is the gate
// of the card-layout request.
func (c *Client) RequestScoreScrollState(ctx context.Context) error {
	return c.send(ctx, CmdScoreScrollState, nil)
}

// RequestCardSelectRightState asks for the settlement card layout. The server
// answers with the class-1 op70 layout; without that send its exit gate treats
// the card chain as unfinished and silently drops op42.
func (c *Client) RequestCardSelectRightState(ctx context.Context) error {
	return c.send(ctx, CmdCardSelectRightState, nil)
}

// DeclareDeath reports the character as dead. Death is client-declared in this
// protocol generation; the server arms a ten second timer that returns the
// character to town unless a revive arrives first.
func (c *Client) DeclareDeath(ctx context.Context, x, y int16) error {
	return c.send(ctx, CmdDieCharacter, DieCharacterBody(x, y))
}

// UseCoin asks the server to revive the acting character. It is only accepted
// while the death timer is armed.
func (c *Client) UseCoin(ctx context.Context, objectKey uint16) error {
	return c.send(ctx, CmdUseCoin, UseCoinBody(objectKey))
}

// SendPeerChat delivers one directed chat message to another character. The
// server only forwards it while both characters stand in the same town area,
// and it only answers the request when it does.
func (c *Client) SendPeerChat(ctx context.Context, messageType byte, targetID uint16, message, targetName []byte) error {
	body, err := PeerChatRequestBody(messageType, targetID, message, targetName)
	if err != nil {
		return err
	}
	return c.send(ctx, CmdPeerChat, body)
}

// TownSceneReady sends the legacy type1345 u32(2) acknowledgement the live
// client emits right after the first typed op24. The server uses it to release
// the deferred scene tail and the HUD gauges.
func (c *Client) TownSceneReady(ctx context.Context) error {
	return c.send(ctx, CmdTownSceneReady, []byte{2, 0, 0, 0})
}

// CheckConnection sends the heartbeat. The server answers only after a
// character has been selected.
func (c *Client) CheckConnection(ctx context.Context) error {
	return c.send(ctx, CmdCheckConnection, nil)
}

// Exit announces an orderly channel exit. Callers close the socket afterwards.
func (c *Client) Exit(ctx context.Context) error {
	return c.send(ctx, CmdExit, ExitBody())
}

// CreateCharacter creates one character (class1/op5 response plus a fresh
// class0/op2 roster).
func (c *Client) CreateCharacter(ctx context.Context, job byte, name []byte) error {
	body, err := CreateCharacterBody(job, name)
	if err != nil {
		return err
	}
	return c.send(ctx, CmdCreateCharacter, body)
}

// DeleteCharacter deletes one roster slot by slot and name.
func (c *Client) DeleteCharacter(ctx context.Context, slot uint16, name []byte) error {
	body, err := DeleteCharacterBody(slot, name)
	if err != nil {
		return err
	}
	return c.send(ctx, CmdDeleteCharacter, body)
}

// CheckCharacterName queries global name availability (class1/op692).
func (c *Client) CheckCharacterName(ctx context.Context, name []byte) error {
	body, err := CheckCharacterNameBody(name)
	if err != nil {
		return err
	}
	return c.send(ctx, CmdCheckCharacterName, body)
}

// CreateExpertJobStore sends the op598 create request. The {1} / {0,code}
// acknowledgement is observed by the session, not read here.
func (c *Client) CreateExpertJobStore(ctx context.Context, kind byte, name []byte, charge uint32, x, y int16, link uint16) error {
	body, err := ExpertJobStoreCreateBody(kind, name, charge, x, y, link)
	if err != nil {
		return err
	}
	return c.send(ctx, CmdCreateExpertStore, body)
}

// CloseExpertJobStore sends the empty op600 close request.
func (c *Client) CloseExpertJobStore(ctx context.Context) error {
	return c.send(ctx, CmdCloseExpertStore, nil)
}

// RequestPeer sends an op10 peer request. For the party invite modes the
// server records the invitation and forwards a class0/NotiRequestPeer prompt
// to the target; the class1 acknowledgement is observed by the session, not
// read here.
func (c *Client) RequestPeer(ctx context.Context, targetID uint16, mode byte, value0, value2 uint32) error {
	body, err := RequestPeerBody(targetID, mode, value0, value2)
	if err != nil {
		return err
	}
	return c.send(ctx, CmdRequestPeer, body)
}

// LeaveParty sends the empty op13 party leave. The acknowledgement and the
// emptied roster are observed by the session, not read here: the roster is what
// the caller waits on, because it is the same notice every other membership
// change arrives in.
func (c *Client) LeaveParty(ctx context.Context) error {
	return c.send(ctx, CmdLeaveParty, nil)
}

// ResponsePeer answers a forwarded peer prompt with the exact seven-byte op11
// shape. Party invites accept with value 0 (modes 0/12) or 1 (modes 4/13), and
// a trade prompt accepts with value 0.
func (c *Client) ResponsePeer(ctx context.Context, targetID uint16, mode byte, value uint32) error {
	return c.send(ctx, CmdResponsePeer, ResponsePeerBody(targetID, mode, value))
}

// MoveItemspace sends the twenty-eight byte op19 item move. Outside a trade the
// server routes it to the ordinary inventory handler; while a trade is open it
// is the trade window's offer and withdrawal command.
func (c *Client) MoveItemspace(ctx context.Context, request TradeMoveRequest) error {
	return c.send(ctx, CmdMoveItemspace, TradeMoveBody(request))
}

// ItemTradeState sends the one byte op24 trade-window state.
func (c *Client) ItemTradeState(ctx context.Context, state byte) error {
	body, err := TradeStateBody(state)
	if err != nil {
		return err
	}
	return c.send(ctx, CmdItemTradeState, body)
}

// MailboxOpen opens the session's mailbox (class1/op96). The body must be
// empty. The server answers twice: the class1 acknowledgement carries the
// not-loaded count, and a class0/0x61 notification carries the whole snapshot.
// Both are observed by the session.
func (c *Client) MailboxOpen(ctx context.Context) error {
	return c.send(ctx, CmdMailboxOpen, MailboxOpenBody())
}

// MailboxClaim extracts attachments by claim-object id (class1/op95). The ids
// come from a class0/0x61 snapshot: one per attachment row, and the letter's
// own wire id for a row that only carries gold. The server moves what fits into
// the character's bag and answers with the rows it settled.
//
// A letter whose contents are already gone is skipped rather than rejected, so
// a batch that mixes claimed and pending objects still succeeds; the server
// only reports failure when nothing in the batch was left to claim.
func (c *Client) MailboxClaim(ctx context.Context, claimObjectIDs []uint32) error {
	body, err := MailboxExtractBody(claimObjectIDs)
	if err != nil {
		return err
	}
	return c.send(ctx, CmdMailboxExtractItem, body)
}

// MailboxChangeState rewrites the state of the named letters (class1/op134).
// Status 0 deletes the letters and makes the server push class0/0x62 for each,
// 2 marks them read and 3 saves them. Claiming does not remove a letter, so
// deletion is what keeps a robot's mailbox from growing without bound.
func (c *Client) MailboxChangeState(ctx context.Context, mailIDs []uint32, status uint16) error {
	body, err := MailboxChangeStateBody(mailIDs, status)
	if err != nil {
		return err
	}
	return c.send(ctx, CmdChangeLetterStat, body)
}

// AuctionRegister lists one inventory stack (class1/op183). The acknowledgement
// is observed by the session: op183 answers as both the register result and the
// class0 service-open notice, so the two are told apart by packet class.
func (c *Client) AuctionRegister(ctx context.Context, listType byte, slot uint16, itemID, count, startPrice, buyoutPrice uint32) error {
	body, err := AuctionRegisterBody(listType, slot, itemID, count, startPrice, buyoutPrice)
	if err != nil {
		return err
	}
	return c.send(ctx, CmdAuctionRegister, body)
}

// AuctionCancel withdraws one of the session's own listings (class1/op184).
func (c *Client) AuctionCancel(ctx context.Context, listingID uint64) error {
	body, err := AuctionCancelBody(listingID)
	if err != nil {
		return err
	}
	return c.send(ctx, CmdAuctionCancel, body)
}

// AuctionBid places a bid on a listing (class1/op185).
func (c *Client) AuctionBid(ctx context.Context, price uint32, listingID uint64, sellerName []byte, itemID uint32) error {
	body, err := AuctionBidBody(price, listingID, sellerName, itemID)
	if err != nil {
		return err
	}
	return c.send(ctx, CmdAuctionBid, body)
}

// AuctionBuyPiece buys a whole listing or part of a stackable one
// (class1/op334). The server charges unit price times count and refuses a
// partial buy on a listing that already carries a bidder.
func (c *Client) AuctionBuyPiece(ctx context.Context, price, count uint32, listingID uint64, sellerName []byte, itemID uint32) error {
	body, err := AuctionPieceBody(price, count, listingID, sellerName, itemID)
	if err != nil {
		return err
	}
	return c.send(ctx, CmdAuctionBuyPiece, body)
}

// AuctionSearchByItem queries the auction page for specific item ids
// (class1/op186); an empty id list matches every listing.
func (c *Client) AuctionSearchByItem(ctx context.Context, offset uint32, itemIDs []uint32) error {
	body, err := AuctionSearchByItemBody(offset, itemIDs)
	if err != nil {
		return err
	}
	return c.send(ctx, CmdAuctionSearchItem, body)
}

// AuctionSearchCategory queries the auction page without an item filter
// (class1/op187). The server answers on op186.
func (c *Client) AuctionSearchCategory(ctx context.Context, offset uint32) error {
	return c.send(ctx, CmdAuctionSearchPage, AuctionSearchCategoryBody(offset))
}

// AuctionMyListings asks for the character's own listings (class1/op188).
func (c *Client) AuctionMyListings(ctx context.Context) error {
	return c.send(ctx, CmdAuctionMyListings, AuctionMineBody())
}

// AuctionMyBids asks for the character's own bids (class1/op189).
func (c *Client) AuctionMyBids(ctx context.Context) error {
	return c.send(ctx, CmdAuctionMyBids, AuctionMineBody())
}

// AuctionAveragePrice asks for the item's recent average price (class1/op182).
func (c *Client) AuctionAveragePrice(ctx context.Context, itemID uint32) error {
	body, err := AuctionAveragePriceBody(itemID)
	if err != nil {
		return err
	}
	return c.send(ctx, CmdAuctionAverage, body)
}

// Read reads one inbound packet with cancellation support.
func (c *Client) Read(ctx context.Context) (Packet, error) {
	if c == nil || c.conn == nil {
		return Packet{}, fmt.Errorf("cn90 client is closed")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	resetDeadline := interruptOnCancel(ctx, c.conn.SetReadDeadline)
	packet, err := ReadPacket(c.conn, c.headerSize, c.maxSize)
	resetDeadline()
	if err != nil && ctx.Err() != nil {
		return Packet{}, ctx.Err()
	}
	return packet, err
}

// Run reads packets until the connection ends or onPacket fails.
func (c *Client) Run(ctx context.Context, onPacket func(Packet) error) error {
	if c == nil || c.conn == nil {
		return fmt.Errorf("cn90 client is closed")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	resetDeadline := interruptOnCancel(ctx, c.conn.SetReadDeadline)
	defer resetDeadline()
	for {
		packet, err := c.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if onPacket != nil {
			if err := onPacket(packet); err != nil {
				return err
			}
		}
	}
}

func (c *Client) send(ctx context.Context, typ uint16, body []byte) error {
	if c == nil || c.conn == nil {
		return fmt.Errorf("cn90 client is closed")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	c.sequence++
	frame := EncodeLegacy(typ, body, c.sequence)
	writeTimeout := c.writeTimeout
	if writeTimeout <= 0 {
		writeTimeout = defaultWriteTimeout
	}
	deadline := time.Now().Add(writeTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := c.conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	resetDeadline := interruptOnCancel(ctx, c.conn.SetWriteDeadline)
	defer resetDeadline()
	err := network.WriteFull(c.conn, frame)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			// The caller canceled (interruptOnCancel unblocks the socket but
			// the connection stays usable for the next write).
			return ctxErr
		}
		if errors.Is(err, os.ErrDeadlineExceeded) {
			// Our own write timeout fired: a partial frame may have been
			// delivered, so close the connection to force a reconnect rather
			// than continuing on a desynchronized stream.
			_ = c.conn.Close()
		}
	}
	return err
}

// interruptOnCancel unblocks a pending socket operation and waits for the
// cancellation callback before clearing the deadline. Waiting prevents an old
// operation from installing an expired deadline after the next one begins.
func interruptOnCancel(ctx context.Context, setDeadline func(time.Time) error) func() {
	if ctx.Done() == nil {
		return func() {}
	}
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = setDeadline(time.Now())
		close(done)
	})
	return func() {
		if !stop() {
			<-done
		}
		_ = setDeadline(time.Time{})
	}
}
