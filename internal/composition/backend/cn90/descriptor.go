package cn90

import "robot/internal/shared"

const BackendID shared.BackendID = "sim_90cn"

func Info() shared.BackendInfo {
	capabilities := shared.CapabilityMatrix(shared.CapabilityStatus{Reason: "90CN capability is not implemented yet"})
	capabilities[shared.CapabilityProvision] = shared.CapabilityStatus{Enabled: true, Reason: "protocol character creation plus offline level/grow, equipment/avatar and pet generation; live verified"}
	capabilities[shared.CapabilityTownMove] = shared.CapabilityStatus{Enabled: true, Reason: "op36 town/area and op35 position requests follow the current client shapes; verified live"}
	capabilities[shared.CapabilityDungeonFollow] = shared.CapabilityStatus{Reason: "90CN party/dungeon follower is not implemented yet"}
	capabilities[shared.CapabilityDungeonRevive] = shared.CapabilityStatus{Enabled: true, Reason: "dungeon death and revive (op40/op41): death is client-declared, the server keeps no character hit points, records the report, answers a class0/op32 notice and arms a ten second timer that returns the character to town unless the op41 revive arrives first; the session answers that notice itself and no death or revive is ever broadcast to peers; live verified"}
	capabilities[shared.CapabilityParty] = shared.CapabilityStatus{Enabled: true, Reason: "town party invite auto-accept: the server forwards op10 invitations as a class0/0x0007 prompt to the same-area online target and the session answers op11 with the mode-specific accept value (0 for modes 0/12, 1 for modes 4/13); the mode-1 trade prompt is answered by CapabilityTrade instead, told apart by the extra byte the server appends only to a mode-1 op11 acknowledgement"}
	capabilities[shared.CapabilityGuildInvite] = shared.CapabilityStatus{Reason: "the server has no guild invite route: op151/op152 and the op347-351 join-application family have no handler; only guild creation and state projection exist"}
	capabilities[shared.CapabilityShout] = shared.CapabilityStatus{Enabled: true, Mode: shared.ShoutModeDirected, Reason: "same-area directed private chat (op17 request / op118 delivery notice): the server has no broadcast channel, so one request carries a target character id and name and is delivered only to that character while both stand in the same town area; a target that is offline or in another area is dropped silently, the class1 acknowledgement is the only success signal, and the session answers a received message from its own template pool with a per-sender cooldown; mode=directed keeps a target-less shout (the automatic template turn and the manual shout commands) refused instead of sent nowhere"}
	capabilities[shared.CapabilityStore] = shared.CapabilityStatus{Enabled: true, Reason: "disassembler machine (op598 kind 0) and enchanter stall (op598 kind 3) with offline profession preparation; the private item stall stays unsupported; live protocol verified"}
	capabilities[shared.CapabilityEmote] = shared.CapabilityStatus{Enabled: true, Reason: "mood/emotion change (op254): the client's mood window sends one u16 emotion index, the server stores it in the character's emotion_index stat without validating the range and echoes it back; the stored value is projected into the scene object tail, so the mood is visible to other players from their next actor-list rebuild"}
	capabilities[shared.CapabilityWorldShout] = shared.CapabilityStatus{Reason: "the server exposes no world or party chat channel; the only chat it forwards is same-area directed peer chat (op17/op118), which CapabilityShout covers"}
	capabilities[shared.CapabilityCleanup] = shared.CapabilityStatus{Enabled: true, Reason: "verified roster deletion protocol; live verified"}
	capabilities[shared.CapabilityDangerousDelete] = shared.CapabilityStatus{Enabled: true, Reason: "adapter-owned SQLite purge over the dnf_* tables; live verified (1197-account fleet purge, zero orphan rows)"}
	capabilities[shared.CapabilityPartyDebug] = shared.CapabilityStatus{Reason: "90CN party diagnostics are not implemented"}
	capabilities[shared.CapabilityMailNotification] = shared.CapabilityStatus{Enabled: true, Reason: "per-robot mailbox round: op96 opens the box (class1 ack plus the class0 snapshot), op95 claims every attachment the snapshot offers and op134 status 0 deletes the letters the round drained, so a robot collects its auction settlement instead of letting the box grow; live verified end to end against the auction settlement mail"}
	capabilities[shared.CapabilityDatabase] = shared.CapabilityStatus{Enabled: true, Mode: "sqlite_health", Reason: "validates the dnf_* schema of the configured SQLite file; live verified"}
	capabilities[shared.CapabilityDiagnostics] = shared.CapabilityStatus{Reason: "90CN diagnostics are not implemented"}
	capabilities[shared.CapabilitySystemAnnouncement] = shared.CapabilityStatus{Reason: "the DNF90 server implements no announcement broadcast"}
	capabilities[shared.CapabilityServerNotice] = shared.CapabilityStatus{Reason: "the DNF90 server implements no notice broadcast: op477 has no handler and op503 is a passive client report; no admin broadcast route exists"}
	capabilities[shared.CapabilityServiceControl] = shared.CapabilityStatus{Reason: "90CN service control is not implemented"}
	capabilities[shared.CapabilityDungeonMove] = shared.CapabilityStatus{Reason: "90CN dungeon movement is not implemented"}
	capabilities[shared.CapabilityMarket] = shared.CapabilityStatus{Enabled: true, Reason: "buyout-only listings (op183 register with the no-start-price sentinel) placed from the offline-seeded bag stock, and purchases of player listings under the price model (op186/op187 browse, op334 piece buy); a character's own listings are read back with op188; listing, purchase and the settlement-mail claim are live verified"}
	capabilities[shared.CapabilityTrade] = shared.CapabilityStatus{Enabled: true, Reason: "player-to-player item trade between two same-area characters: op10 mode 1 asks, op11 mode 1 opens the window, the offer is op19 (28 bytes, source bag row to the trade container) and the two boundaries are op24 (1 byte: 5 for adding complete, 3 for final consent), while class0/op15, /op17, /op16 and /op18 report the peer's offer, both states, a rollback and the atomic completion; the server owns every row (it assigns the visible rows 3..11 itself and echoes the row it chose in the op19 acknowledgement) and holds the first consent privately so the second participant still gets its dialog, which is why a session only learns the peer is ready by consenting itself; a session offers one stack of its offline-seeded stock, since a robot has no live bag read; live verified between two robots"}
	return shared.BackendInfo{
		ID: BackendID, DisplayName: "90CN", SupportedOS: []string{"linux", "windows"}, Selectable: true,
		Capabilities: capabilities, MaxOnline: 10000,
		Settings: []shared.BackendSetting{
			{Key: "server_directory", Label: "Server directory", LabelZH: "服务端目录", Hint: "DNF90 one-click project directory (containing runtime/) or the runtime directory itself; runtime/config/instance.json defines the channel, database and PVF paths.", HintZH: "DNF90 一键工程目录（包含 runtime/）或 runtime 目录本身；通道、数据库与 PVF 路径由 runtime/config/instance.json 定义。", InputType: "path", Required: true, RuntimeSource: "server_directory"},
			{Key: "server_host", Label: "Game host", LabelZH: "游戏地址", Hint: "Game channel host; the 90CN profile binds the channel to 127.0.0.1.", HintZH: "游戏通道地址；90CN 方案将通道绑定在 127.0.0.1。", InputType: "text", Required: true, Default: "127.0.0.1", RuntimeSource: "game_host"},
			{Key: "game_port", Label: "Port", LabelZH: "端口", Hint: "Optional channel port override; empty follows instance.json server.channelListen.", HintZH: "可选通道端口覆盖；留空则跟随 instance.json 的 server.channelListen。", InputType: "number", RuntimeSource: "game_port"},
			{Key: "database_path", Label: "Database", LabelZH: "数据库", Hint: "Optional SQLite override (absolute, or relative to the runtime directory); empty follows instance.json database.path.", HintZH: "可选 SQLite 覆盖（绝对路径，或相对 runtime 目录）；留空则跟随 instance.json 的 database.path。", InputType: "path", Placeholder: "runtime/data/dnf90.db (auto)"},
		},
	}
}
