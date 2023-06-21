/*

 Copyright 2023 Gravitational, Inc.

 Licensed under the Apache License, Version 2.0 (the "License");
 you may not use this file except in compliance with the License.
 You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.

*/

package assist

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gravitational/trace"
	"github.com/gravitational/trace/trail"
	"github.com/jonboulle/clockwork"
	"github.com/sashabaranov/go-openai"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gravitational/teleport/api/gen/proto/go/assist/v1"
	pluginsv1 "github.com/gravitational/teleport/api/gen/proto/go/teleport/plugins/v1"
	"github.com/gravitational/teleport/lib/ai"
	"github.com/gravitational/teleport/lib/ai/model"
	"github.com/gravitational/teleport/lib/auth"
)

// MessageType is a type of the Assist message.
type MessageType string

const (
	// MessageKindCommand is the type of Assist message that contains the command to execute.
	MessageKindCommand MessageType = "COMMAND"
	// MessageKindCommandResult is the type of Assist message that contains the command execution result.
	MessageKindCommandResult MessageType = "COMMAND_RESULT"
	// MessageKindUserMessage is the type of Assist message that contains the user message.
	MessageKindUserMessage MessageType = "CHAT_MESSAGE_USER"
	// MessageKindAssistantMessage is the type of Assist message that contains the assistant message.
	MessageKindAssistantMessage MessageType = "CHAT_MESSAGE_ASSISTANT"
	// MessageKindAssistantPartialMessage is the type of Assist message that contains the assistant partial message.
	MessageKindAssistantPartialMessage MessageType = "CHAT_PARTIAL_MESSAGE_ASSISTANT"
	// MessageKindAssistantPartialFinalize is the type of Assist message that ends the partial message stream.
	MessageKindAssistantPartialFinalize MessageType = "CHAT_PARTIAL_MESSAGE_ASSISTANT_FINALIZE"
	// MessageKindSystemMessage is the type of Assist message that contains the system message.
	MessageKindSystemMessage MessageType = "CHAT_MESSAGE_SYSTEM"
	// MessageKindError is the type of Assist message that is presented to user as information, but not stored persistently in the conversation. This can include backend error messages and the like.
	MessageKindError MessageType = "CHAT_MESSAGE_ERROR"
)

// Assist is the Teleport Assist client.
type Assist struct {
	client *ai.Client
	// clock is a clock used to generate timestamps.
	clock clockwork.Clock
}

// NewAssist creates a new Assist client.
func NewAssist(ctx context.Context, proxyClient auth.ClientI,
	proxySettings any, openaiCfg *openai.ClientConfig) (*Assist, error) {

	client, err := getAssistantClient(ctx, proxyClient, proxySettings, openaiCfg)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	return &Assist{
		client: client,
		clock:  clockwork.NewRealClock(),
	}, nil
}

// Chat is a Teleport Assist chat.
type Chat struct {
	assist *Assist
	chat   *ai.Chat
	// authClient is the auth server client.
	authClient auth.ClientI
	// ConversationID is the ID of the conversation.
	ConversationID string
	// Username is the username of the user who started the chat.
	Username string
}

// NewChat creates a new Assist chat.
func (a *Assist) NewChat(ctx context.Context, authClient auth.ClientI,
	conversationID string, username string,
) (*Chat, error) {
	aichat := a.client.NewChat(authClient.EmbeddingClient(), username)

	chat := &Chat{
		assist:         a,
		chat:           aichat,
		authClient:     authClient,
		ConversationID: conversationID,
		Username:       username,
	}

	if err := chat.loadMessages(ctx); err != nil {
		return nil, trace.Wrap(err)
	}

	return chat, nil
}

// GenerateSummary generates a summary for the given message.
func (a *Assist) GenerateSummary(ctx context.Context, message string) (string, error) {
	return a.client.Summary(ctx, message)
}

// loadMessages loads the messages from the database.
func (c *Chat) loadMessages(ctx context.Context) error {
	// existing conversation, retrieve old messages
	messages, err := c.authClient.GetAssistantMessages(ctx, &assist.GetAssistantMessagesRequest{
		ConversationId: c.ConversationID,
		Username:       c.Username,
	})
	if err != nil {
		return trace.Wrap(err)
	}

	// restore conversation context.
	for _, msg := range messages.GetMessages() {
		role := kindToRole(MessageType(msg.Type))
		if role != "" {
			c.chat.Insert(role, msg.Payload)
		}
	}

	return nil
}

// IsNewConversation returns true if the conversation has no messages yet.
func (c *Chat) IsNewConversation() bool {
	return len(c.chat.GetMessages()) == 1
}

