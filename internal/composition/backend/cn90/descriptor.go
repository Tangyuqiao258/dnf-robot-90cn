package cn90

import "robot/internal/shared"

const BackendID shared.BackendID = "sim_90cn"

func Info() shared.BackendInfo {
	capabilities := shared.CapabilityMatrix(shared.CapabilityStatus{Reason: "90CN capability is not implemented yet"})
	capabilities[shared.CapabilityProvision] = shared.CapabilityStatus{Enabled: true, Reason: "protocol character creation plus offline level/grow, equipment/avatar and pet generation; live verified"}
	capabilities[shared.CapabilityTownMove] = shared.CapabilityStatus{Enabled: true, Reason: "op36 town/area and op35 position requests follow the current client shapes; verified live"}
	capabilities[shared.CapabilityDungeonFollow] = shared.CapabilityStatus{Reason: "90CN party/dungeon follower is not implemented yet"}
	capabilities[shared.CapabilityParty] = shared.CapabilityStatus{Enabled: true, Reason: "town party invite auto-accept: the server forwards op10 invitations as a class0/0x0007 prompt to the same-area online target and the session answers op11 with the mode-specific accept value (0 for modes 0/12, 1 for modes 4/13); the mode-1 trade prompt stays unanswered"}
	capabilities[shared.CapabilityGuildInvite] = shared.CapabilityStatus{Reason: "the server has no guild invite route: op151/op152 and the op347-351 join-application family have no handler; only guild creation and state projection exist"}
	capabilities[shared.CapabilityShout] = shared.CapabilityStatus{Reason: "the server has no broadcast shout; it only forwards same-area directed peer chat (op17 request carrying a target id and name, op118 notice delivered to that online target), so the shout semantics are redefined as directed private chat and stay off until a target-bearing intent exists"}
	capabilities[shared.CapabilityStore] = shared.CapabilityStatus{Enabled: true, Reason: "disassembler machine (op598 kind 0) and enchanter stall (op598 kind 3) with offline profession preparation; the private item stall stays unsupported; live protocol verified"}
	capabilities[shared.CapabilityWorldShout] = shared.CapabilityStatus{Reason: "the server exposes no world or area chat channel; chat is limited to same-area directed peer chat (op17/op118)"}
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
