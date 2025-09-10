package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"go.albinodrought/creamy-discord-ha-webhooks/internal/garageeventstream"
)

var (
	guildID         string
	channelID       string
	garageURL       string
	webhookOpenURL  string
	webhookCloseURL string
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

func (s *State) Read(cb func()) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	cb()
}

func (s *State) Update(cb func()) {
	defer func() {
		select {
		case s.change <- true:
		default:
		}
	}()
	s.lock.Lock()
	defer s.lock.Unlock()
	cb()
}

func (s *State) SaveMessageID(messageID string) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.SaveMessageIDAlreadyLocked(messageID)
}

func (s *State) SaveMessageIDAlreadyLocked(messageID string) {
	s.MessageID = messageID
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
	webhookOpenURL = os.Getenv("CDHAW_WEBHOOK_OPEN_URL")
	webhookCloseURL = os.Getenv("CDHAW_WEBHOOK_CLOSE_URL")

	if authenticationToken == "" || guildID == "" || channelID == "" || garageURL == "" || webhookOpenURL == "" || webhookCloseURL == "" {
		log.Fatal("require CDHAW_TOKEN, CDHAW_GUILD_ID, CDHAW_CHANNEL_ID, CDHAW_GARAGE_URL, CDHAW_WEBHOOK_OPEN_URL, CDHAW_WEBHOOK_CLOSE_URL")
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
			nc := false
			state.Read(func() {
				if state.DoorState != event.Mapped {
					nc = true
				}
			})
			if nc { // only trigger update if required
				state.Update(func() {
					now := time.Now()
					state.DoorState = event.Mapped
					state.DoorStateChangedAt = &now
				})
			}
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
			state.SaveMessageIDAlreadyLocked(st.ID)
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
				state.SaveMessageID(st.ID)
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
			state.SaveMessageID(event.Interaction.Message.ID)
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

			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()

			req, err := http.NewRequestWithContext(ctx, "POST", webhookOpenURL, nil)
			if err != nil {
				log.Printf("failed to create a-1-go request")
			} else {
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					log.Printf("failed to perform a-1-go request")
				} else {
					if resp.Body != nil {
						resp.Body.Close()
					}
				}
			}

			fin := false
			for range 30 {
				time.Sleep(time.Second)
				state.Read(func() {
					if state.DoorState == garageeventstream.DoorStateOpen {
						fin = true
					}
				})
				if fin {
					break
				}
			}
			if !fin {
				log.Printf("no change after 30s, skipping wait-for-close")
			}

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

			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()

			req, err := http.NewRequestWithContext(ctx, "POST", webhookCloseURL, nil)
			if err != nil {
				log.Printf("failed to create a-1-gc request")
			} else {
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					log.Printf("failed to perform a-1-gc request")
				} else {
					if resp.Body != nil {
						resp.Body.Close()
					}
				}
			}

			fin := false
			for range 30 {
				time.Sleep(time.Second)
				state.Read(func() {
					if state.DoorState == garageeventstream.DoorStateClosed {
						fin = true
					}
				})
				if fin {
					break
				}
			}
			if !fin {
				log.Printf("no change after 30s, skipping wait-for-close")
			}

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
				state.Read(func() {
					if state.DoorState != garageeventstream.DoorStateUnknown {
						changed = true
					}
				})
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
