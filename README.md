# Creamy Discord Home Assistant Webhooks

Show a nice message in a Discord channel.

Clicking buttons on that message performs actions in Home Assistant.

## Usage

```sh
CDHAW_TOKEN=your-discord-bot-token \
CDHAW_GUILD_ID=your-guild-id \
CDHAW_CHANNEL_ID=your-channel-id,your-other-channel-id \
CDHAW_GARAGE_URL=http://1.2.3.4/events \
go run main.go
```

See https://discord.com/developers/docs/topics/oauth2#bots for information on creating a Discord bot.
