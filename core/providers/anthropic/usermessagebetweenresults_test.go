package anthropic

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

// TestUserMessageBetweenParallelToolResultsFollowsTheResults checks a Responses input where a user message
// sits between the outputs of one assistant turn's parallel tool calls (a client that attaches a file read by
// the first call). Anthropic requires every tool_use of a turn to be answered in the very next user message,
// so all of the turn's tool_result blocks must lead that message and the user content must come after them.
func TestUserMessageBetweenParallelToolResultsFollowsTheResults(t *testing.T) {
	t.Parallel()
	ctx, cancel := schemas.NewBifrostContextWithCancel(context.Background())
	defer cancel()
	ptr := func(s string) *string { return &s }
	call := func(id string) schemas.ResponsesMessage {
		return schemas.ResponsesMessage{
			Type: schemas.Ptr(schemas.ResponsesMessageTypeFunctionCall),
			ResponsesToolMessage: &schemas.ResponsesToolMessage{
				CallID: ptr(id), Name: ptr("read_file"), Arguments: ptr(`{"file_path":"` + id + `"}`),
			},
		}
	}
	output := func(id, text string) schemas.ResponsesMessage {
		return schemas.ResponsesMessage{
			Type: schemas.Ptr(schemas.ResponsesMessageTypeFunctionCallOutput),
			ResponsesToolMessage: &schemas.ResponsesToolMessage{
				CallID: ptr(id),
				Output: &schemas.ResponsesToolMessageOutputStruct{ResponsesToolCallOutputStr: ptr(text)},
			},
		}
	}
	user := func(text string) schemas.ResponsesMessage {
		return schemas.ResponsesMessage{
			Type: schemas.Ptr(schemas.ResponsesMessageTypeMessage),
			Role: schemas.Ptr(schemas.ResponsesInputMessageRoleUser),
			Content: &schemas.ResponsesMessageContent{ContentBlocks: []schemas.ResponsesMessageContentBlock{
				{Type: schemas.ResponsesInputMessageContentBlockTypeText, Text: ptr(text)},
			}},
		}
	}

	msgs, _ := ConvertBifrostMessagesToAnthropicMessages(ctx, []schemas.ResponsesMessage{
		user("read both files"),
		call("toolu_a"),
		call("toolu_b"),
		output("toolu_a", "Binary content provided (1 item(s))."),
		user("attached file content"),
		output("toolu_b", "notes"),
	}, true, schemas.ResolveModelCaps(schemas.Anthropic, "claude-sonnet-4-5"))

	turn := -1
	for i, m := range msgs {
		for _, b := range m.Content.ContentBlocks {
			if b.Type == AnthropicContentBlockTypeToolUse {
				turn = i
			}
		}
	}
	if turn < 0 || turn+1 >= len(msgs) {
		t.Fatalf("expected an assistant tool_use turn followed by a user message, got %+v", msgs)
	}
	next := msgs[turn+1]
	if next.Role != AnthropicMessageRoleUser {
		t.Fatalf("message after the tool_use turn has role %s", next.Role)
	}
	answered := map[string]bool{}
	sawOther := false
	for _, b := range next.Content.ContentBlocks {
		if b.Type == AnthropicContentBlockTypeToolResult && b.ToolUseID != nil {
			if sawOther {
				t.Fatalf("tool_result %s follows other content in the answering message: %+v", *b.ToolUseID, next.Content.ContentBlocks)
			}
			answered[*b.ToolUseID] = true
		} else {
			sawOther = true
		}
	}
	if !answered["toolu_a"] || !answered["toolu_b"] {
		t.Fatalf("both tool calls must be answered in the next message, got %+v (all messages %+v)", answered, msgs)
	}
	found := false
	for _, m := range msgs[turn+1:] {
		for _, b := range m.Content.ContentBlocks {
			if b.Text != nil && *b.Text == "attached file content" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("the user message between the results was dropped: %+v", msgs)
	}
}
