package cn90

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The DNF90 server listens on one game port per channel: the instance's
// client.gamePortBase plus the channel id (the 90CN native default base is
// 11000; the legacy S4A21 base of 10000 does not apply to this server). The
// channel list comes from the runtime channel_info.etc, whose `[server] <id>`
// rows are `channelID nameKey type areaKey ...`. The channel directory port
// (server.channelListen) is a different protocol and must not be dialed.
const (
	defaultNativeGamePortBase = 11000
	defaultChannelFile        = "data/dnf/channel_info.etc"
)

type gameChannel struct {
	ID    int
	Type  int
	Group string
}

// channelCatalog holds the ranked dialable game ports for the configured
// server group.
type channelCatalog struct {
	ports []int
}

// singlePortCatalog pins one explicit operator override.
func singlePortCatalog(port int) channelCatalog {
	return channelCatalog{ports: []int{port}}
}

// loadChannelCatalog reads the runtime channel_info.etc. The server index
// selects the server group the launcher advertises (1 by default).
func loadChannelCatalog(runtimeRoot, relativePath string, serverIndex, portBase int) (channelCatalog, error) {
	value := strings.TrimSpace(relativePath)
	if value == "" {
		value = defaultChannelFile
	}
	if filepath.IsAbs(value) {
		return channelCatalog{}, fmt.Errorf("instance game.channelInfoPath %q must be relative to the runtime directory", value)
	}
	path := filepath.Join(runtimeRoot, filepath.FromSlash(value))
	data, err := os.ReadFile(path)
	if err != nil {
		return channelCatalog{}, fmt.Errorf("read 90CN channel info: %w", err)
	}
	channels, err := parseChannelInfo(string(data), serverIndex)
	if err != nil {
		return channelCatalog{}, fmt.Errorf("parse 90CN channel info %s: %w", path, err)
	}
	return rankChannelCatalog(channels, portBase), nil
}

// parseChannelInfo implements the subset of the server's channel_info.etc
// parser the robot needs: server rows with channel id, type and area group.
// Dungeon blocks are skipped; text keys may be backtick quoted.
func parseChannelInfo(text string, serverIndex int) ([]gameChannel, error) {
	text = strings.TrimPrefix(text, "\ufeff")
	lines := strings.Split(text, "\n")
	section := ""
	serverID := 0
	channels := make([]gameChannel, 0, 32)
	for _, raw := range lines {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[server]") {
			rest := strings.TrimSpace(strings.TrimPrefix(line, "[server]"))
			serverID = 0
			if rest != "" {
				id, err := strconv.Atoi(strings.Fields(rest)[0])
				if err != nil || id < 0 {
					return nil, fmt.Errorf("invalid server id %q", rest)
				}
				serverID = id
			}
			section = "server"
			continue
		}
		if strings.HasPrefix(line, "[/server]") {
			section = ""
			continue
		}
		if strings.HasPrefix(line, "[dungeon]") {
			section = "dungeon"
			continue
		}
		if strings.HasPrefix(line, "[/dungeon]") {
			section = ""
			continue
		}
		if section != "server" {
			continue
		}
		fields := splitChannelFields(line)
		if len(fields) < 4 {
			if len(fields) == 1 {
				// A bare server id continues the current group.
				if id, err := strconv.Atoi(fields[0]); err == nil && id >= 0 {
					serverID = id
					continue
				}
			}
			return nil, fmt.Errorf("server row requires channel id, name, type and area: %q", line)
		}
		channelID, err := strconv.Atoi(fields[0])
		if err != nil || channelID <= 0 {
			return nil, fmt.Errorf("invalid channel id %q", fields[0])
		}
		channelType, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("invalid channel type %q", fields[2])
		}
		group := strings.ToLower(strings.Trim(fields[3], "`[]"))
		if serverID != serverIndex {
			continue
		}
		channels = append(channels, gameChannel{ID: channelID, Type: channelType, Group: group})
	}
	if len(channels) == 0 {
		return nil, fmt.Errorf("no channels for server index %d", serverIndex)
	}
	return channels, nil
}

// splitChannelFields splits one row into fields, keeping backtick quoted
// values together.
func splitChannelFields(line string) []string {
	fields := make([]string, 0, 8)
	var current strings.Builder
	inQuote := false
	flush := func() {
		if current.Len() == 0 {
			return
		}
		fields = append(fields, current.String())
		current.Reset()
	}
	for _, r := range line {
		switch {
		case r == '`':
			inQuote = !inQuote
			current.WriteRune(r)
		case (r == ' ' || r == '\t') && !inQuote:
			flush()
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return fields
}

// isSpecialChannel classifies by area group only. The type column cannot be
// used: this server marks the ordinary [granfloris] town channel as type 11,
// which the inherited S4A21 type table treats as special, while the raid
// groups (anton_raid, luke_raid) that must stay out of the pool carry types
// that table never listed.
func isSpecialChannel(channel gameChannel) bool {
	if strings.Contains(channel.Group, "raid") {
		return true
	}
	switch channel.Group {
	case "crack", "pvp", "trade", "deathtower":
		return true
	}
	return false
}

func isCrackChannel(channel gameChannel) bool {
	return channel.Group == "crack"
}

// rankChannelCatalog selects the dialable channel pool: ordinary resident
// channels first, then the crack bootstrap channel, then any remaining
// entrance. Robots never spread into PvP or raid entrances while a better
// tier exists. Ports are portBase + channel id.
func rankChannelCatalog(channels []gameChannel, portBase int) channelCatalog {
	normal := make([]int, 0, len(channels))
	crack := make([]int, 0, len(channels))
	other := make([]int, 0, len(channels))
	for _, channel := range channels {
		port := portBase + channel.ID
		switch {
		case !isSpecialChannel(channel):
			normal = append(normal, port)
		case isCrackChannel(channel):
			crack = append(crack, port)
		default:
			other = append(other, port)
		}
	}
	pool := normal
	if len(pool) == 0 {
		pool = crack
	}
	if len(pool) == 0 {
		pool = other
	}
	return channelCatalog{ports: pool}
}

// portForAccount spreads accounts across the ranked channel ports so a single
// robot process can populate more than one channel deterministically.
func (c channelCatalog) portForAccount(account string, fallback int) int {
	if len(c.ports) == 0 {
		return fallback
	}
	if len(c.ports) == 1 {
		return c.ports[0]
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(strings.TrimSpace(strings.ToLower(account))))
	return c.ports[int(hash.Sum32())%len(c.ports)]
}
