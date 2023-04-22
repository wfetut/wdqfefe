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

package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gravitational/trace"
	"github.com/sashabaranov/go-openai"
)

type Retriever interface {
	GetRelatedDocuments(query string) ([]string, error)
}

type Tool interface {
	Name() string
	Description() string
	ReturnDirect() bool
	ArgumentSchema() []ToolArgumentField
	Run(ctx context.Context, args map[string]any) (string, error)
}

type ToolArgumentField struct {
	Name        string
	Type        string
	Instruction string
}

type Agent struct {
	Prompt string
	Tools  []Tool
}

func (a *Agent) createPrompt(systemMessage string, chatHistory []openai.ChatCompletionMessage, humanMessage, agentScratchpad string) []openai.ChatCompletionMessage {
	prompt := make([]openai.ChatCompletionMessage, 0)
	prompt = append(prompt, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleSystem,
		Content: systemMessage,
	})
	prompt = append(prompt, chatHistory...)

	toolList := strings.Builder{}
	for _, tool := range a.Tools {
		toolList.WriteString("> ")
		toolList.WriteString(tool.Name())
		toolList.WriteString(": ")
		toolList.WriteString(tool.Description())
		toolList.WriteString("\n")
	}

	newHumanMessage := fmt.Sprintf(conversationToolUsePrompt, toolList.String(), conversationParserFormatInstructionsPrompt, humanMessage)
	prompt = append(prompt, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: newHumanMessage,
	})

	prompt = append(prompt, openai.ChatCompletionMessage{
		Role:    "TBA",
		Content: agentScratchpad,
	})
	return prompt
}

type AgentAction struct {
	action      string
	actionInput string
}

type AgentFinish struct {
	output string
}

func parseConversationOutput(text string) (any, error) {
	cleaned := strings.TrimSpace(text)
	if strings.Contains(cleaned, "```json") {
		cleaned = strings.Split(cleaned, "```json")[1]
	}
	if strings.Contains(cleaned, "```") {
		cleaned = strings.Split(cleaned, "```")[0]
	}
	if strings.HasPrefix(cleaned, "```json") {
		cleaned = cleaned[len("```json"):]
	}
	if strings.HasPrefix(cleaned, "```") {
		cleaned = cleaned[len("```"):]
	}
	if strings.HasSuffix(cleaned, "```") {
		cleaned = cleaned[:len("```")]
	}
	cleaned = strings.TrimSpace(cleaned)
	var response map[string]string
	err := json.Unmarshal([]byte(cleaned), &response)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	action, actionInput := response["action"], response["action_input"]
	if action == "Final Answer" {
		return AgentFinish{output: actionInput}, nil
	}

	return AgentAction{action: action, actionInput: actionInput}, nil
}

var observationPrefix = "Observation: "
var thoughtPrefix = "Thought: "

var conversationPrefixPrompt = `Assistant is a large language model trained by OpenAI.

Assistant is designed to be able to assist with a wide range of tasks, from answering simple questions to providing in-depth explanations and discussions on a wide range of topics. As a language model, Assistant is able to generate human-like text based on the input it receives, allowing it to engage in natural-sounding conversations and provide responses that are coherent and relevant to the topic at hand.

Assistant is constantly learning and improving, and its capabilities are constantly evolving. It is able to process and understand large amounts of text, and can use this knowledge to provide accurate and informative responses to a wide range of questions. Additionally, Assistant is able to generate its own text based on the input it receives, allowing it to engage in discussions and provide explanations and descriptions on a wide range of topics.

Overall, Assistant is a powerful system that can help with a wide range of tasks and provide valuable insights and information on a wide range of topics. Whether you need help with a specific question or just want to have a conversation about a particular topic, Assistant is here to assist.`

var conversationParserFormatInstructionsPrompt = fmt.Sprintf(`RESPONSE FORMAT INSTRUCTIONS
----------------------------

When responding to me, please output a response in one of two formats:

**Option 1:**
Use this if you want the human to use a tool.
Markdown code snippet formatted in the following schema:

%vjson
{
    "action": string \\ The action to take. Must be one of {tool_names}
    "action_input": string \\ The input to the action
}
%v

**Option #2:**
Use this if you want to respond directly to the human. Markdown code snippet formatted in the following schema:

%vjson
{
    "action": "Final Answer",
    "action_input": string \\ You should put what you want to return to use here
}
%v`, "```", "```", "```", "```")

var conversationToolUsePrompt = `TOOLS
------
Assistant can ask the user to use tools to look up information that may be helpful in answering the users original question. The tools the human can use are:

%v

%v

USER'S INPUT
--------------------
Here is the user's input (remember to respond with a markdown code snippet of a json blob with a single action, and NOTHING else):

%v`
