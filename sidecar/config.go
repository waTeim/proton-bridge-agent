package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

const discordConfigPath = "/etc/sidecar/discord.yaml"

// DiscordConfig holds the Discord bot notification settings.
// Loaded from discordConfigPath; notifications are disabled if the file
// is absent or bot_token / channel_id are empty.
type DiscordConfig struct {
	// BotToken is the Discord bot token from the Developer Portal.
	// Create one at https://discord.com/developers/applications → your app → Bot → Token.
	BotToken string `yaml:"bot_token"`

	// ChannelID is the numeric ID of the channel to post messages to.
	// In Discord: enable Developer Mode (User Settings → Advanced), then
	// right-click the channel and choose "Copy Channel ID".
	ChannelID string `yaml:"channel_id"`

	// BatchWindowSeconds is how long (in seconds) the notifier waits after the
	// first new message before posting to Discord.  All messages that arrive
	// within the window are rolled into a single post, which avoids Discord's
	// per-channel rate limit when many messages arrive simultaneously.
	// Default: 5.  Set to 0 to use the default.
	BatchWindowSeconds int `yaml:"batch_window_seconds"`
}

// loadDiscordConfig reads discordConfigPath and returns the parsed config.
// Returns nil, nil when the file does not exist or required fields are empty,
// meaning Discord notifications are disabled.
func loadDiscordConfig() (*DiscordConfig, error) {
	data, err := os.ReadFile(discordConfigPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read discord config %s: %w", discordConfigPath, err)
	}

	var cfg DiscordConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse discord config: %w", err)
	}

	if cfg.BotToken == "" || cfg.ChannelID == "" {
		return nil, nil
	}
	if cfg.BatchWindowSeconds <= 0 {
		cfg.BatchWindowSeconds = 5
	}
	return &cfg, nil
}
