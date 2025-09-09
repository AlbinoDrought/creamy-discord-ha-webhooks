package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"go.albinodrought/creamy-discord-ha-webhooks/internal/garageeventstream"
)

var (
	guildID   string
	channelID string
	garageURL string
)

type DoorOperation int

const (
	DoorOperationWaiting DoorOperation = iota
	DoorOperationOpen
	DoorOperationClose
	DoorOperationQuery
)

type State struct {
	ges    *garageeventstream.GarageEventStream
	change chan bool

	MessageID string // todo: persist this

	DoorState          garageeventstream.DoorState
	DoorStateChangedAt *time.Time

	DoorOperation DoorOperation

	lock          sync.RWMutex
	operationLock sync.Mutex
}

func (s *State) Update(cb func()) {
	s.lock.Lock()
	cb()
	s.lock.Unlock()
	select {
	case s.change <- true:
	default:
	}
}

type DiscordMessage struct {
	Content    string
	Components []discordgo.MessageComponent
}

func (gdsm *State) ToDiscordMessage() DiscordMessage {
	gdsm.lock.RLock()
	defer gdsm.lock.RUnlock()

	var open = discordgo.Button{
		Label:    "Open Garage",
		Style:    discordgo.PrimaryButton,
		CustomID: "a-1-go",
	}
	var close = discordgo.Button{
		Label:    "Close Garage",
		Style:    discordgo.SecondaryButton,
		CustomID: "a-1-gc",
	}
	var query = discordgo.Button{
		Label:    "Check Status",
		Style:    discordgo.SecondaryButton,
		CustomID: "a-1-gq",
	}

	// if gdsm.DoorOperation != DoorOperationWaiting && gdsm.DoorOperation != DoorOperationQuery {
	// 	open.Disabled = true
	// 	close.Disabled = true
	// }

	var operationText string
	switch gdsm.DoorOperation {
	case DoorOperationWaiting:
		operationText = "Ready"
	case DoorOperationOpen:
		operationText = "Opening Door"
	case DoorOperationClose:
		operationText = "Closing Door"
	case DoorOperationQuery:
		operationText = "Refreshing Door Status"
	}

	var statusText string
	if gdsm.DoorStateChangedAt == nil || gdsm.DoorState == garageeventstream.DoorStateUnknown {
		statusText = "(door status unknown)"
	} else {
		statusText = fmt.Sprintf("Door: %v since %v", gdsm.DoorState.String(), gdsm.DoorStateChangedAt.Format("3:04PM on 2006-01-02"))
	}

	content := fmt.Sprintf("%v\n%v", operationText, statusText)

	return DiscordMessage{
		Content: content,
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					open,
					close,
					query,
				},
			},
		},
	}
}

var state State

func main() {
	authenticationToken := os.Getenv("CDHAW_TOKEN")
	guildID = os.Getenv("CDHAW_GUILD_ID")
	channelID = os.Getenv("CDHAW_CHANNEL_ID")
	garageURL = os.Getenv("CDHAW_GARAGE_URL")

	if authenticationToken == "" || guildID == "" || channelID == "" || garageURL == "" {
		log.Fatal("require CDHAW_TOKEN, CDHAW_GUILD_ID, CDHAW_CHANNEL_ID, CDHAW_GARAGE_URL")
	}

	state.change = make(chan bool, 10)

	events := make(chan garageeventstream.Event, 1)
	state.ges = garageeventstream.New(garageURL, events)
	go func() {
		quickFails := 0
		lastRestart := time.Now()
		for {
			err := state.ges.Poll()
			if err != nil && err != context.Canceled {
				log.Printf("error during ges poll: %v", err)
			}
			if time.Since(lastRestart) < time.Minute {
				quickFails++
				if quickFails > 3 {
					time.Sleep(time.Minute)
				}
			} else {
				quickFails = 0
			}
			lastRestart = time.Now()
		}
	}()
	go func() {
		for event := range events {
			state.Update(func() {
				now := time.Now()
				state.DoorState = event.Mapped
				state.DoorStateChangedAt = &now
			})
		}
	}()

	session, err := discordgo.New("Bot " + authenticationToken)
	if err != nil {
		log.Fatal("failed to create discord session: ", err)
	}
	session.AddHandler(ready)
	session.AddHandler(interactionCreate)

	updateMessage := func(session *discordgo.Session) error {
		msg := state.ToDiscordMessage()

		state.lock.Lock()
		defer state.lock.Unlock()

		if state.MessageID == "" {
			st, err := session.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
				Content:    msg.Content,
				Components: msg.Components,
			})
			if err != nil {
				return err
			}
			state.MessageID = st.ID
		} else {
			_, err := session.ChannelMessageEditComplex(&discordgo.MessageEdit{
				ID:         state.MessageID,
				Channel:    channelID,
				Content:    &msg.Content,
				Components: &msg.Components,
			})
			if err != nil {
				return err
			}
		}

		return nil
	}

	// session.Identify.Intents = discordgo.IntentsGuildMessages

	if err := session.Open(); err != nil {
		log.Fatal("failed to open discord session: ", err)
	}
	defer session.Close()

	go func() {
		for range state.change {
			if err := updateMessage(session); err != nil {
				log.Printf("error updating message: %v", err)
			} else {
				log.Println("sent message")
			}
		}
	}()

	_, err = session.ApplicationCommandCreate(session.State.Application.ID, guildID, &discordgo.ApplicationCommand{
		Name:        "assistant-buttons",
		Description: "Send a message containing the assistant buttons",
	})
	if err != nil {
		log.Fatal(err)
	}

	log.Println("I'm running 😊")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc
	log.Println("I'm closing 😢")
}

