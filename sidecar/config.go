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

	// BodyPreviewWords is the maximum number of words to include in the
	// email body preview posted to Discord (default: 40).
	BodyPreviewWords int `yaml:"body_preview_words"`
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
	if cfg.BodyPreviewWords <= 0 {
		cfg.BodyPreviewWords = 40
	}
	return &cfg, nil
}