// getAssistantClient returns the OpenAI client created base on Teleport Plugin information
// or the static token configured in YAML.
func getAssistantClient(ctx context.Context, proxyClient auth.ClientI,
	proxySettings any, openaiCfg *openai.ClientConfig,
) (*ai.Client, error) {
	apiKey, err := getOpenAITokenFromDefaultPlugin(ctx, proxyClient)
	if err == nil {
		return ai.NewClient(apiKey), nil
	} else if !trace.IsNotFound(err) && !trace.IsNotImplemented(err) {
		// We ignore 2 types of errors here.
		// Unimplemented may be raised by the OSS server,
		// as PluginsService does not exist there yet.
		// NotFound means plugin does not exist,
		// in which case we should fall back on the static token configured in YAML.
		log.WithError(err).Error("Unexpected error fetching default OpenAI plugin")
	}

	// If the default plugin is not configured, try to get the token from the proxy settings.
	keyGetter, found := proxySettings.(interface{ GetOpenAIAPIKey() string })
	if !found {
		return nil, trace.Errorf("GetOpenAIAPIKey is not implemented on %T", proxySettings)
	}

	apiKey = keyGetter.GetOpenAIAPIKey()
	if apiKey == "" {
		return nil, trace.Errorf("OpenAI API key is not set")
	}

	// Allow using the passed config if passed.
	if openaiCfg != nil {
		return ai.NewClientFromConfig(*openaiCfg), nil
	}
	return ai.NewClient(apiKey), nil
}

// ProcessComplete processes the completion request and returns the number of tokens used.
func (c *Chat) ProcessComplete(ctx context.Context,
	onMessage func(kind MessageType, payload []byte, createdTime time.Time) error, userInput string,
) (*model.TokensUsed, error) {
	var tokensUsed *model.TokensUsed

	// query the assistant and fetch an answer
	message, err := c.chat.Complete(ctx, userInput)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	// write the user message to persistent storage and the chat structure
	c.chat.Insert(openai.ChatMessageRoleUser, userInput)
	if err := c.authClient.CreateAssistantMessage(ctx, &assist.CreateAssistantMessageRequest{
		Message: &assist.AssistantMessage{
			Type:        string(MessageKindUserMessage),
			Payload:     userInput, // TODO(jakule): Sanitize the payload
			CreatedTime: timestamppb.New(c.assist.clock.Now().UTC()),
		},
		ConversationId: c.ConversationID,
		Username:       c.Username,
	}); err != nil {
		return nil, trace.Wrap(err)
	}

	switch message := message.(type) {
	case *model.Message:
		tokensUsed = message.TokensUsed
		c.chat.Insert(openai.ChatMessageRoleAssistant, message.Content)

		// write an assistant message to persistent storage
		protoMsg := &assist.CreateAssistantMessageRequest{
			ConversationId: c.ConversationID,
			Username:       c.Username,
			Message: &assist.AssistantMessage{
				Type:        string(MessageKindAssistantMessage),
				Payload:     message.Content,
				CreatedTime: timestamppb.New(c.assist.clock.Now().UTC()),
			},
		}

		if err := c.authClient.CreateAssistantMessage(ctx, protoMsg); err != nil {
			return nil, trace.Wrap(err)
		}

		if err := onMessage(MessageKindAssistantMessage, []byte(message.Content), c.assist.clock.Now().UTC()); err != nil {
			return nil, trace.Wrap(err)
		}
	case *model.CompletionCommand:
		tokensUsed = message.TokensUsed
		payload := commandPayload{
			Command: message.Command,
			Nodes:   message.Nodes,
			Labels:  message.Labels,
		}

		payloadJson, err := json.Marshal(payload)
		if err != nil {
			return nil, trace.Wrap(err)
		}

		msg := &assist.CreateAssistantMessageRequest{
			ConversationId: c.ConversationID,
			Username:       c.Username,
			Message: &assist.AssistantMessage{
				Type:        string(MessageKindCommand),
				Payload:     string(payloadJson),
				CreatedTime: timestamppb.New(c.assist.clock.Now().UTC()),
			},
		}

		if err := c.authClient.CreateAssistantMessage(ctx, msg); err != nil {
			return nil, trace.Wrap(err)
		}

		if err := onMessage(MessageKindCommand, payloadJson, c.assist.clock.Now().UTC()); nil != err {
			return nil, trace.Wrap(err)
		}
	default:
		return nil, trace.Errorf("unknown message type")
	}

	return tokensUsed, nil
}

func getOpenAITokenFromDefaultPlugin(ctx context.Context, proxyClient auth.ClientI) (string, error) {
	// Try retrieving credentials from the plugin resource first
	openaiPlugin, err := proxyClient.PluginsClient().GetPlugin(ctx, &pluginsv1.GetPluginRequest{
		Name:        "openai-default",
		WithSecrets: true,
	})
	if err != nil {
		return "", trail.FromGRPC(err)
	}

	creds := openaiPlugin.Credentials.GetBearerToken()
	if creds == nil {
		return "", trace.BadParameter("malformed credentials")
	}

	return creds.Token, nil
}

// kindToRole converts a message kind to an OpenAI role.
func kindToRole(kind MessageType) string {
	switch kind {
	case MessageKindUserMessage:
		return openai.ChatMessageRoleUser
	case MessageKindAssistantMessage:
		return openai.ChatMessageRoleAssistant
	case MessageKindSystemMessage:
		return openai.ChatMessageRoleSystem
	default:
		return ""
	}
}
