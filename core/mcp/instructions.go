package mcp

// Forwarding of upstream MCP servers' own `instructions` — the string a server may return from
// the initialize handshake describing how its tools should be used. Kept in its own file rather
// than utils.go: the aggregation format, its size bounds and the scoping rule are one coherent
// decision, and burying them among unrelated helpers made both harder to find.

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/maximhq/bifrost/core/schemas"
)

// Bounds on forwarded instructions. A gateway aggregating tens of upstreams can otherwise
// hand every caller an unbounded prefix, which on the inference path is billed on every
// request. Constants rather than config until someone needs to tune them.
const (
	maxInstructionsPerClient = 4096
	maxInstructionsTotal     = 16384

	// instructionTagPrefix matches both the opening and closing block tag ("<mcp_server"
	// and "</mcp_server>" both contain it once "/" is stripped). Upstream text containing
	// it is neutralized so a server cannot draw a boundary and speak as another.
	instructionTagPrefix = "mcp_server"
)

// GetServerInstructions returns the initialize `instructions` of every MCP client this
// request may see, labeled with the client they came from and ordered deterministically.
//
// Visibility is not decided here: it rides entirely on GetToolPerClient, which already
// applies the disabled check, the caller's include-clients/include-tools lists, and the
// client's own ToolsToExecute allow-list. A client contributing no visible tool is a client
// this caller cannot reach at all, so its instructions are withheld too — this is what keeps
// a narrowed caller (x-bf-mcp-include-clients, or a /mcp/<slug> Virtual MCP) from reading
// the guidance of a server it was never granted.
func (m *MCPManager) GetServerInstructions(ctx context.Context) []schemas.MCPServerInstructions {
	visible := m.GetToolPerClient(ctx)

	m.mu.RLock()
	defer m.mu.RUnlock()

	instructionsByName := make(map[string]string, len(m.clientMap))
	for _, client := range m.clientMap {
		if client.ServerInstructions != "" {
			instructionsByName[client.ExecutionConfig.Name] = client.ServerInstructions
		}
	}

	names := make([]string, 0, len(visible))
	for clientName, tools := range visible {
		if len(tools) == 0 || instructionsByName[clientName] == "" {
			continue
		}
		names = append(names, clientName)
	}
	slices.Sort(names)

	result := make([]schemas.MCPServerInstructions, 0, len(names))
	for _, name := range names {
		result = append(result, schemas.MCPServerInstructions{
			ClientName:   name,
			Instructions: instructionsByName[name],
		})
	}
	return result
}

// AggregateServerInstructions renders one tagged block per upstream, naming the client each
// came from so the reader can tell whose rule is whose. Returns "" for no parts, which callers
// must render as an absent field rather than an empty one.
//
// Tagged rather than markdown-headed on purpose. Upstream instructions are themselves usually
// markdown and routinely contain their own "## " headings (the MCP everything reference server
// has four), so a heading delimiter is indistinguishable from the content it wraps: a reader
// cannot tell where one server's guidance ends and the next begins. Worse, it would let an
// upstream forge a boundary and attribute its own text to a server the caller trusts. A tag the
// payload cannot contain — see sanitizeInstructionBody — is a boundary only this function can draw.
func AggregateServerInstructions(parts []schemas.MCPServerInstructions) string {
	var b strings.Builder
	remaining := maxInstructionsTotal
	written := 0

	for i, part := range parts {
		text, omitted := truncateInstructions(sanitizeInstructionBody(part.Instructions), maxInstructionsPerClient)
		block := instructionBlock(part.ClientName, text, omitted)
		separator := 0
		if written > 0 {
			separator = 2 // the "\n\n" between blocks
		}
		if separator+len(block) > remaining {
			// Out of budget: name what was dropped rather than trailing off silently,
			// so a truncated aggregate never reads like a complete one.
			b.WriteString(fmt.Sprintf("\n[%d more server(s) omitted: instruction size limit reached]", len(parts)-i))
			break
		}
		if separator > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(block)
		remaining -= separator + len(block)
		written++
	}
	return b.String()
}

// instructionBlock wraps one upstream's text in its tagged block. The client name is attribute
// escaped for the same reason the body is sanitized: it is operator-supplied, and a quote in it
// would otherwise break out of the attribute.
func instructionBlock(clientName, text string, omitted int) string {
	var b strings.Builder
	b.WriteString(`<mcp_server name="`)
	b.WriteString(escapeInstructionAttr(clientName))
	b.WriteString("\">\n")
	b.WriteString(text)
	if omitted > 0 {
		b.WriteString(fmt.Sprintf("\n[truncated: %d bytes omitted]", omitted))
	}
	b.WriteString("\n</mcp_server>")
	return b.String()
}

// sanitizeInstructionBody neutralizes any literal block tag in an upstream's text, so a boundary
// cannot be forged by the content it delimits. Both forms matter, not just the close: an opening
// tag left intact nests inside the current block and captures the real close as its own, so the
// text after it reads as though it came from whichever server the forged tag names. Replaced
// rather than dropped so the reader can see something was there — silently deleting a server's
// words would be worse than marking them.
func sanitizeInstructionBody(s string) string {
	if !strings.Contains(s, instructionTagPrefix) {
		return s
	}
	return strings.ReplaceAll(s, instructionTagPrefix, "[removed]")
}

// escapeInstructionAttr makes clientName safe inside the name="..." attribute.
func escapeInstructionAttr(s string) string {
	r := strings.NewReplacer(`&`, "&amp;", `"`, "&quot;", `<`, "&lt;", `>`, "&gt;")
	return r.Replace(s)
}

// truncateInstructions cuts s to at most limit bytes on a rune boundary, returning the kept
// text and how many bytes were dropped. Cutting mid-rune would emit invalid UTF-8, which some
// MCP clients reject for the whole initialize response rather than just the field.
func truncateInstructions(s string, limit int) (string, int) {
	if len(s) <= limit {
		return s, 0
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], len(s) - cut
}

// GetAggregatedServerInstructions renders the instructions of every client visible to ctx
// into a single labeled, size-bounded block. See GetServerInstructions for the scoping rule.
func (m *MCPManager) GetAggregatedServerInstructions(ctx context.Context) string {
	return AggregateServerInstructions(m.GetServerInstructions(ctx))
}
