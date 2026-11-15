package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/devXpro/gpt-home/internal/hub"
	"github.com/devXpro/gpt-home/pluginkit"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultTimeout = 30 * time.Second
	maxTimeout     = 300 * time.Second
)

// listTools fetches every page of tools/list and converts it. Tools that break the protocol rules are skipped with a
// warning; bad _meta values fall back to safe defaults.
func (h *Host) listTools(ctx context.Context, p *plugin, cs *mcp.ClientSession) ([]hub.Tool, error) {
	ctx, cancel := context.WithTimeout(ctx, h.timing.rpcTimeout)
	defer cancel()
	var tools []hub.Tool
	inventory := ""
	for t, err := range cs.Tools(ctx, nil) {
		if err != nil {
			return nil, err
		}
		ht, warnings, err := convertTool(p.id, t)
		for _, w := range warnings {
			h.log.Warn("plugin tool _meta", "plugin", p.id, "tool", t.Name, "problem", w)
		}
		if err != nil {
			h.log.Warn("skipping plugin tool", "plugin", p.id, "tool", t.Name, "err", err)
			continue
		}
		tools = append(tools, ht)
	}
	slices.SortFunc(tools, func(a, b hub.Tool) int { return strings.Compare(a.Name, b.Name) })
	for i := range tools {
		if !tools[i].Inventory {
			continue
		}
		if inventory != "" {
			h.log.Warn("plugin has more than one inventory tool; keeping the first", "plugin", p.id, "kept", inventory, "ignored", tools[i].Name)
			tools[i].Inventory = false
			continue
		}
		inventory = tools[i].Name
	}
	return tools, nil
}

// convertTool turns an MCP tool into a hub tool. An error means the tool must be skipped; warnings report _meta
// values that were replaced by defaults.
func convertTool(pluginID string, t *mcp.Tool) (hub.Tool, []string, error) {
	if err := pluginkit.CheckToolName(pluginID, t.Name); err != nil {
		return hub.Tool{}, nil, err
	}
	out := hub.Tool{
		PluginID:    pluginID,
		Name:        t.Name,
		Title:       t.Title,
		Description: t.Description,
		Audience:    hub.AudienceAssistant,
		Timeout:     defaultTimeout,
	}
	if out.Title == "" && t.Annotations != nil {
		out.Title = t.Annotations.Title
	}
	var err error
	if t.InputSchema == nil {
		out.InputSchema = json.RawMessage(`{"type":"object"}`)
	} else if out.InputSchema, err = json.Marshal(t.InputSchema); err != nil {
		return hub.Tool{}, nil, fmt.Errorf("input schema: %w", err)
	}
	if t.OutputSchema != nil {
		if out.OutputSchema, err = json.Marshal(t.OutputSchema); err != nil {
			return hub.Tool{}, nil, fmt.Errorf("output schema: %w", err)
		}
	}
	if t.Annotations != nil {
		if out.Annotations, err = json.Marshal(t.Annotations); err != nil {
			return hub.Tool{}, nil, fmt.Errorf("annotations: %w", err)
		}
	}

	var warnings []string
	if v, ok := t.Meta[pluginkit.MetaAudience]; ok {
		switch v {
		case string(hub.AudienceAssistant):
		case string(hub.AudienceAdmin):
			out.Audience = hub.AudienceAdmin
		default:
			// Hiding a tool by mistake is safer than exposing an admin tool to assistants.
			out.Audience = hub.AudienceAdmin
			warnings = append(warnings, fmt.Sprintf("unknown audience %v, treated as admin", v))
		}
	}
	if v, ok := t.Meta[pluginkit.MetaTimeout]; ok {
		secs, isNum := v.(float64)
		switch {
		case !isNum || !(secs > 0): // also catches NaN
			warnings = append(warnings, fmt.Sprintf("bad timeout %v, using %v", v, defaultTimeout))
		case secs > maxTimeout.Seconds():
			out.Timeout = maxTimeout
			warnings = append(warnings, fmt.Sprintf("timeout %vs capped at %v", secs, maxTimeout))
		default:
			out.Timeout = time.Duration(secs * float64(time.Second))
		}
	}
	if v, ok := t.Meta[pluginkit.MetaRole]; ok {
		switch {
		case v != "inventory":
			warnings = append(warnings, fmt.Sprintf("unknown role %v ignored", v))
		case t.Annotations == nil || !t.Annotations.ReadOnlyHint:
			warnings = append(warnings, "inventory role ignored: the tool is not readOnlyHint")
		default:
			out.Inventory = true
		}
	}
	return out, warnings, nil
}