func interactionCreate(s *discordgo.Session, event *discordgo.InteractionCreate) {
	if event.Interaction.GuildID != guildID || event.Interaction.ChannelID != channelID {
		log.Printf("received interaction from unconfigured guild/channel: %v %v", event.Interaction.GuildID, event.Interaction.ChannelID)
		return
	}

	var err error
	if event.Interaction.Type == discordgo.InteractionApplicationCommand {
		if event.Interaction.ApplicationCommandData().Name == "assistant-buttons" {
			msg := state.ToDiscordMessage()
			err = s.InteractionRespond(event.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content:    msg.Content,
					Components: msg.Components,
				},
			})
			if err != nil {
				log.Printf("failed to response to assistant-buttons command: %v", err)
			}
			st, err := s.InteractionResponse(event.Interaction)
			if err != nil {
				log.Printf("failed to get response to assistant-buttons command: %v", err)
			} else {
				state.lock.Lock()
				state.MessageID = st.ID
				state.lock.Unlock()
			}
			return
		}
	}

	if event.Interaction.Type == discordgo.InteractionMessageComponent {
		if !state.operationLock.TryLock() {
			log.Printf("could not obtain lock, ignoring interaction")
			return
		}
		defer state.operationLock.Unlock()

		if event.Interaction.Message != nil {
			state.lock.Lock()
			state.MessageID = event.Interaction.Message.ID
			state.lock.Unlock()
		}

		switch event.Interaction.MessageComponentData().CustomID {
		case "a-1-go":
			err = s.InteractionRespond(event.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseDeferredMessageUpdate,
			})
			if err != nil {
				log.Printf("failed to response to a-1-go command: %v", err)
			}

			state.Update(func() {
				state.DoorOperation = DoorOperationOpen
			})

			// todo: call open webhook here
			time.Sleep(3 * time.Second)
			// todo: wait for open to finish here

			state.Update(func() {
				state.DoorOperation = DoorOperationWaiting
			})

			return
		case "a-1-gc":
			err = s.InteractionRespond(event.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseDeferredMessageUpdate,
			})
			if err != nil {
				log.Printf("failed to response to a-1-gc command: %v", err)
			}

			state.Update(func() {
				state.DoorOperation = DoorOperationClose
			})

			// todo: call close webhook here
			time.Sleep(3 * time.Second)
			// todo: wait for close to finish here

			state.Update(func() {
				state.DoorOperation = DoorOperationWaiting
			})

			return
		case "a-1-gq":
			err = s.InteractionRespond(event.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseDeferredMessageUpdate,
			})
			if err != nil {
				log.Printf("failed to response to a-1-gc command: %v", err)
			}

			state.Update(func() {
				now := time.Now()
				state.DoorOperation = DoorOperationQuery
				state.DoorState = garageeventstream.DoorStateUnknown
				state.DoorStateChangedAt = &now
			})
			state.ges.Close()

			changed := false
			for range 30 {
				time.Sleep(time.Second)
				state.lock.Lock()
				if state.DoorState != garageeventstream.DoorStateUnknown {
					changed = true
				}
				state.lock.Unlock()
				if changed {
					break
				}
			}
			if !changed {
				log.Printf("no change after 30s, skipping status query")
			}

			state.Update(func() {
				state.DoorOperation = DoorOperationWaiting
			})
			return
		}
	}
}

func ready(s *discordgo.Session, event *discordgo.Ready) {
	s.UpdateCustomStatus("Howdy Neighbour")
}
